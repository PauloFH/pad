package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestResumo(t *testing.T) {
	n := func(tipos ...string) []agenteView {
		var ags []agenteView
		for _, tp := range tipos {
			ags = append(ags, agenteView{Tipo: tp})
		}
		return ags
	}
	for _, c := range []struct {
		ags  []agenteView
		want string
	}{
		{nil, "nenhum agente conectado"},
		{n("claude"), "1 agente: 1 Claude"},
		{n("codex", "claude", "claude", "codex", "claude"), "5 agentes: 3 Claude, 2 Codex"},
		{n("cursor"), "1 agente: 1 cursor"},
	} {
		if got := resumo(c.ags); got != c.want {
			t.Errorf("resumo(%d agentes) = %q, queria %q", len(c.ags), got, c.want)
		}
	}
}

func TestPainelMCP(t *testing.T) {
	a, srv := mcpApp(t, "ana", "bia")
	login := func(name string) *http.Client {
		jar, _ := cookiejar.New(nil)
		c := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		if _, err := c.PostForm(srv.URL+"/_login", url.Values{"name": {name}, "pass": {"senha-forte"}}); err != nil {
			t.Fatal(err)
		}
		return c
	}
	ler := func(res *http.Response, err error) (int, string) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	ana, bia := login("ana"), login("bia")

	// sem login: vai para o login
	res, err := http.DefaultClient.Get(srv.URL + "/_mcp")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.Request.URL.Path != "/_login" {
		t.Fatalf("sem login parou em %s", res.Request.URL.Path)
	}

	res, err = ana.PostForm(srv.URL+"/ana/_mcp/maquinas", url.Values{"nome": {"notebook"}})
	if err == nil && res.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("a página da chave nova sem no-store: %q", res.Header.Get("Cache-Control"))
	}
	code, body := ler(res, err)
	if code != 200 {
		t.Fatalf("cadastrar: %d", code)
	}
	for _, s := range []string{"pad_", "claude mcp add", "bearer_token_env_var", "Guarde agora", "http://127.0.0.1:", "/ana/_mcp"} {
		if !strings.Contains(body, s) {
			t.Errorf("chave nova sem %q", s)
		}
	}
	chave := regexp.MustCompile(`pad_[A-Za-z0-9]+`).FindString(body)
	if chave == "" {
		t.Fatal("a chave não apareceu")
	}

	code, body = ler(ana.Get(srv.URL + "/ana/_mcp"))
	if code != 200 || !strings.Contains(body, "notebook") || strings.Contains(body, chave) || !strings.Contains(body, "nunca") {
		t.Errorf("painel da ana: %d, tem notebook %v, tem a chave %v, nunca %v", code, strings.Contains(body, "notebook"),
			strings.Contains(body, chave), strings.Contains(body, "nunca"))
	}
	_, body = ler(bia.Get(srv.URL + "/bia/_mcp"))
	if strings.Contains(body, "notebook") {
		t.Error("a bia vê a máquina da ana")
	}

	// nome repetido e nome inválido voltam como erro na própria página
	for _, nome := range []string{"notebook", "com espaço"} {
		code, body = ler(ana.PostForm(srv.URL+"/ana/_mcp/maquinas", url.Values{"nome": {nome}}))
		if code != 400 || !strings.Contains(body, `class="err"`) || regexp.MustCompile(`pad_[A-Za-z0-9]{10}`).MatchString(body) {
			t.Errorf("nome %q: %d", nome, code)
		}
	}

	ms, _ := a.listarMaquinas(1)
	if len(ms) != 1 {
		t.Fatalf("máquinas da ana: %+v", ms)
	}
	path := srv.URL + "/bia/_mcp/maquinas/" + strconv.FormatInt(ms[0].ID, 10) + "/revogar"
	if code, _ = ler(bia.PostForm(path, nil)); code != 303 {
		t.Errorf("revogar da bia: %d", code)
	}
	if _, _, ok, _ := a.maquinaDaChave(chave); !ok {
		t.Fatal("a bia revogou a máquina da ana")
	}

	// um agente conectado aparece no JSON
	cli := mcpCli(t, srv, "ana", chave, "claude-code")
	cli.quer("apresentar", map[string]any{"apelido": "pai"}, false, "você é")
	cli.quer("criar_conversa", map[string]any{"nome": "tarefa", "mensagem": "<b>oi</b>"}, false, "")
	res, err = ana.Get(srv.URL + "/ana/_mcp/conectados")
	if err != nil {
		t.Fatal(err)
	}
	_, body = ler(res, nil)
	for _, s := range []string{`"resumo":"1 agente: 1 Claude"`, `"rotulo":"pai · claude · notebook"`, `"maquina":"notebook"`, `"tipo":"claude"`, `"conversas":["tarefa"]`} {
		if !strings.Contains(body, s) {
			t.Errorf("conectados sem %q: %s", s, body)
		}
	}
	_, body = ler(ana.Get(srv.URL + "/ana/_mcp"))
	for _, s := range []string{"1 agente: 1 Claude", `href="/ana/_mcp/c/tarefa"`, "pai · claude · notebook"} {
		if !strings.Contains(body, s) {
			t.Errorf("painel sem %q", s)
		}
	}

	res, err = ana.PostForm(srv.URL+"/ana/_mcp/maquinas/"+strconv.FormatInt(ms[0].ID, 10)+"/revogar", nil)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 303 || res.Header.Get("Location") != "/ana/_mcp" {
		t.Errorf("revogar: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	if _, _, ok, _ := a.maquinaDaChave(chave); ok {
		t.Error("a chave revogada ainda vale")
	}

	// o GET com Authorization é do protocolo MCP, não do painel
	req, _ := http.NewRequest("GET", srv.URL+"/ana/_mcp", nil)
	req.Header.Set("Authorization", "Bearer x")
	res, _ = fazer(t, req)
	if res.StatusCode != 405 || res.Header.Get("Allow") != "POST, DELETE" {
		t.Errorf("GET com Authorization: %d, Allow %q", res.StatusCode, res.Header.Get("Allow"))
	}

	// o menu da conta leva ao painel
	ana.Post(srv.URL+"/ana/doc", "text/plain", strings.NewReader("oi"))
	if _, body = ler(ana.Get(srv.URL + "/ana/doc")); !strings.Contains(body, `href="/ana/_mcp"`) {
		t.Error("menu da conta sem o link do painel")
	}
}

// entrarWeb devolve um cliente logado que não segue redirecionamentos.
func entrarWeb(t *testing.T, srv *httptest.Server, name string) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if _, err := c.PostForm(srv.URL+"/_login", url.Values{"name": {name}, "pass": {"senha-forte"}}); err != nil {
		t.Fatal(err)
	}
	return c
}

type chatMsgs struct {
	Mensagens []struct {
		ID     int64
		Rotulo string
		Humano bool
		Em     int64
		Texto  string
	}
	Agentes []string
}

// pegarMsgs lê o JSON do msgs; o status volta junto para os casos de erro.
func pegarMsgs(t *testing.T, c *http.Client, url string) (chatMsgs, int, time.Duration) {
	t.Helper()
	t0 := time.Now()
	res, err := c.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	var d chatMsgs
	if res.StatusCode == 200 {
		if err := json.Unmarshal(b, &d); err != nil {
			t.Fatalf("%s: %v", b, err)
		}
		if strings.Contains(string(b), "null") {
			t.Errorf("lista nula no JSON: %s", b)
		}
	}
	return d, res.StatusCode, time.Since(t0)
}

func TestChatMCP(t *testing.T) {
	velho := esperaWeb
	esperaWeb = 200 * time.Millisecond
	t.Cleanup(func() { esperaWeb = velho })
	a, srv := mcpApp(t, "ana", "bia")
	ana, bia := entrarWeb(t, srv, "ana"), entrarWeb(t, srv, "bia")

	res, err := ana.PostForm(srv.URL+"/ana/_mcp/c", url.Values{"nome": {" Login "}, "mensagem": {"oi <b>você</b>"}})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 303 || res.Header.Get("Location") != "/ana/_mcp/c/login" {
		t.Fatalf("criar: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	// nome repetido e inválido voltam no painel, com o erro
	for _, nome := range []string{"login", "com espaço"} {
		res, err = ana.PostForm(srv.URL+"/ana/_mcp/c", url.Values{"nome": {nome}, "mensagem": {"x"}})
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != 400 || !strings.Contains(string(b), `class="err"`) || !strings.Contains(string(b), "Conversas") {
			t.Errorf("criar %q: %d", nome, res.StatusCode)
		}
	}

	d, st, _ := pegarMsgs(t, ana, srv.URL+"/ana/_mcp/c/login/msgs?depois=0")
	if st != 200 || len(d.Mensagens) != 1 || !d.Mensagens[0].Humano || d.Mensagens[0].Rotulo != "ana" ||
		d.Mensagens[0].Texto != "oi <b>você</b>" || d.Mensagens[0].Em < time.Now().Unix()-60 || d.Agentes == nil || len(d.Agentes) != 0 {
		t.Fatalf("msgs: %d %+v", st, d)
	}
	primeira := d.Mensagens[0].ID

	// a página tem o que o script precisa; a bia não enxerga a conversa da ana
	res, err = ana.Get(srv.URL + "/ana/_mcp/c/login")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	for _, s := range []string{`id="chat"`, `data-nome="login"`, `id="msgs"`, `id="texto"`, `id="enviar"`, `id="erro"`, `role="status"`, `/_static/chat.js`, `apagar conversa`, `href="/ana/_mcp"`} {
		if res.StatusCode != 200 || !strings.Contains(string(b), s) {
			t.Errorf("página do chat (%d) sem %q", res.StatusCode, s)
		}
	}
	for _, rota := range []struct{ metodo, caminho string }{
		{"GET", "/bia/_mcp/c/login"}, {"GET", "/bia/_mcp/c/login/msgs?depois=0"},
		{"POST", "/bia/_mcp/c/login/msgs"}, {"POST", "/bia/_mcp/c/login/apagar"},
		{"GET", "/bia/_mcp/c/nao-existe"}, {"GET", "/bia/_mcp/c/com%20espaco/msgs"},
	} {
		req, _ := http.NewRequest(rota.metodo, srv.URL+rota.caminho, strings.NewReader(`{"texto":"oi"}`))
		req.Header.Set("Content-Type", "application/json")
		res, err := bia.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 404 {
			t.Errorf("bia em %s %s: %d", rota.metodo, rota.caminho, res.StatusCode)
		}
	}
	if left, _ := a.mensagens(1, "login", 0); len(left) != 1 {
		t.Fatalf("a bia mexeu na conversa da ana: %+v", left)
	}
	// sem login: vai para o login
	res, err = (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Get(srv.URL + "/ana/_mcp/c/login/msgs")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 303 && res.StatusCode != 302 {
		t.Errorf("sem login: %d", res.StatusCode)
	}

	// o agente entra na conversa e a ana, no navegador, espera a resposta
	c := mcpCli(t, srv, "ana", chaveDe(t, a, 1, "desktop"), "claude-code")
	c.quer("apresentar", map[string]any{"apelido": "pai"}, false, "você é")
	c.quer("ler", map[string]any{"conversa": "login", "tudo": true}, false, "oi")
	type volta struct {
		d   chatMsgs
		st  int
		dur time.Duration
	}
	ch := make(chan volta, 1)
	go func() {
		d, st, dur := pegarMsgs(t, ana, srv.URL+"/ana/_mcp/c/login/msgs?esperar=1&depois="+strconv.FormatInt(primeira, 10))
		ch <- volta{d, st, dur}
	}()
	c.quer("enviar", map[string]any{"conversa": "login", "texto": "resposta"}, false, "")
	select {
	case v := <-ch:
		if v.st != 200 || len(v.d.Mensagens) != 1 || v.d.Mensagens[0].Texto != "resposta" || v.d.Mensagens[0].Humano ||
			v.d.Mensagens[0].Rotulo != "pai · claude · desktop" || len(v.d.Agentes) != 1 || v.d.Agentes[0] != "pai · claude · desktop" || v.dur > 2*time.Second {
			t.Errorf("long-poll: %d %+v em %v", v.st, v.d, v.dur)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("o long-poll não voltou")
	}

	// sem novidade: volta no prazo com a lista vazia (e não nula)
	d, _, _ = pegarMsgs(t, ana, srv.URL+"/ana/_mcp/c/login/msgs?depois=0")
	if len(d.Mensagens) != 2 {
		t.Fatalf("esperava 2 mensagens: %+v", d)
	}
	d, st, dur := pegarMsgs(t, ana, srv.URL+"/ana/_mcp/c/login/msgs?esperar=1&depois="+strconv.FormatInt(d.Mensagens[1].ID, 10))
	if st != 200 || d.Mensagens == nil || len(d.Mensagens) != 0 || dur > time.Second || dur < 100*time.Millisecond {
		t.Errorf("long-poll vazio: %d %+v em %v", st, d, dur)
	}

	// enviar pelo navegador: vazia 400, grande 413, JSON ruim 400, boa 204 e o agente recebe
	post := func(corpo string) (int, string) {
		res, err := ana.Post(srv.URL+"/ana/_mcp/c/login/msgs", "application/json", strings.NewReader(corpo))
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	if st, _ := post(`{"texto":"  "}`); st != 400 {
		t.Errorf("vazia: %d", st)
	}
	if st, _ := post(`isto não é json`); st != 400 {
		t.Errorf("JSON ruim: %d", st)
	}
	if st, corpo := post(`{"texto":"` + strings.Repeat("a", maxMensagem+1) + `"}`); st != 413 || !strings.Contains(corpo, "64 KB") {
		t.Errorf("grande: %d %q", st, corpo)
	}
	if st, _ := post(`{"texto":"` + strings.Repeat("a", 3*maxMensagem) + `"}`); st != 413 {
		t.Errorf("enorme: %d", st)
	}
	if st, _ := post(`{"texto":"` + strings.Repeat(`\n`, maxMensagem-1) + `a"}`); st != 204 {
		t.Errorf("64 KiB de quebras de linha, que escapadas dobram no JSON: %d", st)
	}
	c.quer("ler", map[string]any{"conversa": "login"}, false, "") // põe o lido em dia
	if st, _ := post(`{"texto":"vai pelo navegador"}`); st != 204 {
		t.Fatalf("enviar: %d", st)
	}
	c.quer("esperar", map[string]any{"conversas": []string{"login"}}, false, "vai pelo navegador")

	// apagar: 303 para o painel e a conversa some
	res, err = ana.PostForm(srv.URL+"/ana/_mcp/c/login/apagar", nil)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 303 || res.Header.Get("Location") != "/ana/_mcp" {
		t.Errorf("apagar: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	if res, err = ana.Get(srv.URL + "/ana/_mcp/c/login"); err != nil || res.StatusCode != 404 {
		t.Errorf("depois de apagar: %v %v", res.StatusCode, err)
	}
	if _, st, _ := pegarMsgs(t, ana, srv.URL+"/ana/_mcp/c/login/msgs?depois=0"); st != 404 {
		t.Errorf("msgs depois de apagar: %d", st)
	}
}

func TestChatEncerraLongPoll(t *testing.T) {
	velho := esperaWeb
	esperaWeb = time.Minute
	t.Cleanup(func() { esperaWeb = velho })
	a, srv := mcpApp(t, "ana")
	ana := entrarWeb(t, srv, "ana")
	if err := a.criarConversa(1, "c", mensagem{Autor: "ana", Rotulo: "ana", Humano: true, Texto: "oi"}); err != nil {
		t.Fatal(err)
	}
	fim := make(chan time.Duration, 1)
	go func() {
		_, _, dur := pegarMsgs(t, ana, srv.URL+"/ana/_mcp/c/c/msgs?esperar=1&depois=1")
		fim <- dur
	}()
	a.mcp.encerrar()
	select {
	case dur := <-fim:
		if dur > 2*time.Second {
			t.Errorf("long-poll demorou %v para sair no desligamento", dur)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("o desligamento não acordou o long-poll")
	}
}

func TestEnderecoPorUsuario(t *testing.T) {
	a, srv := mcpApp(t, "ana", "bia")
	ana, bia := entrarWeb(t, srv, "ana"), entrarWeb(t, srv, "bia")
	semLogin := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	status := func(c *http.Client, metodo, caminho string) (int, string, string) {
		t.Helper()
		req, _ := http.NewRequest(metodo, srv.URL+caminho, strings.NewReader(`{"texto":"oi"}`))
		req.Header.Set("Content-Type", "application/json")
		res, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, res.Header.Get("Location"), string(b)
	}

	// o endereço sem usuário só redireciona
	if st, loc, _ := status(ana, "GET", "/_mcp"); st != 303 || loc != "/ana/_mcp" {
		t.Errorf("GET /_mcp da ana: %d %q", st, loc)
	}
	if st, loc, _ := status(semLogin, "GET", "/_mcp"); st != 303 || !strings.HasPrefix(loc, "/_login") {
		t.Errorf("GET /_mcp sem login: %d %q", st, loc)
	}
	for _, m := range []string{"POST", "DELETE"} {
		if st, _, _ := status(ana, m, "/_mcp"); st < 400 {
			t.Errorf("%s /_mcp ainda responde %d", m, st)
		}
	}

	// navegador: só o dono entra
	if st, _, body := status(ana, "GET", "/ana/_mcp"); st != 200 || !strings.Contains(body, "/ana/_mcp") {
		t.Errorf("painel da ana: %d", st)
	}
	st, _, body := status(ana, "GET", "/bia/_mcp")
	if st != 404 || !strings.Contains(body, "sem acesso") || !strings.Contains(body, "/ana/_mcp") {
		t.Errorf("ana em /bia/_mcp: %d", st)
	}
	if st, loc, _ := status(semLogin, "GET", "/ana/_mcp"); st != 303 || !strings.HasPrefix(loc, "/_login") {
		t.Errorf("painel sem login: %d %q", st, loc)
	}
	if st, _, _ := status(semLogin, "POST", "/ana/_mcp/maquinas"); st != 401 {
		t.Errorf("POST sem login: %d", st)
	}
	if err := a.criarConversa(1, "tarefa", humana(user{Name: "ana"}, "oi")); err != nil {
		t.Fatal(err)
	}
	for _, rota := range []struct{ metodo, caminho string }{
		{"POST", "/ana/_mcp/maquinas"}, {"POST", "/ana/_mcp/maquinas/1/revogar"}, {"POST", "/ana/_mcp/c"},
		{"GET", "/ana/_mcp/c/tarefa"}, {"POST", "/ana/_mcp/c/tarefa/apagar"},
	} {
		if st, _, body := status(bia, rota.metodo, rota.caminho); st != 404 || !strings.Contains(body, "sem acesso") {
			t.Errorf("bia em %s %s: %d", rota.metodo, rota.caminho, st)
		}
	}
	// as rotas JSON dão 404 simples
	for _, rota := range []struct{ metodo, caminho string }{
		{"GET", "/ana/_mcp/conectados"}, {"GET", "/ana/_mcp/c/tarefa/msgs"}, {"POST", "/ana/_mcp/c/tarefa/msgs"},
	} {
		if st, _, body := status(bia, rota.metodo, rota.caminho); st != 404 || strings.Contains(body, "<html") {
			t.Errorf("bia em %s %s: %d", rota.metodo, rota.caminho, st)
		}
	}
	if ms, _ := a.mensagens(1, "tarefa", 0); len(ms) != 1 {
		t.Errorf("a bia mexeu na conversa da ana: %+v", ms)
	}

	// MCP: a chave tem que ser do usuário do caminho
	chave := chaveDe(t, a, 1, "note")
	mcpCli(t, srv, "ana", chave, "claude-code")
	res, _ := fazer(t, mcpReq("POST", srv.URL+"/bia", chave, "", initialize("2025-06-18", "x")))
	if res.StatusCode != 401 || res.Header.Get("WWW-Authenticate") != "Bearer" {
		t.Errorf("chave da ana em /bia/_mcp: %d %q", res.StatusCode, res.Header.Get("WWW-Authenticate"))
	}
	sessao := mcpCli(t, srv, "ana", chave, "claude-code").sessao
	if res, _ := fazer(t, mcpReq("DELETE", srv.URL+"/bia", chave, sessao, "")); res.StatusCode != 401 {
		t.Errorf("DELETE com chave de outro usuário: %d", res.StatusCode)
	}
	if res, _ := fazer(t, mcpReq("DELETE", srv.URL+"/ana", chave, sessao, "")); res.StatusCode != 204 {
		t.Errorf("a sessão da ana não pôde ser encerrada: %d", res.StatusCode)
	}
	req, _ := http.NewRequest("GET", srv.URL+"/ana/_mcp", nil)
	req.Header.Set("Authorization", "Bearer "+chave)
	if res, _ := fazer(t, req); res.StatusCode != 405 || res.Header.Get("Allow") != "POST, DELETE" {
		t.Errorf("GET com Authorization: %d %q", res.StatusCode, res.Header.Get("Allow"))
	}

	// o painel e o menu mostram o endereço da conta
	res, err := ana.PostForm(srv.URL+"/ana/_mcp/maquinas", url.Values{"nome": {"desktop"}})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	body = string(b)
	if !strings.Contains(body, srv.URL+"/ana/_mcp") || strings.Contains(body, srv.URL+"/_mcp") {
		t.Errorf("comandos sem o endereço da ana: %s", body)
	}
	_, _, body = status(ana, "GET", "/ana/_mcp/c/tarefa")
	if !strings.Contains(body, `href="/ana/_mcp"`) || !strings.Contains(body, `data-base="/ana/_mcp"`) {
		t.Error("o chat não leva o endereço da conta")
	}

	// a origem de outro site continua barrada nas rotas novas
	req, _ = http.NewRequest("POST", srv.URL+"/ana/_mcp/maquinas", strings.NewReader("nome=x"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://outro.site")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	if res, err := ana.Do(req); err != nil || res.StatusCode != 403 {
		t.Errorf("POST de outra origem: %v %v", res, err)
	}

	// o resto do pad segue sem conflito de rota
	ana.Post(srv.URL+"/ana/notas", "text/plain", strings.NewReader("oi"))
	if got := get(t, ana, srv.URL+"/ana/notas"); !strings.Contains(got, "oi") {
		t.Errorf("/ana/notas: %q", got)
	}
	if st, _, _ := status(semLogin, "GET", "/_static/chat.js"); st != 200 {
		t.Errorf("/_static/chat.js: %d", st)
	}
}
