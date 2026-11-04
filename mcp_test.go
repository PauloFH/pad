package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type mcpCliente struct {
	t                  *testing.T
	url, chave, sessao string
}

func mcpApp(t *testing.T, names ...string) (*app, *httptest.Server) {
	t.Helper()
	a := appComUsuarios(t, names...)
	srv := httptest.NewServer(a.routes())
	t.Cleanup(srv.Close)
	return a, srv
}

func chaveDe(t *testing.T, a *app, uid int64, nome string) string {
	t.Helper()
	chave, err := a.criarMaquina(uid, nome)
	if err != nil {
		t.Fatal(err)
	}
	return chave
}

func initialize(versao, cliente string) string {
	return `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"` + versao +
		`","capabilities":{},"clientInfo":{"name":"` + cliente + `","version":"1"}}}`
}

// mcpReq monta o POST /_mcp; chave e sessão vazias ficam sem header.
func mcpReq(method, url, chave, sessao, corpo string) *http.Request {
	req, _ := http.NewRequest(method, url+"/_mcp", strings.NewReader(corpo))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if chave != "" {
		req.Header.Set("Authorization", "Bearer "+chave)
	}
	if sessao != "" {
		req.Header.Set("Mcp-Session-Id", sessao)
	}
	return req
}

// fazer usa t.Error, e não t.Fatal, porque roda também em goroutines.
func fazer(t *testing.T, req *http.Request) (*http.Response, []byte) {
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Error(err)
		return &http.Response{Header: http.Header{}}, nil
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res, b
}

func mcpCli(t *testing.T, srv *httptest.Server, chave, clientName string) *mcpCliente {
	t.Helper()
	c := &mcpCliente{t: t, url: srv.URL, chave: chave}
	res, b := c.post(initialize("2025-06-18", clientName))
	c.sessao = res.Header.Get("Mcp-Session-Id")
	if res.StatusCode != 200 || c.sessao == "" {
		t.Fatalf("initialize: %d %s", res.StatusCode, b)
	}
	return c
}

func (c *mcpCliente) post(corpo string) (*http.Response, []byte) {
	return fazer(c.t, mcpReq("POST", c.url, c.chave, c.sessao, corpo))
}

func (c *mcpCliente) call(method string, params any) (json.RawMessage, int) {
	corpo, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 2, "method": method, "params": params})
	res, b := c.post(string(corpo))
	if res.StatusCode != 200 {
		return nil, res.StatusCode
	}
	var r struct {
		Result json.RawMessage
		Error  *rpcErro
	}
	if err := json.Unmarshal(b, &r); err != nil || r.Error != nil {
		c.t.Errorf("%s: %s", method, b)
	}
	return r.Result, res.StatusCode
}

func (c *mcpCliente) tool(nome string, args any) (texto string, isError bool) {
	res, st := c.call("tools/call", map[string]any{"name": nome, "arguments": args})
	if st != 200 {
		c.t.Errorf("tools/call %s: status %d", nome, st)
		return "", true
	}
	var r struct {
		Content []struct{ Type, Text string }
		IsError bool
	}
	if err := json.Unmarshal(res, &r); err != nil || len(r.Content) != 1 || r.Content[0].Type != "text" {
		c.t.Errorf("tools/call %s: %s", nome, res)
		return "", true
	}
	return r.Content[0].Text, r.IsError
}

// quer confere o texto e o isError de uma ferramenta.
func (c *mcpCliente) quer(nome string, args any, erro bool, contem string) string {
	c.t.Helper()
	texto, isError := c.tool(nome, args)
	if isError != erro || !strings.Contains(texto, contem) {
		c.t.Fatalf("%s %v: %q (isError %v), queria %q (isError %v)", nome, args, texto, isError, contem, erro)
	}
	return texto
}

func (c *mcpCliente) sessaoEm(a *app, f func(s *sessao)) {
	a.mcp.mu.Lock()
	defer a.mcp.mu.Unlock()
	f(a.mcp.sessoes[c.sessao])
}

// esperandoJa espera o esperar do cliente entrar na espera, para o teste não correr na frente.
func (c *mcpCliente) esperandoJa(a *app) {
	c.t.Helper()
	for range 200 {
		n := 0
		c.sessaoEm(a, func(s *sessao) {
			if s != nil {
				n = s.esperando
			}
		})
		if n > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	c.t.Fatal("o esperar não começou")
}

type resultado struct {
	texto string
	erro  bool
	dur   time.Duration
}

// emFundo chama a ferramenta numa goroutine e mede quanto ela levou.
func (c *mcpCliente) emFundo(nome string, args any) <-chan resultado {
	ch := make(chan resultado, 1)
	go func() {
		t0 := time.Now()
		texto, erro := c.tool(nome, args)
		ch <- resultado{texto, erro, time.Since(t0)}
	}()
	return ch
}

func receber(t *testing.T, ch <-chan resultado) resultado {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("a ferramenta não voltou")
		return resultado{}
	}
}

func TestMCPInitialize(t *testing.T) {
	a, srv := mcpApp(t, "ana")
	chave := chaveDe(t, a, 1, "desktop")
	for _, v := range [][2]string{{"2025-06-18", "2025-06-18"}, {"2025-11-25", "2025-11-25"}, {"2025-03-26", "2025-03-26"}, {"1999-01-01", "2025-06-18"}} {
		res, b := fazer(t, mcpReq("POST", srv.URL, chave, "", initialize(v[0], "claude-code")))
		var r struct {
			ID     int
			Result struct {
				ProtocolVersion string
				Capabilities    map[string]any
				ServerInfo      struct{ Name, Version string }
				Instructions    string
			}
		}
		if err := json.Unmarshal(b, &r); err != nil || res.StatusCode != 200 {
			t.Fatalf("%d %s", res.StatusCode, b)
		}
		if r.Result.ProtocolVersion != v[1] || r.ID != 1 {
			t.Fatalf("pedido %s → %s (id %d)", v[0], r.Result.ProtocolVersion, r.ID)
		}
		if res.Header.Get("Mcp-Session-Id") == "" || !strings.Contains(r.Result.Instructions, "apresentar") ||
			r.Result.ServerInfo.Name != "pad" || r.Result.Capabilities["tools"] == nil || r.Result.Capabilities["resources"] == nil {
			t.Fatalf("initialize: %v %s", res.Header, b)
		}
		if res.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("Content-Type %q", res.Header.Get("Content-Type"))
		}
	}
	if !strings.HasSuffix(instrucoes, "tudo=true.") {
		t.Fatal("instruções truncadas")
	}
}

func TestMCPAuth(t *testing.T) {
	a, srv := mcpApp(t, "ana")
	chave := chaveDe(t, a, 1, "desktop")
	res, _ := fazer(t, mcpReq("POST", srv.URL, "", "", initialize("2025-06-18", "x")))
	if res.StatusCode != 401 || res.Header.Get("WWW-Authenticate") != "Bearer" {
		t.Fatalf("sem chave: %d %q", res.StatusCode, res.Header.Get("WWW-Authenticate"))
	}
	if res, _ := fazer(t, mcpReq("POST", srv.URL, "pad_errada", "", initialize("2025-06-18", "x"))); res.StatusCode != 401 {
		t.Fatalf("chave errada: %d", res.StatusCode)
	}
	req := mcpReq("POST", srv.URL, chave, "", initialize("2025-06-18", "x"))
	req.Header.Set("Origin", "https://mal.example")
	if res, _ := fazer(t, req); res.StatusCode != 403 {
		t.Fatalf("origem de fora: %d", res.StatusCode)
	}
	req = mcpReq("POST", srv.URL, chave, "", initialize("2025-06-18", "x"))
	req.Header.Set("Origin", srv.URL)
	if res, b := fazer(t, req); res.StatusCode != 200 {
		t.Fatalf("mesma origem: %d %s", res.StatusCode, b)
	}
	ms, _ := a.listarMaquinas(1)
	if ms[0].Vista.Unix() == 0 {
		t.Fatal("a chamada aceita não marcou a máquina como vista")
	}
}

func TestMCPSessaoDesconhecida(t *testing.T) {
	a, srv := mcpApp(t, "ana")
	chave := chaveDe(t, a, 1, "desktop")
	c := mcpCli(t, srv, chave, "claude-code")
	if _, st := c.call("tools/list", nil); st != 200 {
		t.Fatalf("tools/list: %d", st)
	}
	outra := mcpCli(t, srv, chaveDe(t, a, 1, "note"), "codex")
	outra.sessao = c.sessao // sessão de outra máquina
	if _, st := outra.call("ping", nil); st != 404 {
		t.Fatalf("sessão de outra máquina: %d", st)
	}
	c.sessao = "x"
	if _, st := c.call("tools/list", nil); st != 404 {
		t.Fatalf("sessão x: %d", st)
	}
	c = mcpCli(t, srv, chave, "claude-code")
	if res, _ := fazer(t, mcpReq("DELETE", srv.URL, chave, c.sessao, "")); res.StatusCode != 204 {
		t.Fatalf("DELETE: %d", res.StatusCode)
	}
	if _, st := c.call("ping", nil); st != 404 {
		t.Fatalf("depois do DELETE: %d", st)
	}
	if res, _ := fazer(t, mcpReq("DELETE", srv.URL, chave, c.sessao, "")); res.StatusCode != 404 {
		t.Fatalf("DELETE de novo: %d", res.StatusCode)
	}
}

func TestMCPNotificacao(t *testing.T) {
	a, srv := mcpApp(t, "ana")
	c := mcpCli(t, srv, chaveDe(t, a, 1, "desktop"), "claude-code")
	res, b := c.post(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if res.StatusCode != 202 || len(b) != 0 {
		t.Fatalf("notificação: %d %q", res.StatusCode, b)
	}
}

func TestMCPErrosRPC(t *testing.T) {
	a, srv := mcpApp(t, "ana")
	c := mcpCli(t, srv, chaveDe(t, a, 1, "desktop"), "claude-code")
	for corpo, code := range map[string]int{
		`{`: -32700,
		`[{"jsonrpc":"2.0","id":1,"method":"ping"}]`:                              -32600,
		`{"jsonrpc":"2.0","id":1}`:                                                -32600,
		`{"jsonrpc":"1.0","id":1,"method":"ping"}`:                                -32600,
		`{"jsonrpc":"2.0","id":1,"method":"nada"}`:                                -32601,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"nada"}}`: -32602,
	} {
		res, b := c.post(corpo)
		var r struct{ Error *rpcErro }
		if json.Unmarshal(b, &r); res.StatusCode != 200 || r.Error == nil || r.Error.Code != code {
			t.Errorf("%s: %d %s, queria %d", corpo, res.StatusCode, b, code)
		}
	}
	if _, st := c.call("ping", nil); st != 200 {
		t.Fatalf("ping: %d", st)
	}
	res, _ := c.call("tools/list", nil)
	var l struct {
		Tools []struct {
			Name        string
			InputSchema struct{ Type string }
		}
	}
	json.Unmarshal(res, &l)
	var nomes []string
	for _, f := range l.Tools {
		if f.InputSchema.Type != "object" {
			t.Errorf("%s sem inputSchema", f.Name)
		}
		nomes = append(nomes, f.Name)
	}
	if strings.Join(nomes, ",") != "apresentar,conversas,criar_conversa,enviar,ler,esperar" {
		t.Fatalf("ferramentas: %v", nomes)
	}
}

func TestMCPApresentar(t *testing.T) {
	a, srv := mcpApp(t, "ana", "bia")
	chave := chaveDe(t, a, 1, "desktop")
	c1 := mcpCli(t, srv, chave, "claude-code")
	c1.quer("conversas", nil, true, "apresentar")
	c1.quer("apresentar", map[string]any{"apelido": "Ana"}, true, "login")
	c1.quer("apresentar", map[string]any{"apelido": "com espaço"}, true, "inválido")
	c1.quer("apresentar", map[string]any{"apelido": " Pai "}, false, "você é pai · claude · desktop")
	c1.quer("conversas", nil, false, "nenhuma conversa")

	c2 := mcpCli(t, srv, chave, "codex-mcp-client")
	c2.quer("apresentar", map[string]any{"apelido": "pai"}, true, "apelido em uso por claude · desktop")
	// outra conta pode usar o mesmo apelido
	mcpCli(t, srv, chaveDe(t, a, 2, "desktop"), "claude-code").quer("apresentar", map[string]any{"apelido": "pai"}, false, "você é pai")

	c1.sessaoEm(a, func(s *sessao) { s.ultima = time.Now().Add(-ativoJanela - time.Second) })
	c2.quer("apresentar", map[string]any{"apelido": "pai"}, false, "você é pai · codex · desktop")
	c1.quer("conversas", nil, true, "apresentar") // a sessão inativa perdeu o apelido
}

func TestMCPConversaPaiFilho(t *testing.T) {
	a, srv := mcpApp(t, "ana")
	pai := mcpCli(t, srv, chaveDe(t, a, 1, "desktop"), "claude-code")
	filho := mcpCli(t, srv, chaveDe(t, a, 1, "note"), "codex-mcp-client")
	pai.quer("apresentar", map[string]any{"apelido": "pai"}, false, "você é pai")
	pai.quer("criar_conversa", map[string]any{"nome": "Login", "mensagem": "faz o login"}, false, "conversa login criada")
	pai.quer("criar_conversa", map[string]any{"nome": "login", "mensagem": "de novo"}, true, "já existe")
	filho.quer("apresentar", map[string]any{"apelido": "login"}, false, "você é login · codex · note")
	filho.quer("ler", map[string]any{"conversa": "login"}, false, "pai · claude · desktop\nfaz o login")
	filho.quer("ler", map[string]any{"conversa": "login"}, false, "nada novo")
	filho.quer("ler", map[string]any{"conversa": "nada"}, true, "conversa não encontrada")

	espera := pai.emFundo("esperar", map[string]any{"segundos": 5})
	pai.esperandoJa(a)
	filho.quer("enviar", map[string]any{"conversa": "login", "texto": "dúvida"}, false, "enviada (#2)")
	r := receber(t, espera)
	if r.erro || !strings.HasPrefix(r.texto, "== login ==\n#2 · ") || !strings.HasSuffix(r.texto, "login · codex · note\ndúvida") || r.dur > 2*time.Second {
		t.Fatalf("esperar: %+v", r)
	}
	pai.quer("conversas", nil, false, "login · última: login · codex · note ")
	filho.quer("enviar", map[string]any{"conversa": "login", "texto": "outra"}, false, "enviada (#3)")
	if texto := pai.quer("conversas", nil, false, " · 1 não lida"); strings.Count(texto, "\n") != 0 {
		t.Fatalf("conversas: %q", texto)
	}

	now := time.Now()
	ag := a.mcp.conectados(1, now)
	if len(ag) != 2 || ag[0].Tipo != "claude" || ag[1].Tipo != "codex" || ag[1].Rotulo != "login · codex · note" ||
		len(ag[0].Conversas) != 1 || ag[0].Conversas[0] != "login" {
		t.Fatalf("conectados: %+v", ag)
	}
	if na := a.mcp.agentesNa(1, "login", now); strings.Join(na, "|") != "login · codex · note|pai · claude · desktop" {
		t.Fatalf("agentesNa: %v", na)
	}
	if len(a.mcp.conectados(2, now)) != 0 {
		t.Fatal("conectados de outra conta")
	}
}

func TestMCPEsperarPrazo(t *testing.T) {
	velho := esperaMax
	esperaMax = 200 * time.Millisecond
	t.Cleanup(func() { esperaMax = velho })
	a, srv := mcpApp(t, "ana")
	c := mcpCli(t, srv, chaveDe(t, a, 1, "desktop"), "claude-code")
	c.quer("apresentar", map[string]any{"apelido": "pai"}, false, "você é")
	t0 := time.Now()
	c.quer("esperar", nil, false, "nada ainda")
	if d := time.Since(t0); d > time.Second {
		t.Fatalf("esperar levou %v", d)
	}
}

func TestMCPRevogaDuranteEsperar(t *testing.T) {
	a, srv := mcpApp(t, "ana")
	c := mcpCli(t, srv, chaveDe(t, a, 1, "desktop"), "claude-code")
	c.quer("apresentar", map[string]any{"apelido": "pai"}, false, "você é")
	espera := c.emFundo("esperar", map[string]any{"segundos": 50})
	c.esperandoJa(a)
	ms, _ := a.listarMaquinas(1)
	if err := a.revogarMaquina(1, ms[0].ID); err != nil {
		t.Fatal(err)
	}
	if r := receber(t, espera); !r.erro || r.texto != "máquina revogada" || r.dur > time.Second {
		t.Fatalf("esperar: %+v", r)
	}
	if _, st := c.call("ping", nil); st != 401 {
		t.Fatalf("depois de revogar: %d", st)
	}
}

func TestMCPEncerrarAcordaEspera(t *testing.T) {
	a, srv := mcpApp(t, "ana")
	c := mcpCli(t, srv, chaveDe(t, a, 1, "desktop"), "claude-code")
	c.quer("apresentar", map[string]any{"apelido": "pai"}, false, "você é")
	espera := c.emFundo("esperar", map[string]any{"segundos": 50})
	c.esperandoJa(a)
	a.mcp.encerrar()
	a.mcp.encerrar() // o desligamento pode chamar mais de uma vez
	if r := receber(t, espera); r.erro || r.texto != "nada ainda" || r.dur > time.Second {
		t.Fatalf("esperar: %+v", r)
	}
}

func TestMCPLimiteSessoes(t *testing.T) {
	a, srv := mcpApp(t, "ana")
	chave := chaveDe(t, a, 1, "desktop")
	var cs []*mcpCliente
	for range 21 {
		cs = append(cs, mcpCli(t, srv, chave, "claude-code"))
	}
	if _, st := cs[0].call("ping", nil); st != 404 {
		t.Fatalf("a sessão mais antiga devia ter caído: %d", st)
	}
	if _, st := cs[1].call("ping", nil); st != 200 {
		t.Fatalf("a segunda continua: %d", st)
	}
}

func TestTipoDoCliente(t *testing.T) {
	for nome, tipo := range map[string]string{
		"claude-code":           "claude",
		"Codex-MCP-Client":      "codex",
		"cursor":                "cursor",
		strings.Repeat("a", 30): strings.Repeat("a", 20),
	} {
		if got := tipoDoCliente(nome); got != tipo {
			t.Errorf("tipoDoCliente(%q) = %q, queria %q", nome, got, tipo)
		}
	}
}

func TestFormatarMensagens(t *testing.T) {
	em := time.Date(2026, 11, 4, 14, 32, 0, 0, time.Local)
	got := formatarMensagens([]mensagem{
		{ID: 12, Rotulo: "filho · codex · note", Em: em, Texto: "um"},
		{ID: 13, Rotulo: "ana", Em: em, Texto: "dois\nlinhas"},
	})
	want := "#12 · 04/11 14:32 · filho · codex · note\num\n\n#13 · 04/11 14:32 · ana\ndois\nlinhas"
	if got != want {
		t.Fatalf("%q", got)
	}
}
