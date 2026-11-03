package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// wsApp sobe o servidor com ana, bia e caio. A limpeza espera os handlers
// terminarem (e gravarem) antes de o TempDir ser apagado.
func wsApp(t *testing.T) (*app, *httptest.Server, func(string) *http.Client) {
	t.Helper()
	dir := t.TempDir()
	a, err := openApp(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"ana", "bia", "caio"} {
		if err := runCLI(a.db, []string{"useradd", n}, strings.NewReader("senha-forte\n")); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	h := a.routes()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wg.Add(1)
		defer wg.Done()
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(wg.Wait)
	login := func(name string) *http.Client {
		jar, _ := cookiejar.New(nil)
		c := &http.Client{Jar: jar}
		if _, err := c.PostForm(srv.URL+"/_login", url.Values{"name": {name}, "pass": {"senha-forte"}}); err != nil {
			t.Fatal(err)
		}
		return c
	}
	return a, srv, login
}

func post(t *testing.T, c *http.Client, u, ctype, body string) {
	t.Helper()
	res, err := c.Post(u, ctype, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
}

type wsMsg struct {
	T       string     `json:"t"`
	Rev     int        `json:"rev"`
	Text    string     `json:"text"`
	Me      string     `json:"me"`
	CanEdit bool       `json:"canEdit"`
	Visitor string     `json:"visitor"`
	Seq     int        `json:"seq"`
	Op      Op         `json:"op"`
	By      string     `json:"by"`
	Peers   []peerView `json:"peers"`
	Reason  string     `json:"reason"`
}

// testClient faz o papel do SyncClient: uma op pendente por vez, transformada
// contra cada op que chega; a próxima só sai depois do ack.
type testClient struct {
	c       *websocket.Conn
	cid     string
	seq     int
	rev     int
	text    []uint16
	pending Op
	init    wsMsg
	peers   []peerView
}

func wsURL(srv *httptest.Server, name, query string) string {
	return "ws" + strings.TrimPrefix(srv.URL, "http") + "/_ws/" + name + "?" + query
}

func dialWS(t *testing.T, srv *httptest.Server, hc *http.Client, name, cid, visitor string) *testClient {
	t.Helper()
	q := url.Values{"c": {cid}}
	if visitor != "" {
		q.Set("v", visitor)
	}
	c, _, err := websocket.Dial(context.Background(), wsURL(srv, name, q.Encode()), &websocket.DialOptions{HTTPClient: hc})
	if err != nil {
		t.Fatal(err)
	}
	c.SetReadLimit(8 << 20)
	t.Cleanup(func() { c.CloseNow() })
	tc := &testClient{c: c, cid: cid}
	if m, err := tc.next(); err != nil || m.T != "init" {
		t.Fatalf("primeira mensagem %+v, %v", m, err)
	}
	return tc
}

func (tc *testClient) next() (wsMsg, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, b, err := tc.c.Read(ctx)
	if err != nil {
		return wsMsg{}, err
	}
	var m wsMsg
	if err := json.Unmarshal(b, &m); err != nil {
		return m, err
	}
	switch m.T {
	case "init":
		tc.init, tc.rev, tc.text, tc.pending = m, m.Rev, utf16Of(m.Text), nil
	case "ack":
		tc.rev, tc.pending = m.Rev, nil
	case "op":
		op := m.Op
		if tc.pending != nil {
			if tc.pending, op, err = Transform(tc.pending, op); err != nil {
				return m, err
			}
		}
		if tc.text, err = op.Apply(tc.text); err != nil {
			return m, err
		}
		tc.rev = m.Rev
	case "peers":
		tc.peers = m.Peers
	}
	return m, nil
}

// until lê até chegar uma mensagem que satisfaz ok.
func (tc *testClient) until(ok func(wsMsg) bool) (wsMsg, error) {
	for {
		m, err := tc.next()
		if err != nil || ok(m) {
			return m, err
		}
	}
}

func (tc *testClient) write(v any) error {
	b, _ := json.Marshal(v)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return tc.c.Write(ctx, websocket.MessageText, b)
}

// edit aplica op localmente e a envia como pendente; cur é o cursor depois dela.
func (tc *testClient) edit(op Op, cur int) error {
	text, err := op.Apply(tc.text)
	if err != nil {
		return err
	}
	tc.text, tc.pending = text, op
	tc.seq++
	return tc.write(map[string]any{"t": "op", "cid": tc.cid, "seq": tc.seq, "rev": tc.rev, "op": op, "a": cur, "h": cur})
}

func isType(t string) func(wsMsg) bool { return func(m wsMsg) bool { return m.T == t } }

// randomOp insere (às vezes emoji) ou apaga sem cortar par substituto.
func randomOp(rng *rand.Rand, text []uint16) (Op, int) {
	n := len(text)
	edge := func(i int) int {
		if i > 0 && i < n && isLow(text[i]) {
			return i - 1
		}
		return i
	}
	var b opBuilder
	s := edge(rng.IntN(n + 1))
	if n == 0 || rng.IntN(3) > 0 {
		ins := []string{"a", "é", "😀", "ç😀z", "\n"}[rng.IntN(5)]
		b.add(Comp{Retain: s})
		b.add(Comp{Insert: ins})
		b.add(Comp{Retain: n - s})
		return Op(b), s + len(utf16Of(ins))
	}
	e := min(n, s+1+rng.IntN(3))
	if e < n && isLow(text[e]) {
		e++
	}
	if s == e {
		s = edge(s - 1)
	}
	b.add(Comp{Retain: s})
	b.add(Comp{Delete: e - s})
	b.add(Comp{Retain: n - e})
	return Op(b), s
}

func TestWSConvergencia(t *testing.T) {
	a, srv, login := wsApp(t)
	ana, bia := login("ana"), login("bia")
	post(t, ana, srv.URL+"/ana/conv", "text/plain", "início 😀 ação")
	post(t, ana, srv.URL+"/_share/ana/conv", "application/x-www-form-urlencoded", "tipo=user&alvo=bia")
	clients := []*testClient{
		dialWS(t, srv, ana, "ana/conv", "c1", ""),
		dialWS(t, srv, bia, "ana/conv", "c2", ""),
		dialWS(t, srv, ana, "ana/conv", "c3", ""),
	}
	const each = 100
	total := each * len(clients)
	var wg sync.WaitGroup
	for i, tc := range clients {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rng := rand.New(rand.NewPCG(uint64(i), 42))
			for sent := 0; sent < each || tc.pending != nil || tc.rev < total; {
				if tc.pending == nil && sent < each {
					if err := tc.edit(randomOp(rng, tc.text)); err != nil {
						t.Error(err)
						return
					}
					sent++
				}
				m, err := tc.next()
				if err != nil {
					t.Errorf("cliente %d: %v", i, err)
					return
				}
				if m.T == "reject" || m.T == "init" {
					t.Errorf("cliente %d recebeu %+v", i, m)
					return
				}
			}
		}()
	}
	wg.Wait()
	want := stringOf(clients[0].text)
	for i, tc := range clients {
		if got := stringOf(tc.text); got != want {
			t.Fatalf("cliente %d divergiu:\n%q\n%q", i, got, want)
		}
	}
	for _, tc := range clients {
		tc.c.Close(websocket.StatusNormalClosure, "")
	}
	path := filepath.Join(a.docsDir, "ana", "conv.txt")
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		b, _ := os.ReadFile(path)
		if string(b) == want {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("arquivo %q, queria %q", b, want)
		}
	}
}

func TestWSSemAcesso(t *testing.T) {
	_, srv, login := wsApp(t)
	ana := login("ana")
	post(t, ana, srv.URL+"/ana/priv", "text/plain", "segredo")
	for _, c := range []struct {
		hc   *http.Client
		want int
	}{{login("caio"), 404}, {&http.Client{}, 401}} {
		_, res, err := websocket.Dial(context.Background(), wsURL(srv, "ana/priv", "c=x"), &websocket.DialOptions{HTTPClient: c.hc})
		if err == nil || res == nil || res.StatusCode != c.want {
			t.Errorf("handshake: %v, %+v; queria %d", err, res, c.want)
		}
	}
}

func TestWSOrigemOutroSite(t *testing.T) {
	a, srv, _ := wsApp(t)
	_, res, err := websocket.Dial(context.Background(), wsURL(srv, "publico/x", "c=x"), &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": {"https://evil.example"}},
	})
	if err == nil || res == nil || res.StatusCode != http.StatusForbidden {
		t.Errorf("handshake de outra origem: %v, %+v", err, res)
	}
	a.hub.mu.Lock()
	defer a.hub.mu.Unlock()
	if len(a.hub.docs) != 0 {
		t.Error("handshake recusado carregou o documento")
	}
}

func TestWSLeitorPublico(t *testing.T) {
	_, srv, login := wsApp(t)
	ana := login("ana")
	post(t, ana, srv.URL+"/ana/pub", "text/plain", "aberto")
	post(t, ana, srv.URL+"/_public/ana/pub", "application/x-www-form-urlencoded", "public=1")
	editor := dialWS(t, srv, ana, "ana/pub", "e1", "")
	if !editor.init.CanEdit {
		t.Fatal("ana devia editar")
	}
	leitor := dialWS(t, srv, &http.Client{}, "ana/pub", "l1", "")
	if leitor.init.CanEdit || leitor.init.Text != "aberto" || leitor.init.Visitor == "" {
		t.Fatalf("init do leitor %+v", leitor.init)
	}
	if _, err := leitor.until(func(m wsMsg) bool { return m.T == "peers" && len(m.Peers) == 2 }); err != nil {
		t.Fatal(err)
	}
	for _, p := range leitor.peers {
		if p.Name == "ana" || p.Name == "" {
			t.Errorf("leitor vê o nome %q", p.Name)
		}
	}
	if err := leitor.edit(Op{{Retain: 6}, {Insert: "!"}}, 7); err != nil {
		t.Fatal(err)
	}
	m, err := leitor.until(func(m wsMsg) bool { return m.T != "peers" })
	if err != nil || m.T != "reject" {
		t.Fatalf("op do leitor: %+v, %v", m, err)
	}
	if m, err = leitor.until(func(m wsMsg) bool { return m.T != "peers" }); err != nil || m.T != "init" || m.Text != "aberto" {
		t.Fatalf("depois do reject: %+v, %v", m, err)
	}
}

func TestWSPublicoVisitante(t *testing.T) {
	_, srv, login := wsApp(t)
	ana := login("ana")
	self := func(tc *testClient) string {
		t.Helper()
		if _, err := tc.until(func(m wsMsg) bool { return m.T == "peers" && len(m.Peers) > 0 }); err != nil {
			t.Fatal(err)
		}
		for _, p := range tc.peers {
			if p.ID == tc.init.Me {
				return p.Name
			}
		}
		t.Fatalf("a própria aba não está nos peers: %+v", tc.peers)
		return ""
	}
	tc := dialWS(t, srv, ana, "publico/sala", "p1", "")
	token := tc.init.Visitor
	name := self(tc)
	if token == "" || name == "" || name == "ana" {
		t.Fatalf("visitante %q, nome %q", token, name)
	}
	tc.c.Close(websocket.StatusNormalClosure, "")
	tc = dialWS(t, srv, ana, "publico/sala", "p2", token)
	if tc.init.Visitor != "" {
		t.Errorf("token válido não devia gerar outro: %q", tc.init.Visitor)
	}
	if got := self(tc); got != name {
		t.Errorf("nome mudou ao reconectar: %q, antes %q", got, name)
	}
}

func TestWSRejeitaAcimaDoLimite(t *testing.T) {
	_, srv, _ := wsApp(t)
	tc := dialWS(t, srv, &http.Client{}, "publico/grande", "g1", "")
	if err := tc.edit(Op{{Insert: "antes"}}, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := tc.until(isType("ack")); err != nil {
		t.Fatal(err)
	}
	if err := tc.edit(Op{{Retain: 5}, {Insert: strings.Repeat("x", maxPublicoDoc)}}, 5); err != nil {
		t.Fatal(err)
	}
	m, err := tc.until(func(m wsMsg) bool { return m.T != "peers" })
	if err != nil || m.T != "reject" || m.Reason != "documento da área pública passa de 256 KB" {
		t.Fatalf("op grande: %+v, %v", m.Reason, err)
	}
	if m, err = tc.until(func(m wsMsg) bool { return m.T != "peers" }); err != nil || m.T != "init" || m.Text != "antes" {
		t.Fatalf("depois do reject: %q %q, %v", m.T, m.Text, err)
	}
}

func TestWSCriaDocumento(t *testing.T) {
	a, srv, login := wsApp(t)
	tc := dialWS(t, srv, login("ana"), "ana/novo", "n1", "")
	if !tc.init.CanEdit {
		t.Fatal("ana devia editar o próprio documento novo")
	}
	if err := tc.edit(Op{{Insert: "oi"}}, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := tc.until(isType("ack")); err != nil {
		t.Fatal(err)
	}
	var owner string
	if err := a.db.QueryRow(`SELECT u.name FROM docs d JOIN users u ON u.id = d.owner_id WHERE d.name = 'ana/novo'`).Scan(&owner); err != nil || owner != "ana" {
		t.Fatalf("dono %q, %v", owner, err)
	}
}

// Gravação pelo temporizador, seq que sobrevive à reconexão, op repetida e revisão velha.
func TestWSReconexao(t *testing.T) {
	old := flushDelay
	flushDelay = 20 * time.Millisecond
	t.Cleanup(func() { flushDelay = old })
	a, srv, login := wsApp(t)
	ana := login("ana")
	tc := dialWS(t, srv, ana, "ana/rec", "r1", "")
	if err := tc.edit(Op{{Insert: "oi"}}, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := tc.until(isType("ack")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(a.docsDir, "ana", "rec.txt")
	for deadline := time.Now().Add(2 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if b, _ := os.ReadFile(path); string(b) == "oi" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("não gravou com a aba aberta")
		}
	}
	tc.c.Close(websocket.StatusNormalClosure, "")
	tc = dialWS(t, srv, ana, "ana/rec", "r1", "")
	if tc.init.Seq != 1 || tc.init.Text != "oi" || tc.init.Rev != 1 {
		t.Fatalf("init depois de reconectar: %+v", tc.init)
	}
	// a mesma seq de novo: ack da revisão atual, sem aplicar outra vez
	if err := tc.write(map[string]any{"t": "op", "cid": "r1", "seq": 1, "rev": 0, "op": Op{{Insert: "oi"}}, "a": 2, "h": 2}); err != nil {
		t.Fatal(err)
	}
	m, err := tc.until(func(m wsMsg) bool { return m.T != "peers" })
	if err != nil || m.T != "ack" || m.Rev != 1 {
		t.Fatalf("op repetida: %+v, %v", m, err)
	}
	// revisão que o servidor não conhece: só init, sem reject, para o cliente fazer rebase
	if err := tc.write(map[string]any{"t": "op", "cid": "r1", "seq": 2, "rev": 99, "op": Op{{Retain: 2}, {Insert: "!"}}, "a": 3, "h": 3}); err != nil {
		t.Fatal(err)
	}
	if m, err = tc.until(func(m wsMsg) bool { return m.T != "peers" }); err != nil || m.T != "init" || m.Text != "oi" {
		t.Fatalf("revisão velha: %+v, %v", m, err)
	}
}

func TestHubPrazoNaMemoria(t *testing.T) {
	old := keepAlive
	keepAlive = 20 * time.Millisecond
	t.Cleanup(func() { keepAlive = old })
	a, _, _ := wsApp(t)
	h := a.hub
	join := func() (*liveDoc, *peer) {
		d, err := h.open("publico/x")
		if err != nil {
			t.Fatal(err)
		}
		p := &peer{id: "1", key: "v:1", canEdit: true, send: make(chan []byte, 256)}
		d.mu.Lock()
		d.add(p)
		d.mu.Unlock()
		return d, p
	}
	d, p := join()
	if !h.connected("publico/x") {
		t.Fatal("devia estar conectado")
	}
	h.leave(d, p)
	if h.connected("publico/x") {
		t.Fatal("sem abas não está conectado")
	}
	// volta dentro do prazo: o mesmo documento (e o lastSeq de antes)
	d2, p2 := join()
	if d2 != d {
		t.Fatal("reconexão rápida devia reaproveitar o documento")
	}
	h.leave(d2, p2)
	for deadline := time.Now().Add(2 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		h.mu.Lock()
		n := len(h.docs)
		h.mu.Unlock()
		if n == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("documento não saiu da memória depois do prazo")
		}
	}
}

// fila cheia: a aba sai do documento e o canal fecha, para o escritor derrubar a conexão.
func TestEnqueueCheio(t *testing.T) {
	d := newLiveDoc("publico/x", "")
	p := &peer{id: "1", key: "v:1", send: make(chan []byte, 1)}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.add(p)
	d.enqueue(p, []byte("a"))
	d.enqueue(p, []byte("b"))
	if d.peers[p] {
		t.Fatal("aba com fila cheia continua no documento")
	}
	<-p.send
	if _, ok := <-p.send; ok {
		t.Fatal("canal devia estar fechado")
	}
	d.enqueue(p, []byte("c")) // já fora: não envia nem fecha de novo
}

// eventually espera ok virar verdade em até 2 s.
func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); !ok(); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal(what)
		}
	}
}

// closedWith lê até a conexão fechar (em até 1 s) e devolve o código e o motivo.
func closedWith(t *testing.T, c *websocket.Conn) websocket.CloseError {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for {
		_, _, err := c.Read(ctx)
		if err == nil {
			continue
		}
		var ce websocket.CloseError
		if !errors.As(err, &ce) {
			t.Fatalf("conexão não fechou com código: %v", err)
		}
		return ce
	}
}

func postStatus(t *testing.T, c *http.Client, u, body string) (int, string) {
	t.Helper()
	res, err := c.Post(u, "text/plain", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, strings.TrimSpace(string(b))
}

const accessMsg = "seu acesso a este documento mudou: recarregue a página"

func TestWSPerdeAcesso(t *testing.T) {
	_, srv, login := wsApp(t)
	ana, bia := login("ana"), login("bia")
	post(t, ana, srv.URL+"/ana/doc", "text/plain", "oi")
	post(t, ana, srv.URL+"/_share/ana/doc", "application/x-www-form-urlencoded", "tipo=user&alvo=bia")
	tc := dialWS(t, srv, bia, "ana/doc", "b1", "")
	dona := dialWS(t, srv, ana, "ana/doc", "a1", "")
	post(t, ana, srv.URL+"/_unshare/ana/doc", "application/x-www-form-urlencoded", "tipo=user&alvo=bia")
	if ce := closedWith(t, tc.c); ce.Code != 4000 || ce.Reason != accessMsg {
		t.Fatalf("fechou com %+v", ce)
	}
	// a dona continua: o acesso dela não mudou
	if err := dona.edit(Op{{Retain: 2}, {Insert: "!"}}, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := dona.until(isType("ack")); err != nil {
		t.Fatal(err)
	}
	// leitor que vira editor também recarrega (canEdit mudou)
	post(t, ana, srv.URL+"/_public/ana/doc", "application/x-www-form-urlencoded", "public=1")
	leitor := dialWS(t, srv, bia, "ana/doc", "b2", "")
	if leitor.init.CanEdit {
		t.Fatal("bia devia só ler")
	}
	post(t, ana, srv.URL+"/_share/ana/doc", "application/x-www-form-urlencoded", "tipo=user&alvo=bia")
	if ce := closedWith(t, leitor.c); ce.Code != 4000 || ce.Reason != accessMsg {
		t.Fatalf("leitor fechou com %+v", ce)
	}
}

func TestWSApagado(t *testing.T) {
	old := flushDelay
	flushDelay = 20 * time.Millisecond
	t.Cleanup(func() { flushDelay = old })
	a, srv, login := wsApp(t)
	ana := login("ana")
	path := filepath.Join(a.docsDir, "ana", "doc.txt")
	gone := func() {
		t.Helper()
		time.Sleep(5 * flushDelay)
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("arquivo voltou: %v", err)
		}
		var n int
		a.db.QueryRow(`SELECT COUNT(*) FROM docs WHERE name = 'ana/doc'`).Scan(&n)
		if n != 0 {
			t.Fatal("registro voltou")
		}
	}
	post(t, ana, srv.URL+"/ana/doc", "text/plain", "velho")
	tc := dialWS(t, srv, ana, "ana/doc", "a1", "")
	if err := tc.edit(Op{{Retain: 5}, {Insert: "!"}}, 6); err != nil {
		t.Fatal(err)
	}
	if _, err := tc.until(isType("ack")); err != nil {
		t.Fatal(err)
	}
	post(t, ana, srv.URL+"/_delete/ana/doc", "application/x-www-form-urlencoded", "")
	if ce := closedWith(t, tc.c); ce.Code != 4000 || ce.Reason != "documento apagado" {
		t.Fatalf("fechou com %+v", ce)
	}
	gone()

	// apagado no prazo da memória (sem abas): a reconexão não traz o texto de volta
	post(t, ana, srv.URL+"/ana/doc", "text/plain", "outro")
	tc = dialWS(t, srv, ana, "ana/doc", "a2", "")
	tc.c.Close(websocket.StatusNormalClosure, "")
	eventually(t, "aba não saiu", func() bool { return !a.hub.connected("ana/doc") })
	post(t, ana, srv.URL+"/_delete/ana/doc", "application/x-www-form-urlencoded", "")
	tc = dialWS(t, srv, ana, "ana/doc", "a3", "")
	if tc.init.Text != "" || !tc.init.CanEdit {
		t.Fatalf("init depois de apagar: %+v", tc.init)
	}
	tc.c.Close(websocket.StatusNormalClosure, "")
	gone()
}

func TestWSSessaoExpira(t *testing.T) {
	old := recheckEvery
	recheckEvery = 50 * time.Millisecond
	t.Cleanup(func() { recheckEvery = old })
	a, srv, login := wsApp(t)
	ana, bia := login("ana"), login("bia")
	post(t, ana, srv.URL+"/ana/doc", "text/plain", "oi")
	post(t, ana, srv.URL+"/_share/ana/doc", "application/x-www-form-urlencoded", "tipo=user&alvo=bia")
	tc := dialWS(t, srv, bia, "ana/doc", "b1", "")
	dona := dialWS(t, srv, ana, "ana/doc", "a1", "")
	time.Sleep(3 * recheckEvery) // nada mudou: ninguém cai
	if _, err := a.db.Exec(`DELETE FROM sessions WHERE user_id = (SELECT id FROM users WHERE name = 'bia')`); err != nil {
		t.Fatal(err)
	}
	if ce := closedWith(t, tc.c); ce.Code != 4000 || ce.Reason != accessMsg {
		t.Fatalf("fechou com %+v", ce)
	}
	if err := dona.edit(Op{{Retain: 2}, {Insert: "!"}}, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := dona.until(isType("ack")); err != nil {
		t.Fatalf("a ana caiu junto: %v", err)
	}
}

func TestPOSTComConexao(t *testing.T) {
	a, srv, login := wsApp(t)
	ana := login("ana")
	post(t, ana, srv.URL+"/ana/doc", "text/plain", "velho")
	tc := dialWS(t, srv, ana, "ana/doc", "a1", "")
	if st, msg := postStatus(t, ana, srv.URL+"/ana/doc", "x"); st != 409 || msg != "documento aberto na edição ao vivo: recarregue a página" {
		t.Fatalf("POST com aba aberta: %d %q", st, msg)
	}
	tc.c.Close(websocket.StatusNormalClosure, "")
	eventually(t, "aba não saiu", func() bool { return !a.hub.connected("ana/doc") })
	if st, msg := postStatus(t, ana, srv.URL+"/ana/doc", "novo"); st != 204 {
		t.Fatalf("POST sem aba: %d %q", st, msg)
	}
	// reconexão no prazo da memória: vale o texto do POST, e a op seguinte não ressuscita o velho
	tc = dialWS(t, srv, ana, "ana/doc", "a1", "")
	if tc.init.Text != "novo" {
		t.Fatalf("init depois do POST: %q", tc.init.Text)
	}
	if err := tc.edit(Op{{Retain: 4}, {Insert: "!"}}, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := tc.until(isType("ack")); err != nil {
		t.Fatal(err)
	}
	tc.c.Close(websocket.StatusNormalClosure, "")
	path := filepath.Join(a.docsDir, "ana", "doc.txt")
	eventually(t, "não gravou novo!", func() bool { b, _ := os.ReadFile(path); return string(b) == "novo!" })
}

func TestEvictPulaAberto(t *testing.T) {
	old := maxPublicoItems
	t.Cleanup(func() { maxPublicoItems = old })
	a, srv, _ := wsApp(t)
	anon := &http.Client{}
	for i, n := range []string{"a", "b"} {
		post(t, anon, srv.URL+"/publico/"+n, "text/plain", n)
		at := time.Now().Add(time.Duration(i-10) * time.Hour) // a é o mais antigo
		os.Chtimes(filepath.Join(a.docsDir, "publico", n+".txt"), at, at)
	}
	// b fica na memória (prazo de keepAlive) depois que a aba sai
	tb := dialWS(t, srv, anon, "publico/b", "b1", "")
	tb.c.Close(websocket.StatusNormalClosure, "")
	eventually(t, "aba não saiu", func() bool { return !a.hub.connected("publico/b") })
	ta := dialWS(t, srv, anon, "publico/a", "a1", "")
	maxPublicoItems = 2
	post(t, anon, srv.URL+"/publico/c", "text/plain", "c")
	for n, want := range map[string]bool{"a": true, "b": false, "c": true} {
		if _, err := os.Stat(filepath.Join(a.docsDir, "publico", n+".txt")); (err == nil) != want {
			t.Errorf("publico/%s existe = %v, queria %v", n, err == nil, want)
		}
	}
	if ta.init.Text != "a" {
		t.Fatalf("init de a: %q", ta.init.Text)
	}
	// b apagado não volta da memória
	if tb = dialWS(t, srv, anon, "publico/b", "b2", ""); tb.init.Text != "" {
		t.Fatalf("b voltou da memória: %q", tb.init.Text)
	}
}

func TestWSLimitePorIP(t *testing.T) {
	a, srv, _ := wsApp(t)
	dial := func(name, ip string) (*websocket.Conn, int) {
		h := http.Header{}
		if ip != "" {
			h.Set("X-Real-Ip", ip)
		}
		c, res, err := websocket.Dial(context.Background(), wsURL(srv, name, "c=x"), &websocket.DialOptions{HTTPHeader: h})
		if err != nil {
			if res == nil {
				t.Fatal(err)
			}
			return nil, res.StatusCode
		}
		t.Cleanup(func() { c.CloseNow() })
		return c, res.StatusCode
	}
	var first *websocket.Conn
	for i := range maxConnsPerIP {
		c, st := dial(fmt.Sprintf("publico/d%d", i), "")
		if st != 101 {
			t.Fatalf("conexão %d: %d", i+1, st)
		}
		first = cmp.Or(first, c)
	}
	if _, st := dial("publico/outro", ""); st != 429 {
		t.Fatalf("conexão %d: %d, queria 429", maxConnsPerIP+1, st)
	}
	if _, st := dial("publico/outro", "10.0.0.9"); st != 101 {
		t.Fatalf("outro IP: %d", st)
	}
	// a vaga volta quando a conexão termina
	first.Close(websocket.StatusNormalClosure, "")
	eventually(t, "vaga não voltou", func() bool {
		a.hub.mu.Lock()
		defer a.hub.mu.Unlock()
		return a.hub.perIP["127.0.0.1"] < maxConnsPerIP
	})
	if _, st := dial("publico/outro", ""); st != 101 {
		t.Fatalf("depois de liberar: %d", st)
	}

	old := maxPeersPerDoc
	maxPeersPerDoc = 2
	t.Cleanup(func() { maxPeersPerDoc = old })
	for i, want := range []int{101, 101, 429} {
		if _, st := dial("publico/cheio", fmt.Sprintf("10.1.0.%d", i)); st != want {
			t.Fatalf("aba %d do documento: %d, queria %d", i+1, st, want)
		}
	}
}

// apagado enquanto uma aba entrava: o flush não recria um arquivo sem registro.
func TestFlushSemRegistro(t *testing.T) {
	a, _, _ := wsApp(t)
	d := newLiveDoc("ana/x", "oi")
	d.dirty = true
	if err := a.hub.flush(d); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(a.docPath("ana/x")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("gravou documento sem registro: %v", err)
	}
}

// SIGTERM: as abas caem com 1001 (o cliente reconecta) e o texto vai para o disco.
func TestHubDesligamento(t *testing.T) {
	a, srv, _ := wsApp(t)
	tc := dialWS(t, srv, &http.Client{}, "publico/fim", "f1", "")
	if err := tc.edit(Op{{Insert: "fim"}}, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := tc.until(isType("ack")); err != nil {
		t.Fatal(err)
	}
	// o navegador lê enquanto o servidor fecha: o handshake de fechamento completa
	closed := make(chan error, 1)
	go func() {
		for {
			if _, _, err := tc.c.Read(context.Background()); err != nil {
				closed <- err
				return
			}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	a.hub.closeAll(ctx)
	if ctx.Err() != nil {
		t.Fatal("closeAll esperou até o prazo")
	}
	a.hub.flushAll()
	if code := websocket.CloseStatus(<-closed); code != websocket.StatusGoingAway {
		t.Fatalf("fechou com %v", code)
	}
	if b, _ := os.ReadFile(filepath.Join(a.docsDir, "publico", "fim.txt")); string(b) != "fim" {
		t.Fatalf("arquivo %q", b)
	}
	// quem termina o handshake depois disso também cai com 1001
	c, _, err := websocket.Dial(context.Background(), wsURL(srv, "publico/fim", "c=y"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	if ce := closedWith(t, c); ce.Code != websocket.StatusGoingAway {
		t.Fatalf("depois do desligamento: %+v", ce)
	}
}

// o /publico lê no máximo o dobro do seu próprio limite, não o do documento comum
func TestWSLimiteDeLeituraPublico(t *testing.T) {
	_, srv, _ := wsApp(t)
	tc := dialWS(t, srv, &http.Client{}, "publico/leitura", "l1", "")
	if err := tc.edit(Op{{Insert: strings.Repeat("x", 2*maxPublicoDoc+64<<10)}}, 0); err != nil {
		t.Fatal(err)
	}
	if ce := closedWith(t, tc.c); ce.Code != websocket.StatusMessageTooBig {
		t.Fatalf("fechou com %+v", ce)
	}
}

// o ack já na fila sai antes do fechamento: sem ele, o cliente reenviaria a op ao
// próximo processo (seqs vazio) e o texto dobraria
func TestWriteLoopAckAntesDoKick(t *testing.T) {
	for i := range 50 {
		p := &peer{send: make(chan []byte, 2), kick: make(chan closeReq, 1)}
		p.send <- []byte(`{"t":"ack","rev":1}`)
		p.kick <- closeReq{websocket.StatusGoingAway, "servidor reiniciando"}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer c.CloseNow()
			writeLoop(r.Context(), c, p)
		}))
		c, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, b, err := c.Read(ctx)
		cancel()
		if err != nil || string(b) != `{"t":"ack","rev":1}` {
			t.Fatalf("rodada %d: %q, %v", i, b, err)
		}
		if ce := closedWith(t, c); ce.Code != websocket.StatusGoingAway {
			t.Fatalf("rodada %d: fechou com %+v", i, ce)
		}
		c.CloseNow()
		srv.Close()
	}
}

func TestHubEditores(t *testing.T) {
	a, srv, login := wsApp(t)
	ana := login("ana")
	post(t, ana, srv.URL+"/ana/pub", "text/plain", "aberto")
	post(t, ana, srv.URL+"/_public/ana/pub", "application/x-www-form-urlencoded", "public=1")
	dialWS(t, srv, ana, "ana/pub", "e1", "")
	dialWS(t, srv, ana, "ana/pub", "e2", "") // duas abas da mesma pessoa contam uma vez
	dialWS(t, srv, &http.Client{}, "ana/pub", "l1", "")
	eventually(t, "editores deviam ser [ana]", func() bool { return fmt.Sprint(a.hub.editores("ana/pub")) == "[ana]" })
	dialWS(t, srv, ana, "publico/sala", "p1", "")
	if got := a.hub.editores("publico/sala"); len(got) != 0 {
		t.Errorf("editores do /publico = %v, quero []", got)
	}
}
