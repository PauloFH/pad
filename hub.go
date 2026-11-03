package main

import (
	"context"
	"encoding/json"
	"log"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Hub: os documentos abertos na edição ao vivo, a gravação em disco e o
// envio às abas. Ordem dos locks: a.writeMu, depois hub.mu, depois liveDoc.mu;
// nunca hub.mu antes de a.writeMu.

var (
	flushDelay = 2 * time.Second
	// o documento fica na memória depois que o último sai, para o lastSeq
	// sobreviver a uma reconexão rápida
	keepAlive = 60 * time.Second
	// reconferência da sessão e do acesso de cada aba; var para o teste baixar
	recheckEvery = time.Minute
	// vagas, reservadas antes do upgrade; var para o teste baixar
	maxConnsPerIP  = 20
	maxPeersPerDoc = 50
)

const accessChanged = "seu acesso a este documento mudou: recarregue a página"

type hub struct {
	a       *app
	mu      sync.Mutex
	docs    map[string]*liveDoc
	perIP   map[string]int // conexões por IP, no servidor todo
	perDoc  map[string]int // conexões por documento
	closing bool           // SIGTERM: nenhuma aba entra mais
}

// closeReq é o fechamento pedido ao escritor da aba.
type closeReq struct {
	code   websocket.StatusCode
	reason string
}

// open devolve o documento aberto ou o carrega do disco. Cada open pede um leave.
// writeMu: um POST não grava entre a leitura do disco e a entrada da aba.
func (h *hub) open(name string) (*liveDoc, error) {
	h.a.writeMu.Lock()
	defer h.a.writeMu.Unlock()
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
	if d.refs == 0 && d.dropT == nil && h.docs[d.name] == d {
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

// connected diz se alguma conexão segura o documento (inclui a que ainda
// está entrando, para um POST não gravar por baixo dela).
func (h *hub) connected(name string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	d := h.docs[name]
	return d != nil && d.refs > 0
}

// forget (com writeMu) tira da memória o documento sem abas, cujo arquivo
// acabou de mudar por fora: a próxima conexão lê o disco de novo.
func (h *hub) forget(name string) { h.discard(name, "", true) }

// kill (com writeMu) fecha todas as abas com 4000 e descarta o documento sem gravar.
func (h *hub) kill(name, reason string) { h.discard(name, reason, false) }

func (h *hub) discard(name, reason string, idleOnly bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	d := h.docs[name]
	if d == nil || idleOnly && d.refs > 0 {
		return
	}
	delete(h.docs, name)
	if d.dropT != nil {
		d.dropT.Stop()
		d.dropT = nil
	}
	close(d.stop)
	d.mu.Lock()
	defer d.mu.Unlock()
	// gone faz o flush ignorar o texto e barra quem ainda está entrando
	d.gone, d.dirty = closeReq{4000, reason}, false
	if d.flushT != nil {
		d.flushT.Stop()
	}
	for p := range d.peers {
		d.kick(p, d.gone)
	}
}

// join põe a aba no documento e manda o init. Se o documento foi descartado ou
// o servidor está encerrando enquanto ela entrava, devolve o fechamento.
func (h *hub) join(d *liveDoc, p *peer, visitor string) (closeReq, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	d.mu.Lock()
	defer d.mu.Unlock()
	switch {
	case h.closing:
		return closeReq{websocket.StatusGoingAway, "servidor reiniciando"}, false
	case d.gone != (closeReq{}):
		return d.gone, false
	}
	d.add(p)
	d.sendInit(p, visitor)
	d.peersSoon()
	return closeReq{}, true
}

// recheck refaz o acesso de cada aba do documento (o compartilhamento mudou).
func (h *hub) recheck(name string) {
	h.mu.Lock()
	d := h.docs[name]
	h.mu.Unlock()
	if d == nil {
		return
	}
	d.mu.Lock()
	ps := slices.Collect(maps.Keys(d.peers))
	d.mu.Unlock()
	for _, p := range ps { // as consultas ao banco ficam fora dos locks
		h.a.recheckPeer(d, p)
	}
}

// admit reserva a vaga da conexão antes do upgrade; quem recebe true chama release.
func (h *hub) admit(ip, name string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.perIP[ip] >= maxConnsPerIP || h.perDoc[name] >= maxPeersPerDoc {
		return false
	}
	h.perIP[ip]++
	h.perDoc[name]++
	return true
}

func (h *hub) release(ip, name string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.perIP[ip]--; h.perIP[ip] == 0 {
		delete(h.perIP, ip)
	}
	if h.perDoc[name]--; h.perDoc[name] == 0 {
		delete(h.perDoc, name)
	}
}

// closeAll (SIGTERM) fecha as abas com 1001, para o cliente reconectar no
// próximo processo, e espera as conexões gravarem e saírem (até ctx).
func (h *hub) closeAll(ctx context.Context) {
	h.mu.Lock()
	h.closing = true
	for _, d := range h.docs {
		d.mu.Lock()
		for p := range d.peers {
			d.kick(p, closeReq{websocket.StatusGoingAway, "servidor reiniciando"})
		}
		d.mu.Unlock()
	}
	h.mu.Unlock()
	t := time.NewTicker(10 * time.Millisecond)
	defer t.Stop()
	for {
		h.mu.Lock()
		n := len(h.perIP)
		h.mu.Unlock()
		if n == 0 {
			return
		}
		select {
		case <-t.C:
		case <-ctx.Done():
			return
		}
	}
}

// flush grava o texto se mudou. writeMu segura do retrato à gravação, para
// um retrato antigo nunca sobrescrever um novo.
func (h *hub) flush(d *liveDoc) error {
	h.a.writeMu.Lock()
	defer h.a.writeMu.Unlock()
	d.mu.Lock()
	dirty, body := d.dirty && d.gone == (closeReq{}), ""
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
	res, err := h.a.db.Exec(`UPDATE docs SET updated = ? WHERE name = ?`, time.Now().Unix(), name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil // apagado enquanto alguém entrava: não recria um arquivo sem dono
	}
	return h.a.writeDoc(name, []byte(body))
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

// kick (com d.mu) tira a aba do documento, para nenhuma op dela valer mais,
// e pede ao escritor o fechamento.
func (d *liveDoc) kick(p *peer, c closeReq) {
	if !d.peers[p] {
		return
	}
	d.remove(p)
	d.peersSoon()
	select {
	case p.kick <- c:
	default:
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

// editores dá os logins distintos de quem edita o documento agora, em ordem
// alfabética. Leitores e visitantes não têm login; no /publico ninguém tem.
func (h *hub) editores(name string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	d := h.docs[name]
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	set := map[string]bool{}
	for p := range d.peers {
		if p.canEdit && p.login != "" {
			set[p.login] = true
		}
	}
	return slices.Sorted(maps.Keys(set))
}
