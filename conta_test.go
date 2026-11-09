package main

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// contaComTudo monta ana com um pouco de cada coisa que a exclusão tem de levar: três documentos (um compartilhado
// com bia), um documento de bia compartilhado com ana, o grupo time, um convite gerado, uma máquina e uma conversa
// com mensagem e leitura.
func contaComTudo(t *testing.T) (*app, string, *httptest.Server, *http.Client) {
	t.Helper()
	dir := t.TempDir()
	a, err := openApp(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range [][]string{{"useradd", "ana"}, {"useradd", "bia"}, {"groupadd", "time"}, {"join", "time", "ana"}} {
		if err := runCLI(a.db, c, strings.NewReader("senha-forte\n")); err != nil {
			t.Fatal(c, err)
		}
	}
	srv := httptest.NewServer(a.routes())
	t.Cleanup(srv.Close)
	entrar := func(n string) *http.Client {
		jar, _ := cookiejar.New(nil)
		c := &http.Client{Jar: jar}
		if _, err := c.PostForm(srv.URL+"/_login", url.Values{"name": {n}, "pass": {"senha-forte"}}); err != nil {
			t.Fatal(err)
		}
		return c
	}
	ana, bia := entrar("ana"), entrar("bia")
	for _, d := range []string{"/ana/p", "/ana/a", "/ana/p/b"} {
		post(t, ana, srv.URL+d, "text/plain", "texto de ana")
	}
	post(t, bia, srv.URL+"/bia/x", "text/plain", "texto de bia")
	form := "application/x-www-form-urlencoded"
	post(t, ana, srv.URL+"/_share/ana/a", form, "tipo=user&alvo=bia")
	post(t, bia, srv.URL+"/_share/bia/x", form, "tipo=user&alvo=ana")
	if _, err := a.db.Exec(`INSERT INTO mcp_maquinas (user_id, nome, hash, criada) SELECT id, 'notebook', x'01', 0 FROM users WHERE name = 'ana';
		INSERT INTO mcp_conversas (user_id, nome, criada) SELECT id, 'busca', 0 FROM users WHERE name = 'ana';
		INSERT INTO mcp_mensagens (conversa_id, autor, rotulo, humano, em, texto) SELECT id, 'pai', 'pai', 0, 0, 'parser pronto' FROM mcp_conversas;
		INSERT INTO mcp_lido (conversa_id, apelido, ate) SELECT id, 'pai', 1 FROM mcp_conversas;
		INSERT INTO invites (token, created_by, expires) SELECT 'CONVITE', id, 0 FROM users WHERE name = 'ana';`); err != nil {
		t.Fatal(err)
	}
	return a, dir, srv, ana
}

// tudoDeAna é quanto da fixture aponta para ana: users 1, docs 3, user_shares 2, sessions 1, members 1, invites 1,
// mcp_maquinas 1, mcp_conversas 1, mcp_mensagens 1, mcp_lido 1.
const tudoDeAna = 13

// sobrouDeAna conta o que ainda aponta para ana no banco (bia não tem grupo, convite nem nada de MCP) e no disco.
func sobrouDeAna(t *testing.T, a *app, dir string) (linhas int, arquivos []string) {
	t.Helper()
	if err := a.db.QueryRow(`SELECT (SELECT count(*) FROM users WHERE name = 'ana') + (SELECT count(*) FROM docs WHERE name LIKE 'ana/%')
		+ (SELECT count(*) FROM user_shares) + (SELECT count(*) FROM sessions s JOIN users u ON u.id = s.user_id WHERE u.name = 'ana')
		+ (SELECT count(*) FROM members) + (SELECT count(*) FROM invites)
		+ (SELECT count(*) FROM mcp_maquinas) + (SELECT count(*) FROM mcp_conversas)
		+ (SELECT count(*) FROM mcp_mensagens) + (SELECT count(*) FROM mcp_lido)`).Scan(&linhas); err != nil {
		t.Fatal(err)
	}
	if es, err := os.ReadDir(filepath.Join(dir, "docs", "ana")); err == nil {
		for _, e := range es {
			arquivos = append(arquivos, e.Name())
		}
	}
	return
}

// ler(t)(c.Get(...)) devolve o status e o corpo; a função devolvida aceita direto os dois retornos do cliente.
func ler(t *testing.T) func(*http.Response, error) (int, string) {
	return func(res *http.Response, err error) (int, string) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
}

func TestApagarContaPeloSite(t *testing.T) {
	a, dir, srv, ana := contaComTudo(t)
	if code, page := ler(t)(ana.Get(srv.URL + "/_conta/apagar")); code != 200 || !strings.Contains(page, "3 documentos") ||
		!strings.Contains(page, "1 máquina ") || !strings.Contains(page, "1 conversa ") {
		t.Fatalf("tela de confirmação: %d sem as contagens", code)
	}
	if code, _ := ler(t)(ana.PostForm(srv.URL+"/_conta/apagar", url.Values{"pass": {"errada-123"}})); code != 401 {
		t.Errorf("senha errada: %d, queria 401", code)
	}
	if n, arqs := sobrouDeAna(t, a, dir); n != tudoDeAna || len(arqs) != 3 { // p.txt, a.txt e a pasta p
		t.Fatalf("senha errada apagou algo: %d linhas, %v", n, arqs)
	}
	if code, page := ler(t)(ana.PostForm(srv.URL+"/_conta/apagar", url.Values{"pass": {"senha-forte"}})); code != 200 || !strings.Contains(page, "foram apagados") {
		t.Fatalf("apagar: %d", code)
	}
	if n, arqs := sobrouDeAna(t, a, dir); n != 0 || len(arqs) != 0 {
		t.Errorf("sobrou de ana: %d linhas, %v", n, arqs)
	}
	var grupos int
	a.db.QueryRow(`SELECT count(*) FROM groups`).Scan(&grupos)
	if grupos != 1 {
		t.Error("o grupo time é de todos e não devia sair")
	}
	if b, err := os.ReadFile(filepath.Join(dir, "docs", "bia", "x.txt")); err != nil || string(b) != "texto de bia" {
		t.Errorf("o documento de bia sumiu ou mudou: %q, %v", b, err)
	}
	// o cookie foi limpo e a sessão não existe mais: a raiz volta a ser a apresentação
	if _, page := ler(t)(ana.Get(srv.URL + "/")); !strings.Contains(page, `class="landing"`) {
		t.Error("depois de apagar, a sessão ainda vale")
	}
}

// a tela de apagar usa os limites do login: quem pegou uma sessão aberta não adivinha a senha por ela
func TestApagarContaLimiteDeSenha(t *testing.T) {
	a, dir, srv, ana := contaComTudo(t)
	for i := range 10 {
		if code, _ := ler(t)(ana.PostForm(srv.URL+"/_conta/apagar", url.Values{"pass": {"errada-123"}})); code != 401 {
			t.Fatalf("erro %d: %d, queria 401", i+1, code)
		}
	}
	if code, _ := ler(t)(ana.PostForm(srv.URL+"/_conta/apagar", url.Values{"pass": {"senha-forte"}})); code != 429 {
		t.Errorf("depois de 10 erros: %d, queria 429 mesmo com a senha certa", code)
	}
	if n, _ := sobrouDeAna(t, a, dir); n != tudoDeAna {
		t.Errorf("bloqueado e ainda assim apagou: %d linhas", n)
	}
}

// com a aba aberta e uma edição ainda na memória, a conta sai e o arquivo não volta com o flush
func TestApagarContaComAbaAberta(t *testing.T) {
	old := flushDelay
	flushDelay = 20 * time.Millisecond
	t.Cleanup(func() { flushDelay = old })
	a, srv, login := wsApp(t)
	ana := login("ana")
	post(t, ana, srv.URL+"/ana/doc", "text/plain", "velho")
	tc := dialWS(t, srv, ana, "ana/doc", "a1", "")
	if err := tc.edit(Op{{Retain: 5}, {Insert: "!"}}, 6); err != nil {
		t.Fatal(err)
	}
	if _, err := tc.until(isType("ack")); err != nil {
		t.Fatal(err)
	}
	if code, _ := ler(t)(ana.PostForm(srv.URL+"/_conta/apagar", url.Values{"pass": {"senha-forte"}})); code != 200 {
		t.Fatalf("apagar: %d", code)
	}
	if ce := closedWith(t, tc.c); ce.Code != 4000 || ce.Reason != "documento apagado" {
		t.Fatalf("a aba fechou com %+v", ce)
	}
	time.Sleep(5 * flushDelay)
	if _, err := os.Stat(filepath.Join(a.docsDir, "ana")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a pasta de ana voltou: %v", err)
	}
}

func TestApagarTudoCLI(t *testing.T) {
	a, dir, _, _ := contaComTudo(t)
	// o servidor segura o pad.lock: com ele preso, a exclusão por fora recusa
	trava, err := travaDados(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := apagarTudoCLI(a, dir, "ana", strings.NewReader("ana\n")); err == nil || !strings.Contains(err.Error(), "no ar") {
		t.Errorf("com o pad no ar: %v", err)
	}
	trava.Close()
	if err := apagarTudoCLI(a, dir, "ana", strings.NewReader("bia\n")); err == nil {
		t.Error("confirmação com outro nome passou")
	}
	if err := apagarTudoCLI(a, dir, "zeca", strings.NewReader("zeca\n")); err == nil {
		t.Error("usuário inexistente passou")
	}
	if n, _ := sobrouDeAna(t, a, dir); n != tudoDeAna {
		t.Fatalf("algo saiu antes da confirmação: %d linhas", n)
	}
	if err := apagarTudoCLI(a, dir, "ana", strings.NewReader("ana\n")); err != nil {
		t.Fatal(err)
	}
	if n, arqs := sobrouDeAna(t, a, dir); n != 0 || len(arqs) != 0 {
		t.Errorf("sobrou de ana: %d linhas, %v", n, arqs)
	}
	// a trava do CLI foi solta: o servidor consegue subir depois
	if trava, err := travaDados(dir, false); err != nil {
		t.Errorf("a trava ficou presa depois do CLI: %v", err)
	} else {
		trava.Close()
	}
}

// uma conta com nome fora da regra (de antes do validUserName) não leva a área pública no RemoveAll
func TestApagarTudoNomeForaDaRegra(t *testing.T) {
	a, err := openApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.apagarTudo(1, "publico"); err == nil {
		t.Error("apagou a conta publico")
	}
}

func baixarZip(t *testing.T, c *http.Client, srv *httptest.Server) map[string]string {
	t.Helper()
	res, err := c.Get(srv.URL + "/_conta/exportar")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "application/zip" ||
		!strings.Contains(res.Header.Get("Content-Disposition"), `filename="pad-`) {
		t.Fatalf("exportar: %d %q %q", res.StatusCode, res.Header.Get("Content-Type"), res.Header.Get("Content-Disposition"))
	}
	z, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	arq := map[string]string{}
	for _, f := range z.File {
		r, _ := f.Open()
		c, _ := io.ReadAll(r)
		r.Close()
		arq[f.Name] = string(c)
	}
	return arq
}

func TestExportarDados(t *testing.T) {
	_, _, srv, ana := contaComTudo(t)
	arq := baixarZip(t, ana, srv)
	for _, d := range []string{"documentos/a.txt", "documentos/p.txt", "documentos/p/b.txt"} {
		if arq[d] != "texto de ana" {
			t.Errorf("%s = %q", d, arq[d])
		}
	}
	if len(arq) != 5 {
		t.Errorf("%d arquivos no zip, quero 3 documentos e 2 json: %v", len(arq), arq)
	}
	// o documento de bia entra só pelo nome; o texto é dela
	for _, s := range []string{`"Usuario": "ana"`, `"time"`, `"usuário bia"`, `"bia/x"`, `"notebook"`} {
		if !strings.Contains(arq["conta.json"], s) {
			t.Errorf("conta.json sem %s:\n%s", s, arq["conta.json"])
		}
	}
	tudo := ""
	for _, c := range arq {
		tudo += c
	}
	if strings.Contains(tudo, "texto de bia") || strings.Contains(arq["conta.json"], "Hash") {
		t.Error("o zip leva o texto de bia ou o hash da chave")
	}
	for _, s := range []string{`"busca"`, `"parser pronto"`, `"Apelido": "pai"`, `"AteMensagem": 1`} {
		if !strings.Contains(arq["conversas.json"], s) {
			t.Errorf("conversas.json sem %s:\n%s", s, arq["conversas.json"])
		}
	}
	// sem sessão, vai para o login
	if res, err := http.Get(srv.URL + "/_conta/exportar"); err != nil || res.Request.URL.Path != "/_login" {
		t.Errorf("exportar sem login não levou ao login: %v", err)
	}
}

// a edição que ainda está só na memória (flush adiado) entra no zip
func TestExportarLevaTextoAoVivo(t *testing.T) {
	old := flushDelay
	flushDelay = time.Hour
	t.Cleanup(func() { flushDelay = old })
	_, srv, login := wsApp(t)
	ana := login("ana")
	post(t, ana, srv.URL+"/ana/doc", "text/plain", "velho")
	tc := dialWS(t, srv, ana, "ana/doc", "a1", "")
	if err := tc.edit(Op{{Retain: 5}, {Insert: "!"}}, 6); err != nil {
		t.Fatal(err)
	}
	if _, err := tc.until(isType("ack")); err != nil {
		t.Fatal(err)
	}
	if got := baixarZip(t, ana, srv)["documentos/doc.txt"]; got != "velho!" {
		t.Errorf("zip levou %q, queria o texto da tela", got)
	}
}
