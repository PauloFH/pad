package main

import (
	"encoding/json"
	"log"
	"sync"
	"time"
)

// Hub: os documentos abertos na edição ao vivo, a gravação em disco e o
// envio às abas. Ordem dos locks: a.writeMu, depois hub.mu, depois liveDoc.mu;
// nunca hub.mu antes de a.writeMu.

var (
	flushDelay = 2 * time.Second
	// o documento fica na memória depois que o último sai, para o lastSeq
	// sobreviver a uma reconexão rápida
	keepAlive = 60 * time.Second
)

type hub struct {
	a    *app
	mu   sync.Mutex
	docs map[string]*liveDoc
}

// open devolve o documento aberto ou o carrega do disco. Cada open pede um leave.
func (h *hub) open(name string) (*liveDoc, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	d := h.docs[name]
	if d == nil {
		body, err := h.a.readDoc(name)
		if err != nil {
			return nil, err
		}
		d = newLiveDoc(name, body)
		d.stop = make(chan struct{})
		h.docs[name] = d
		go d.tick()
	}
	if d.dropT != nil {
		d.dropT.Stop()
		d.dropT = nil
	}
	d.refs++
	return d, nil
}

// leave tira p; quando sai o último, grava na hora e agenda a saída da memória.
func (h *hub) leave(d *liveDoc, p *peer) {
	d.mu.Lock()
	d.remove(p)
	d.peersSoon()
	d.mu.Unlock()
	h.mu.Lock()
	d.refs--
	last := d.refs == 0
	h.mu.Unlock()
	if !last {
		return
	}
	if err := h.flush(d); err != nil {
		log.Print(err)
	}
	h.mu.Lock()
	if d.refs == 0 && d.dropT == nil {
		d.dropT = time.AfterFunc(keepAlive, func() { h.drop(d) })
	}
	h.mu.Unlock()
}

func (h *hub) drop(d *liveDoc) {
	if err := h.flush(d); err != nil {
		log.Print(err)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if d.refs > 0 || h.docs[d.name] != d {
		return
	}
	d.mu.Lock()
	dirty := d.dirty
	d.mu.Unlock()
	if dirty { // gravação falhou: segura o texto e tenta de novo depois
		d.dropT = time.AfterFunc(keepAlive, func() { h.drop(d) })
		return
	}
	d.dropT = nil
	delete(h.docs, d.name)
	close(d.stop)
}

// connected diz se alguma aba está no documento.
func (h *hub) connected(name string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	d := h.docs[name]
	if d == nil {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.peers) > 0
}

// flush grava o texto se mudou. writeMu segura do retrato à gravação, para
// um retrato antigo nunca sobrescrever um novo.
func (h *hub) flush(d *liveDoc) error {
	h.a.writeMu.Lock()
	defer h.a.writeMu.Unlock()
	d.mu.Lock()
	dirty, body := d.dirty, ""
	if dirty {
		body, d.dirty = stringOf(d.text), false
	}
	d.mu.Unlock()
	if !dirty {
		return nil
	}
	err := h.persist(d.name, body)
	if err != nil {
		d.mu.Lock()
		d.dirty = true
		d.mu.Unlock()
	}
	return err
}

func (h *hub) persist(name, body string) error {
	if isPublico(name) {
		if err := h.a.roomPublico(name); err != nil {
			return err
		}
		return h.a.writeDoc(name, []byte(body))
	}
	if err := h.a.writeDoc(name, []byte(body)); err != nil {
		return err
	}
	_, err := h.a.db.Exec(`UPDATE docs SET updated = ? WHERE name = ?`, time.Now().Unix(), name)
	return err
}

func (h *hub) flushAll() {
	h.mu.Lock()
	ds := make([]*liveDoc, 0, len(h.docs))
	for _, d := range h.docs {
		ds = append(ds, d)
	}
	h.mu.Unlock()
	for _, d := range ds {
		if err := h.flush(d); err != nil {
			log.Print(err)
		}
	}
}

// armFlush (com d.mu) adia a gravação para flushDelay depois da última operação.
func (h *hub) armFlush(d *liveDoc) {
	if d.flushT == nil {
		d.flushT = time.AfterFunc(flushDelay, func() {
			if err := h.flush(d); err != nil {
				log.Print(err)
			}
		})
		return
	}
	d.flushT.Reset(flushDelay)
}

// tick solta as travas de quem ficou parado.
func (d *liveDoc) tick() {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-d.stop:
			return
		case now := <-t.C:
			d.mu.Lock()
			if d.expireLocks(now) {
				d.peersSoon()
			}
			d.mu.Unlock()
		}
	}
}

// peersSoon (com d.mu) agenda a presença: no máximo uma a cada 50 ms.
func (d *liveDoc) peersSoon() {
	if d.peersT != nil {
		return
	}
	d.peersT = time.AfterFunc(50*time.Millisecond, func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		d.peersT = nil
		for p := range d.peers {
			d.sendJSON(p, map[string]any{"t": "peers", "peers": d.views(p)})
		}
	})
}

func (d *liveDoc) sendJSON(p *peer, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		log.Print(err)
		return
	}
	d.enqueue(p, b)
}

// enqueue (com d.mu) põe a mensagem na fila da aba; nunca escreve no socket aqui.
func (d *liveDoc) enqueue(p *peer, b []byte) {
	if !d.peers[p] {
		return
	}
	select {
	case p.send <- b:
	default:
		// aba que não dá conta de ler: sai do documento e o escritor derruba a conexão
		d.remove(p)
		close(p.send)
		d.peersSoon()
	}
}

func (d *liveDoc) sendInit(p *peer, visitor string) {
	m := map[string]any{"t": "init", "rev": d.rev, "text": stringOf(d.text), "me": p.id,
		"canEdit": p.canEdit, "seq": d.lastSeq(p.cid)}
	if visitor != "" {
		m["visitor"] = visitor
	}
	d.sendJSON(p, m)
}
