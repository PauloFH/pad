package main

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
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

	code, body := ler(ana.PostForm(srv.URL+"/_mcp/maquinas", url.Values{"nome": {"notebook"}}))
	if code != 200 {
		t.Fatalf("cadastrar: %d", code)
	}
	for _, s := range []string{"pad_", "claude mcp add", "bearer_token_env_var", "Guarde agora", "http://127.0.0.1:"} {
		if !strings.Contains(body, s) {
			t.Errorf("chave nova sem %q", s)
		}
	}
	chave := regexp.MustCompile(`pad_[A-Za-z0-9]+`).FindString(body)
	if chave == "" {
		t.Fatal("a chave não apareceu")
	}

	code, body = ler(ana.Get(srv.URL + "/_mcp"))
	if code != 200 || !strings.Contains(body, "notebook") || strings.Contains(body, chave) || !strings.Contains(body, "nunca") {
		t.Errorf("painel da ana: %d, tem notebook %v, tem a chave %v, nunca %v", code, strings.Contains(body, "notebook"),
			strings.Contains(body, chave), strings.Contains(body, "nunca"))
	}
	_, body = ler(bia.Get(srv.URL + "/_mcp"))
	if strings.Contains(body, "notebook") {
		t.Error("a bia vê a máquina da ana")
	}

	// nome repetido e nome inválido voltam como erro na própria página
	for _, nome := range []string{"notebook", "com espaço"} {
		code, body = ler(ana.PostForm(srv.URL+"/_mcp/maquinas", url.Values{"nome": {nome}}))
		if code != 400 || !strings.Contains(body, `class="err"`) || regexp.MustCompile(`pad_[A-Za-z0-9]{10}`).MatchString(body) {
			t.Errorf("nome %q: %d", nome, code)
		}
	}

	ms, _ := a.listarMaquinas(1)
	if len(ms) != 1 {
		t.Fatalf("máquinas da ana: %+v", ms)
	}
	path := srv.URL + "/_mcp/maquinas/" + strconv.FormatInt(ms[0].ID, 10) + "/revogar"
	if code, _ = ler(bia.PostForm(path, nil)); code != 303 {
		t.Errorf("revogar da bia: %d", code)
	}
	if _, _, ok, _ := a.maquinaDaChave(chave); !ok {
		t.Fatal("a bia revogou a máquina da ana")
	}

	// um agente conectado aparece no JSON
	cli := mcpCli(t, srv, chave, "claude-code")
	cli.quer("apresentar", map[string]any{"apelido": "pai"}, false, "você é")
	cli.quer("criar_conversa", map[string]any{"nome": "tarefa", "mensagem": "<b>oi</b>"}, false, "")
	res, err = ana.Get(srv.URL + "/_mcp/conectados")
	if err != nil {
		t.Fatal(err)
	}
	_, body = ler(res, nil)
	for _, s := range []string{`"resumo":"1 agente: 1 Claude"`, `"rotulo":"pai · claude · notebook"`, `"maquina":"notebook"`, `"tipo":"claude"`, `"conversas":["tarefa"]`} {
		if !strings.Contains(body, s) {
			t.Errorf("conectados sem %q: %s", s, body)
		}
	}
	_, body = ler(ana.Get(srv.URL + "/_mcp"))
	for _, s := range []string{"1 agente: 1 Claude", `href="/_mcp/c/tarefa"`, "pai · claude · notebook"} {
		if !strings.Contains(body, s) {
			t.Errorf("painel sem %q", s)
		}
	}

	res, err = ana.PostForm(srv.URL+"/_mcp/maquinas/"+strconv.FormatInt(ms[0].ID, 10)+"/revogar", nil)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 303 || res.Header.Get("Location") != "/_mcp" {
		t.Errorf("revogar: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	if _, _, ok, _ := a.maquinaDaChave(chave); ok {
		t.Error("a chave revogada ainda vale")
	}

	// o GET com Authorization é do protocolo MCP, não do painel
	req, _ := http.NewRequest("GET", srv.URL+"/_mcp", nil)
	req.Header.Set("Authorization", "Bearer x")
	res, _ = fazer(t, req)
	if res.StatusCode != 405 || res.Header.Get("Allow") != "POST, DELETE" {
		t.Errorf("GET com Authorization: %d, Allow %q", res.StatusCode, res.Header.Get("Allow"))
	}

	// o menu da conta leva ao painel
	ana.Post(srv.URL+"/ana/doc", "text/plain", strings.NewReader("oi"))
	if _, body = ler(ana.Get(srv.URL + "/ana/doc")); !strings.Contains(body, `href="/_mcp"`) {
		t.Error("menu da conta sem o link do painel")
	}
}
