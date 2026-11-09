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
	"net/netip"
	"time"

	"github.com/coder/websocket"
)

// liveIn é uma mensagem da aba.
type liveIn struct {
	T        string
	Rev, Seq int
	Op       Op
	A, H     int
}

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
	if !a.hub.admit(ip) {
		http.Error(w, "conexões demais, tente de novo mais tarde", http.StatusTooManyRequests)
		return
	}
	defer a.hub.release(ip)
	// a janela freia quem abre e fecha conexões: cada documento fica na memória por keepAlive depois de sair
	if !a.limWS.permite(ip, time.Now()) {
		http.Error(w, "conexões demais, tente de novo mais tarde", http.StatusTooManyRequests)
		return
	}
	u, logged, err := a.currentUser(w, r)
	if err != nil {
		serverError(w, err)
		return
	}
	// decidido antes do upgrade
	ac, err := a.acesso(name, u)
	switch {
	case err != nil:
		serverError(w, err)
		return
	case ac.Ver:
	case !logged:
		http.Error(w, "sessão expirada, entre de novo", http.StatusUnauthorized)
		return
	default:
		http.Error(w, "documento não encontrado ou sem acesso", http.StatusNotFound)
		return
	}
	canEdit, create := ac.Editar, ac.Criar
	vaga := vagaDoc(name, canEdit)
	if !a.hub.admitDoc(vaga) {
		http.Error(w, "conexões demais, tente de novo mais tarde", http.StatusTooManyRequests)
		return
	}
	defer a.hub.releaseDoc(vaga)
	// toda aba tem identidade de visitante: é o nome que leitores e os anônimos do /publico veem
	vid, vname, ok := a.parseVisitor(r.URL.Query().Get("v"))
	var fresh string
	if !ok {
		vid, vname, fresh = a.newVisitor()
	}
	p := &peer{id: rand.Text(), key: "v:" + vid, visitor: vname, cid: cid, canEdit: canEdit,
		active: time.Now(), send: make(chan []byte, 256), kick: make(chan closeReq, 1), u: u}
	// 7x: um init escapado pode ter 6x o documento (\u003c), e ainda sobra para o que já está na fila
	limite, _ := limiteDoc(name)
	p.teto = int64(7 * limite)
	if logged {
		ck, _ := r.Cookie(cookieSessao) // logged garante o cookie
		p.sid = ck.Value
	}
	if canEdit && logged {
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
	// o escape do JSON pode dobrar um insert grande; o limite de verdade fica no submit
	c.SetReadLimit(int64(2*d.max) + 64<<10)
	if !canEdit {
		c.SetReadLimit(4 << 10) // quem só lê manda apenas a seleção
	}
	defer a.hub.leave(d, p)
	if cr, ok := a.hub.join(d, p, fresh); !ok {
		c.Close(cr.code, cr.reason)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	written := make(chan struct{})
	go func() { writeLoop(ctx, c, p); close(written) }()
	go pingLoop(ctx, c)
	go a.recheckLoop(ctx, d, p, recheckEvery) // lido aqui: os testes trocam o valor entre conexões

	freio := ritmo{porSeg: d.max, folga: 2*d.max + 64<<10, ultimo: time.Now()}
	for {
		_, b, err := c.Read(ctx)
		if err != nil {
			return
		}
		var m liveIn
		bad := json.Unmarshal(b, &m) != nil
		if m.T == "op" && create && !bad {
			segue, err := a.hub.criar(d, p, u.ID)
			if !segue {
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
		segue, custo := a.liveMsg(d, p, m, bad)
		if !segue {
			<-written // o escritor fecha com o motivo
			return
		}
		if espera := freio.gastar(len(b)+custo, time.Now()); espera > 0 {
			select {
			case <-time.After(espera):
			case <-ctx.Done():
				return
			}
		}
	}
}

// liveMsg trata uma mensagem da aba, diz se ela segue no documento e quanto a resposta custou:
// uma mensagem pequena pode gerar um init com o documento inteiro. O defer solta d.mu mesmo
// num pânico: preso, o lock pararia o documento e depois o hub inteiro.
func (a *app) liveMsg(d *liveDoc, p *peer, m liveIn, bad bool) (segue bool, custo int) {
	now := time.Now()
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.peers[p] { // fila estourada ou acesso mudou: nada mais dela vale
		return false, 0
	}
	initCusto := 2 * len(d.text)
	switch {
	case bad:
		d.sendJSON(p, map[string]any{"t": "reject", "reason": errInvalid.Error()})
		d.sendInit(p, "")
		custo = initCusto
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
			custo = initCusto
		default:
			d.sendJSON(p, map[string]any{"t": "reject", "reason": err.Error()})
			d.sendInit(p, "")
			custo = initCusto
		}
	case m.T == "sel":
		d.setSel(p, m.Rev, m.A, m.H, now) // revisão velha: a próxima sel corrige
	}
	// até uma recusa mexe nas travas (touch), então a presença sai sempre
	d.peersSoon()
	return true, custo
}

// writeLoop é o único que escreve no socket; canal fechado = aba que não acompanhou.
func writeLoop(ctx context.Context, c *websocket.Conn, p *peer) {
	for {
		select {
		case <-ctx.Done():
			return
		case k := <-p.kick:
			// o select sorteia entre canais prontos: o que já estava na fila (um ack) sai antes.
			// Depois do kick nada mais entra na fila, e este é o único leitor dela.
			for len(p.send) > 0 {
				b := <-p.send
				p.filaBytes.Add(-int64(len(b)))
				if c.Write(ctx, websocket.MessageText, b) != nil {
					break
				}
			}
			c.Close(k.code, k.reason)
			return
		case b, ok := <-p.send:
			if !ok {
				c.Close(websocket.StatusPolicyViolation, "conexão lenta demais")
				return
			}
			p.filaBytes.Add(-int64(len(b)))
			if c.Write(ctx, websocket.MessageText, b) != nil {
				c.CloseNow()
				return
			}
		}
	}
}

// recheckPeer fecha a aba com 4000 se a sessão caiu ou se o acesso mudou desde a conexão.
func (a *app) recheckPeer(d *liveDoc, p *peer) {
	u := p.u
	if p.sid != "" {
		err := a.db.QueryRow(`SELECT 1 FROM sessions WHERE token = ? AND expires > ?`, p.sid, time.Now().Unix()).Scan(new(int))
		switch {
		case errors.Is(err, sql.ErrNoRows):
			u = user{}
		case err != nil:
			log.Print(err)
			return
		}
	}
	ac, err := a.acesso(d.name, u)
	if err != nil {
		log.Print(err)
		return
	}
	if !ac.Ver || ac.Editar != p.canEdit {
		d.mu.Lock()
		d.kick(p, closeReq{4000, accessChanged})
		d.mu.Unlock()
	}
}

func (a *app) recheckLoop(ctx context.Context, d *liveDoc, p *peer, every time.Duration) {
	t := time.NewTicker(every)
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
	ip := r.Header.Get("X-Real-Ip")
	if ip == "" {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			return r.RemoteAddr
		}
		ip = host
	}
	return chaveIP(ip)
}

// chaveIP agrupa IPv6 por /64: um único cliente costuma ter a rede inteira e trocaria de endereço a cada pedido.
func chaveIP(ip string) string {
	a, err := netip.ParseAddr(ip)
	if err != nil || !a.Is6() || a.Is4In6() {
		return ip
	}
	p, err := a.WithZone("").Prefix(64)
	if err != nil {
		return ip
	}
	return p.String()
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
