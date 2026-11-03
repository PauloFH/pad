package main

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestAcessoECompartilhamento(t *testing.T) {
	dir := t.TempDir()
	a, err := openApp(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range [][]string{
		{"useradd", "ana"}, {"useradd", "bia"}, {"useradd", "caio"},
		{"groupadd", "time"}, {"join", "time", "caio"},
	} {
		if err := runCLI(a.db, c, strings.NewReader("senha-forte\n")); err != nil {
			t.Fatal(c, err)
		}
	}
	srv := httptest.NewServer(a.routes())
	defer srv.Close()

	login := func(name, pass string) *http.Client {
		jar, _ := cookiejar.New(nil)
		c := &http.Client{Jar: jar}
		if _, err := c.PostForm(srv.URL+"/_login", url.Values{"name": {name}, "pass": {pass}}); err != nil {
			t.Fatal(err)
		}
		return c
	}
	status := func(c *http.Client, method, path, body string) int {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		if method == "POST" && strings.HasPrefix(path, "/_") {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		res, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.Request.URL.Path == "/_login" {
			return 401 // foi mandado para o login
		}
		return res.StatusCode
	}
	ana, bia, caio := login("ana", "senha-forte"), login("bia", "senha-forte"), login("caio", "senha-forte")

	check := func(got, want int, what string) {
		t.Helper()
		if got != want {
			t.Errorf("%s: status %d, queria %d", what, got, want)
		}
	}
	check(status(login("ana", "errada"), "GET", "/", ""), 401, "senha errada")
	check(status(ana, "POST", "/ana/hoje", "oi <b>"), 204, "ana cria")
	if b, _ := os.ReadFile(filepath.Join(dir, "docs", "ana", "hoje.txt")); string(b) != "oi <b>" {
		t.Errorf("arquivo = %q", b)
	}
	// cada um só cria dentro do próprio usuário; mesmo nome em usuários diferentes não colide
	check(status(bia, "POST", "/ana/novo", "x"), 403, "bia cria no espaço da ana")
	check(status(bia, "GET", "/ana/novo", ""), 404, "bia abre doc inexistente da ana")
	check(status(ana, "POST", "/solto", "x"), 403, "doc fora de qualquer usuário")
	check(status(bia, "POST", "/bia/hoje", "da bia"), 204, "bia cria o seu hoje")
	check(status(ana, "GET", "/bia/hoje", ""), 404, "ana não vê o hoje da bia")
	// subpastas: /ana/pessoal e /ana/pessoal/cpf são documentos independentes
	check(status(ana, "POST", "/ana/pessoal", "pai"), 204, "ana cria pessoal")
	check(status(ana, "POST", "/ana/pessoal/cpf", "filho"), 204, "ana cria pessoal/cpf")
	check(status(ana, "POST", "/_share/ana/pessoal", "tipo=user&alvo=bia"), 200, "ana compartilha só o pai")
	check(status(bia, "GET", "/ana/pessoal", ""), 200, "bia lê o pai")
	check(status(bia, "GET", "/ana/pessoal/cpf", ""), 404, "compartilhar o pai não abre o filho")
	if b, _ := os.ReadFile(filepath.Join(dir, "docs", "ana", "pessoal", "cpf.txt")); string(b) != "filho" {
		t.Errorf("pessoal/cpf.txt = %q", b)
	}
	check(status(bia, "GET", "/ana/hoje", ""), 404, "bia sem acesso lê")
	check(status(bia, "POST", "/ana/hoje", "x"), 403, "bia sem acesso grava")
	check(status(bia, "POST", "/_share/ana/hoje", "tipo=user&alvo=bia"), 403, "bia compartilha doc alheio")
	check(status(ana, "POST", "/_share/ana/hoje", "tipo=user&alvo=bia"), 200, "ana compartilha com bia")
	check(status(bia, "POST", "/ana/hoje", "editado"), 204, "bia grava")
	check(status(caio, "GET", "/ana/hoje", ""), 404, "caio antes do grupo")
	check(status(ana, "POST", "/_share/ana/hoje", "tipo=group&alvo=time"), 200, "ana compartilha com time")
	check(status(caio, "GET", "/ana/hoje", ""), 200, "caio pelo grupo")
	check(status(ana, "POST", "/_unshare/ana/hoje", "tipo=group&alvo=time"), 200, "ana remove time")
	check(status(caio, "GET", "/ana/hoje", ""), 404, "caio depois de remover")
	// "/../" puro o ServeMux já limpa; codificado chega ao handler e precisa ser barrado
	check(status(ana, "GET", "/..%2F..%2Fetc%2Fpasswd", ""), 404, "path traversal")
	check(status(ana, "POST", "/a%2F..%2F..%2Fx", "x"), 404, "path traversal na gravação")
	// renovação deslizante: sessão perto de vencer volta para 30 dias quando é usada
	if _, err := a.db.Exec(`UPDATE sessions SET expires = ?`, time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	check(status(bia, "GET", "/", ""), 200, "bia com sessão quase vencida")
	var exp int64
	if err := a.db.QueryRow(`SELECT MAX(s.expires) FROM sessions s JOIN users u ON u.id = s.user_id WHERE u.name = 'bia'`).Scan(&exp); err != nil {
		t.Fatal(err)
	}
	if time.Until(time.Unix(exp, 0)) < 29*24*time.Hour {
		t.Errorf("sessão da bia não renovou: vence em %v", time.Until(time.Unix(exp, 0)))
	}
	check(status(ana, "POST", "/_logout", ""), 401, "logout leva ao login")
	check(status(ana, "GET", "/", ""), 401, "depois do logout")

	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for next, want := range map[string]string{"/ana/hoje": "/ana/hoje", "/\t/evil.com": "/", "//evil.com": "/", "/\\evil.com": "/", "https://evil.com": "/"} {
		res, err := noFollow.PostForm(srv.URL+"/_login", url.Values{"name": {"bia"}, "pass": {"senha-forte"}, "next": {next}})
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if got := res.Header.Get("Location"); got != want {
			t.Errorf("next=%q redirecionou para %q, queria %q", next, got, want)
		}
	}

	// público: qualquer um lê sem login, ninguém de fora edita
	ana = login("ana", "senha-forte")
	anon := &http.Client{}
	check(status(anon, "GET", "/ana/hoje", ""), 401, "anônimo em doc privado")
	check(status(anon, "GET", "/nao/existe", ""), 401, "anônimo em doc inexistente")
	check(status(caio, "POST", "/_public/ana/hoje", "public=1"), 403, "não dono publica")
	check(status(ana, "POST", "/_public/ana/hoje", "public=1"), 200, "ana publica")
	res, err := anon.Get(srv.URL + "/ana/hoje")
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(pub), "editado") || !strings.Contains(string(pub), "readonly") {
		t.Errorf("anônimo em doc público: status %d, página %q", res.StatusCode, pub)
	}
	check(status(anon, "POST", "/ana/hoje", "pichado"), 401, "anônimo grava em doc público")
	check(status(caio, "GET", "/ana/hoje", ""), 200, "caio lê doc público")
	check(status(caio, "POST", "/ana/hoje", "pichado"), 403, "caio grava em doc público")
	check(status(ana, "POST", "/_public/ana/hoje", "public=0"), 200, "ana despublica")
	check(status(anon, "GET", "/ana/hoje", ""), 401, "anônimo depois de despublicar")

	// área /publico: sem login, qualquer um lê e grava; não vira nome de usuário
	check(status(anon, "GET", "/publico/recado", ""), 200, "anônimo abre /publico novo")
	check(status(anon, "POST", "/publico/recado", "oi de fora"), 204, "anônimo grava em /publico")
	check(status(caio, "POST", "/publico/recado", "oi do caio"), 204, "logado grava em /publico")
	check(status(anon, "POST", "/publico", "raiz"), 204, "anônimo grava na raiz /publico")
	check(status(anon, "POST", "/publico/grande", strings.Repeat("x", maxPublicoDoc+1)), 413, "documento público grande demais")
	check(status(anon, "POST", "/publico/1/2/3/4/5", "fundo"), 204, "5 níveis de pasta")
	check(status(anon, "POST", "/publico/1/2/3/4/5/6", "fundo"), 404, "6 níveis de pasta")
	check(status(ana, "POST", "/ana/1/2/3/4/5/6", "fundo"), 404, "6 níveis no espaço do usuário")
	if n := countEntries(filepath.Join(dir, "docs", "publico")); n != 6 { // recado.txt + pastas 1..4 + 4/5.txt
		t.Errorf("itens em /publico = %d, queria 6", n)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "docs", "publico", "recado.txt")); string(b) != "oi do caio" {
		t.Errorf("publico/recado.txt = %q", b)
	}
	// área cheia: o documento novo entra e o editado há mais tempo sai, levando as pastas vazias
	old := time.Now().Add(-time.Hour)
	for _, p := range []string{"publico/1/2/3/4/5.txt", "publico/1/2/3/4", "publico/1/2/3", "publico/1/2", "publico/1"} {
		os.Chtimes(filepath.Join(dir, "docs", p), old, old)
	}
	maxPublicoItems = 6
	check(status(anon, "POST", "/publico/novo", "x"), 204, "doc novo com área cheia")
	maxPublicoItems = 50000
	if _, err := os.Stat(filepath.Join(dir, "docs", "publico", "1")); !os.IsNotExist(err) {
		t.Error("o mais antigo (1/2/3/4/5) e suas pastas vazias deviam ter sido apagados")
	}
	if n := countEntries(filepath.Join(dir, "docs", "publico")); n != 2 { // recado.txt + novo.txt
		t.Errorf("itens em /publico depois da limpeza = %d, queria 2", n)
	}
	check(status(anon, "POST", "/_delete/publico/recado", ""), 401, "anônimo apaga em /publico")
	check(status(ana, "POST", "/_delete/publico/recado", ""), 403, "ninguém é dono de /publico")
	if err := runCLI(a.db, []string{"useradd", "publico"}, strings.NewReader("senha-forte\n")); err == nil {
		t.Error("criou usuário publico")
	}

	// convites: só admin gera; o link cria uma conta, uma vez só
	check(status(ana, "POST", "/_convite", ""), 403, "não admin convida")
	if err := runCLI(a.db, []string{"admin", "ana"}, nil); err != nil {
		t.Fatal(err)
	}
	res, err = ana.Post(srv.URL+"/_convite", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(res.Body)
	res.Body.Close()
	join := regexp.MustCompile(`/_join/[A-Z0-9]+`).FindString(string(page))
	if join == "" {
		t.Fatalf("link de convite não apareceu: %s", page)
	}
	form := func(c *http.Client, v url.Values) (int, string) {
		res, err := c.PostForm(srv.URL+join, v)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode, res.Request.URL.Path
	}
	jar, _ := cookiejar.New(nil)
	davi := &http.Client{Jar: jar}
	if code, _ := form(davi, url.Values{"name": {"publico"}, "pass": {"senha-forte"}, "pass2": {"senha-forte"}}); code != 400 {
		t.Errorf("convite com nome publico: status %d", code)
	}
	if code, _ := form(davi, url.Values{"name": {"bia"}, "pass": {"senha-forte"}, "pass2": {"senha-forte"}}); code != 400 {
		t.Errorf("nome repetido: status %d", code)
	}
	if code, _ := form(davi, url.Values{"name": {"davi"}, "pass": {"curta"}, "pass2": {"curta"}}); code != 400 {
		t.Errorf("senha curta: status %d", code)
	}
	if code, path := form(davi, url.Values{"name": {" Davi "}, "pass": {"senha-forte"}, "pass2": {"senha-forte"}}); code != 200 || path != "/" {
		t.Errorf("cadastro: status %d em %s", code, path)
	}
	check(status(davi, "GET", "/", ""), 200, "davi entra logado")
	check(status(davi, "GET", join, ""), 404, "convite reusado")

	if err := runCLI(a.db, []string{"userdel", "ana"}, nil); err == nil {
		t.Error("apagou usuária que ainda é dona de documento")
	}
}

func TestStatic(t *testing.T) {
	a, err := openApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.routes())
	defer srv.Close()
	res, err := http.Get(srv.URL + "/_static/ot.js")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(res.Header.Get("Content-Type"), "javascript") {
		t.Errorf("status %d, tipo %q", res.StatusCode, res.Header.Get("Content-Type"))
	}
	if res.Header.Get("Cache-Control") != "no-cache" {
		t.Errorf("Cache-Control = %q", res.Header.Get("Cache-Control"))
	}
	for _, dir := range []string{"/_static/", "/_static/fonts/"} {
		if res, err = http.Get(srv.URL + dir); err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 404 {
			t.Errorf("listagem de %s: status %d", dir, res.StatusCode)
		}
	}
	for path, tipo := range map[string]string{
		"/_static/pad.css": "text/css",
		"/_static/fonts/jetbrains-mono-latin-400-normal.woff2": "font/woff2",
	} {
		if res, err = http.Get(srv.URL + path); err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), tipo) {
			t.Errorf("%s: status %d, tipo %q", path, res.StatusCode, res.Header.Get("Content-Type"))
		}
	}
}

func TestPaginaCarregaScripts(t *testing.T) {
	a, err := openApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := runCLI(a.db, []string{"useradd", "ana"}, strings.NewReader("senha-forte\n")); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	ana := &http.Client{Jar: jar}
	if _, err := ana.PostForm(srv.URL+"/_login", url.Values{"name": {"ana"}, "pass": {"senha-forte"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := ana.Post(srv.URL+"/ana/doc", "text/plain", strings.NewReader("oi")); err != nil {
		t.Fatal(err)
	}
	if _, err := ana.PostForm(srv.URL+"/_public/ana/doc", url.Values{"public": {"1"}}); err != nil {
		t.Fatal(err)
	}
	get := func(c *http.Client) string {
		res, err := c.Get(srv.URL + "/ana/doc")
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return string(b)
	}
	page := get(ana)
	for _, s := range []string{"/_static/ot.js", "/_static/sync.js", "/_static/editor.js", "/_static/presence.js", `id="peers"`, `id="mir"`, `data-name="ana/doc"`, `data-can-edit="true"`} {
		if !strings.Contains(page, s) {
			t.Errorf("editor sem %q", s)
		}
	}
	if strings.Contains(page, "fetch(location.pathname") {
		t.Error("editor ainda grava por POST")
	}
	// público aberto sem login: o mesmo editor, só leitura
	page = get(http.DefaultClient)
	for _, s := range []string{"/_static/editor.js", "/_static/presence.js", `id="peers"`, `id="mir"`, `data-name="ana/doc"`, `data-can-edit="false"`} {
		if !strings.Contains(page, s) {
			t.Errorf("página pública sem %q", s)
		}
	}
}

// login, criar conta, mensagem e convite usam o mesmo cartão centralizado
func TestTelasSimplesUsamCartao(t *testing.T) {
	a, err := openApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := runCLI(a.db, []string{"useradd", "ana"}, strings.NewReader("senha-forte\n")); err != nil {
		t.Fatal(err)
	}
	if err := runCLI(a.db, []string{"admin", "ana"}, nil); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	ana := &http.Client{Jar: jar}
	if _, err := ana.PostForm(srv.URL+"/_login", url.Values{"name": {"ana"}, "pass": {"senha-forte"}}); err != nil {
		t.Fatal(err)
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
	_, convite := ler(ana.Post(srv.URL+"/_convite", "", nil))
	join := regexp.MustCompile(`/_join/[A-Z0-9]+`).FindString(convite)
	if join == "" {
		t.Fatal("link de convite não apareceu")
	}
	for _, c := range []struct {
		nome, path string
		cli        *http.Client
		status     int
		tem        []string
	}{
		{"login", "/_login", http.DefaultClient, 200, []string{"pad<span", "Blocos de notas compartilhados, ao vivo."}},
		{"criar conta", join, http.DefaultClient, 200, nil},
		{"mensagem", "/zeca/nao-existe", ana, 404, nil},
		{"convite", "/_convite", ana, 200, nil},
	} {
		code, page := ler(c.cli.Get(srv.URL + c.path))
		if code != c.status {
			t.Errorf("%s: status %d, queria %d", c.nome, code, c.status)
		}
		for _, s := range append(c.tem, `class="tela-cartao"`, "/_static/pad.css") {
			if !strings.Contains(page, s) {
				t.Errorf("%s sem %q", c.nome, s)
			}
		}
	}
	// o convite gerado traz o link, o Copiar e o Gerar outro
	for _, s := range []string{"Copiar", "Gerar outro link"} {
		if !strings.Contains(convite, s) {
			t.Errorf("convite gerado sem %q", s)
		}
	}
}

// /_static/ revalida a cada carga (no-cache), mas com ETag do conteúdo embutido: sem mudança volta 304
func TestStaticETag(t *testing.T) {
	a, err := openApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.routes())
	defer srv.Close()
	tags := map[string]bool{}
	for _, path := range []string{"/_static/pad.css", "/_static/ot.js", "/_static/fonts/space-grotesk-latin-400-normal.woff2"} {
		res, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		tag := res.Header.Get("ETag")
		if tag == "" || res.Header.Get("Cache-Control") != "no-cache" {
			t.Fatalf("%s: ETag %q, Cache-Control %q", path, tag, res.Header.Get("Cache-Control"))
		}
		tags[tag] = true
		req, _ := http.NewRequest("GET", srv.URL+path, nil)
		req.Header.Set("If-None-Match", tag)
		if res, err = http.DefaultClient.Do(req); err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusNotModified {
			t.Errorf("%s com If-None-Match: status %d, quero 304", path, res.StatusCode)
		}
	}
	if len(tags) != 3 {
		t.Errorf("arquivos diferentes com o mesmo ETag: %v", tags)
	}
}

// aba de um documento que sumiu ou perdeu o acesso: a página do 404 tira o nome das abas
func TestAbaSemAcessoSai(t *testing.T) {
	_, srv, login := wsApp(t)
	ana, bia := login("ana"), login("bia")
	post(t, bia, srv.URL+"/bia/fora", "text/plain", "x")
	res, err := ana.Get(srv.URL + "/bia/fora")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Fatalf("status %d", res.StatusCode)
	}
	if !regexp.MustCompile(`PadShell\.abas\.fechar\("ana",\s*"bia\\?/fora"\)`).Match(b) {
		t.Errorf("404 não fecha a aba:\n%s", b)
	}
}
