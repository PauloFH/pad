package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
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
	ip := clientIP(r)
	if !a.hub.admit(ip, name) {
		http.Error(w, "conexões demais, tente de novo mais tarde", http.StatusTooManyRequests)
		return
	}
	defer a.hub.release(ip, name)
	u, logged, err := a.currentUser(w, r)
	if err != nil {
		serverError(w, err)
		return
	}
	// mesmas regras do view, decididas antes do upgrade
	canView, canEdit, create, err := a.liveAccess(name, u, logged)
	switch {
	case err != nil:
		serverError(w, err)
		return
	case canView:
	case !logged:
		http.Error(w, "sessão expirada, entre de novo", http.StatusUnauthorized)
		return
	default:
		http.Error(w, "documento não encontrado ou sem acesso", http.StatusNotFound)
		return
	}
	// toda aba tem identidade de visitante: é o nome que leitores e o /publico veem
	vid, vname, ok := a.parseVisitor(r.URL.Query().Get("v"))
	var fresh string
	if !ok {
		vid, vname, fresh = a.newVisitor()
	}
	p := &peer{id: rand.Text(), key: "v:" + vid, visitor: vname, cid: cid, canEdit: canEdit,
		active: time.Now(), send: make(chan []byte, 256), kick: make(chan closeReq, 1), u: u, logged: logged}
	if logged {
		ck, _ := r.Cookie("sid") // logged garante o cookie
		p.sid = ck.Value
	}
	if canEdit && logged && !isPublico(name) {
		p.key, p.login = fmt.Sprintf("u:%d", u.ID), u.Name
	}

	c, err := websocket.Accept(w, r, nil) // origem diferente do Host é recusada
	if err != nil {
		return // Accept já respondeu
	}
	defer c.CloseNow()
	// só depois do handshake: um upgrade recusado não carrega documento nem liga o tique
	d, err := a.hub.open(name)
	if err != nil {
		log.Print(err)
		c.Close(websocket.StatusInternalError, "erro interno")
		return
	}
	defer a.hub.leave(d, p)
	if cr, ok := a.hub.join(d, p, fresh); !ok {
		c.Close(cr.code, cr.reason)
		return
	}
	// o escape do JSON pode dobrar um insert grande; o limite de verdade fica no submit
	c.SetReadLimit(int64(2*maxDoc) + 64<<10)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	written := make(chan struct{})
	go func() { writeLoop(ctx, c, p); close(written) }()
	go pingLoop(ctx, c)
	go a.recheckLoop(ctx, d, p)

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
			// mesmo INSERT do save: a primeira op cria o documento no espaço do usuário.
			// Sob writeMu e só se a aba segue no documento: um del não é desfeito.
			a.writeMu.Lock()
			d.mu.Lock()
			in := d.peers[p]
			d.mu.Unlock()
			if in {
				_, err = a.db.Exec(`INSERT INTO docs (name, owner_id, updated) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`,
					name, u.ID, time.Now().Unix())
			}
			a.writeMu.Unlock()
			if !in {
				<-written // o escritor fecha com o motivo
				return
			}
			if err != nil {
				log.Print(err)
				c.Close(websocket.StatusInternalError, "erro interno")
				return
			}
			create = false
		}
		now := time.Now()
		d.mu.Lock()
		if !d.peers[p] { // fila estourada ou acesso mudou: nada mais dela vale
			d.mu.Unlock()
			<-written // o escritor fecha com o motivo
			return
		}
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
func writeLoop(ctx context.Context, c *websocket.Conn, p *peer) {
	for {
		select {
		case <-ctx.Done():
			return
		case k := <-p.kick:
			c.Close(k.code, k.reason)
			return
		case b, ok := <-p.send:
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

// liveAccess decide, como o view, se a aba abre o documento, se edita e se a
// primeira op o cria. A conexão e as reconferências usam esta mesma função.
func (a *app) liveAccess(name string, u user, logged bool) (canView, canEdit, create bool, err error) {
	if isPublico(name) {
		return true, true, false, nil
	}
	exists, allowed, _, public, err := a.access(name, u.ID)
	switch {
	case err != nil:
		return false, false, false, err
	case allowed:
		return true, true, false, nil
	case logged && !exists && strings.HasPrefix(name, u.Name+"/"):
		return true, true, true, nil
	}
	return public, false, false, nil
}

// recheckPeer fecha a aba com 4000 se a sessão caiu ou se o acesso mudou desde a conexão.
func (a *app) recheckPeer(d *liveDoc, p *peer) {
	if isPublico(d.name) {
		return // aberto a todos: nada a mudar
	}
	u, logged := p.u, p.logged
	if p.sid != "" {
		err := a.db.QueryRow(`SELECT 1 FROM sessions WHERE token = ? AND expires > ?`, p.sid, time.Now().Unix()).Scan(new(int))
		switch {
		case errors.Is(err, sql.ErrNoRows):
			u, logged = user{}, false
		case err != nil:
			log.Print(err)
			return
		}
	}
	canView, canEdit, _, err := a.liveAccess(d.name, u, logged)
	if err != nil {
		log.Print(err)
		return
	}
	if !canView || canEdit != p.canEdit {
		d.mu.Lock()
		d.kick(p, closeReq{4000, accessChanged})
		d.mu.Unlock()
	}
}

// recheckLoop reconfere a aba a cada recheckEvery, até a conexão terminar.
func (a *app) recheckLoop(ctx context.Context, d *liveDoc, p *peer) {
	t := time.NewTicker(recheckEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.recheckPeer(d, p)
		}
	}
}

// shortcut: confia no X-Real-Ip do Traefik; se o serviço ficar exposto sem proxy, ler só o RemoteAddr
func clientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-Ip"); ip != "" {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
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
