package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
)

// liveWS é a conexão de uma aba com um documento: GET /_ws/{name}?c=<cid>&v=<visitante>.
func (a *app) liveWS(w http.ResponseWriter, r *http.Request) {
	name, ok := docName(w, r)
	if !ok {
		return
	}
	cid := r.URL.Query().Get("c")
	if len(cid) > 64 { // vira chave em memória enquanto o documento está aberto
		http.Error(w, "identificador de aba inválido", http.StatusBadRequest)
		return
	}
	u, logged, err := a.currentUser(w, r)
	if err != nil {
		serverError(w, err)
		return
	}
	// mesmas regras do view, decididas antes do upgrade
	canEdit, create := true, false
	if !isPublico(name) {
		exists, allowed, _, public, err := a.access(name, u.ID)
		if err != nil {
			serverError(w, err)
			return
		}
		switch {
		case allowed:
		case logged && !exists && strings.HasPrefix(name, u.Name+"/"):
			create = true
		case public:
			canEdit = false
		case !logged:
			http.Error(w, "sessão expirada, entre de novo", http.StatusUnauthorized)
			return
		default:
			http.Error(w, "documento não encontrado ou sem acesso", http.StatusNotFound)
			return
		}
	}
	// toda aba tem identidade de visitante: é o nome que leitores e o /publico veem
	vid, vname, ok := a.parseVisitor(r.URL.Query().Get("v"))
	var fresh string
	if !ok {
		vid, vname, fresh = a.newVisitor()
	}
	p := &peer{id: rand.Text(), key: "v:" + vid, visitor: vname, cid: cid, canEdit: canEdit,
		active: time.Now(), send: make(chan []byte, 256)}
	if canEdit && logged && !isPublico(name) {
		p.key, p.login = fmt.Sprintf("u:%d", u.ID), u.Name
	}

	d, err := a.hub.open(name)
	if err != nil {
		serverError(w, err)
		return
	}
	defer a.hub.leave(d, p)
	c, err := websocket.Accept(w, r, nil) // origem diferente do Host é recusada
	if err != nil {
		return // Accept já respondeu
	}
	defer c.CloseNow()
	// o escape do JSON pode dobrar um insert grande; o limite de verdade fica no submit
	c.SetReadLimit(int64(2*maxDoc) + 64<<10)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go writeLoop(ctx, c, p.send)
	go pingLoop(ctx, c)

	d.mu.Lock()
	d.add(p)
	d.sendInit(p, fresh)
	d.peersSoon()
	d.mu.Unlock()

	for {
		_, b, err := c.Read(ctx)
		if err != nil {
			return
		}
		var m struct {
			T        string
			Rev, Seq int
			Op       Op
			A, H     int
		}
		bad := json.Unmarshal(b, &m) != nil
		if m.T == "op" && create && !bad {
			// mesmo INSERT do save: a primeira op cria o documento no espaço do usuário
			if _, err := a.db.Exec(`INSERT INTO docs (name, owner_id, updated) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`,
				name, u.ID, time.Now().Unix()); err != nil {
				log.Print(err)
				c.Close(websocket.StatusInternalError, "erro interno")
				return
			}
			create = false
		}
		now := time.Now()
		d.mu.Lock()
		switch {
		case bad:
			d.sendJSON(p, map[string]any{"t": "reject", "reason": errInvalid.Error()})
			d.sendInit(p, "")
		case m.T == "op":
			op, err := d.submit(p, m.Rev, m.Seq, m.Op, m.A, m.H, now)
			switch {
			case err == nil:
				d.sendJSON(p, map[string]any{"t": "ack", "rev": d.rev})
				out, _ := json.Marshal(map[string]any{"t": "op", "rev": d.rev, "op": op, "by": p.id})
				for q := range d.peers {
					if q != p {
						d.enqueue(q, out)
					}
				}
				a.hub.armFlush(d)
			case errors.Is(err, errDuplicate): // o ack anterior se perdeu
				d.sendJSON(p, map[string]any{"t": "ack", "rev": d.rev})
			case errors.Is(err, errStale): // só init: o cliente faz rebase e não perde o pendente
				d.sendInit(p, "")
			default:
				d.sendJSON(p, map[string]any{"t": "reject", "reason": err.Error()})
				d.sendInit(p, "")
			}
		case m.T == "sel":
			d.setSel(p, m.Rev, m.A, m.H, now) // revisão velha: a próxima sel corrige
		}
		// até uma recusa mexe nas travas (touch), então a presença sai sempre
		d.peersSoon()
		d.mu.Unlock()
	}
}

// writeLoop é o único que escreve no socket; canal fechado = aba que não acompanhou.
func writeLoop(ctx context.Context, c *websocket.Conn, send chan []byte) {
	for {
		select {
		case <-ctx.Done():
			return
		case b, ok := <-send:
			if !ok {
				c.Close(websocket.StatusPolicyViolation, "conexão lenta demais")
				return
			}
			if c.Write(ctx, websocket.MessageText, b) != nil {
				c.CloseNow()
				return
			}
		}
	}
}

// pingLoop derruba a conexão que parou de responder.
func pingLoop(ctx context.Context, c *websocket.Conn) {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		pctx, done := context.WithTimeout(ctx, 5*time.Second)
		err := c.Ping(pctx)
		done()
		if err != nil {
			c.CloseNow()
			return
		}
	}
}
