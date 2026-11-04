package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
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
	check(status(login("ana", "errada"), "GET", "/_convite", ""), 401, "senha errada")
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
	// a raiz responde 200 para todos: só a lista de documentos prova que há sessão
	temSessao := func(c *http.Client) bool { return strings.Contains(pagina(t, c, srv.URL+"/"), `id="coluna"`) }
	if !temSessao(bia) {
		t.Error("bia com sessão quase vencida não entrou")
	}
	var exp int64
	if err := a.db.QueryRow(`SELECT MAX(s.expires) FROM sessions s JOIN users u ON u.id = s.user_id WHERE u.name = 'bia'`).Scan(&exp); err != nil {
		t.Fatal(err)
	}
	if time.Until(time.Unix(exp, 0)) < 29*24*time.Hour {
		t.Errorf("sessão da bia não renovou: vence em %v", time.Until(time.Unix(exp, 0)))
	}
	check(status(ana, "POST", "/_logout", ""), 401, "logout leva ao login")
	check(status(ana, "GET", "/_convite", ""), 401, "depois do logout")
	if temSessao(ana) {
		t.Error("ana continua com sessão depois do logout")
	}

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
	if n, _ := countEntries(filepath.Join(dir, "docs", "publico")); n != 6 { // recado.txt + pastas 1..4 + 4/5.txt
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
	if n, _ := countEntries(filepath.Join(dir, "docs", "publico")); n != 2 { // recado.txt + novo.txt
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
	if !temSessao(davi) {
		t.Error("davi não entrou logado depois do cadastro")
	}
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

// os totais guardados batem com a pasta depois de criar, sobrescrever e apagar; e o teto de bytes abre espaço.
func TestPublicoTotais(t *testing.T) {
	a, err := openApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(a.docsDir, publico)
	confere := func(quando string) {
		t.Helper()
		if n, b := countEntries(root); n != a.pubItens || b != a.pubBytes {
			t.Errorf("%s: pasta %d itens %d bytes, totais %d e %d", quando, n, b, a.pubItens, a.pubBytes)
		}
	}
	grava := func(name, body string, at time.Time) {
		t.Helper()
		if err := a.gravarPublico(name, []byte(body)); err != nil {
			t.Fatal(name, err)
		}
		os.Chtimes(a.docPath(name), at, at)
	}
	agora := time.Now()
	grava("publico/a/b/c", strings.Repeat("x", 100), agora.Add(-3*time.Hour))
	confere("criou com pastas")
	grava("publico/a/b/c", strings.Repeat("x", 40), agora.Add(-3*time.Hour))
	confere("sobrescreveu")
	grava("publico/d", strings.Repeat("x", 50), agora.Add(-2*time.Hour))
	grava("publico/e", strings.Repeat("x", 50), agora.Add(-time.Hour))
	confere("criou mais")

	old := maxPublicoBytes
	maxPublicoBytes = 150
	t.Cleanup(func() { maxPublicoBytes = old })
	grava("publico/f", strings.Repeat("x", 50), agora) // 190 bytes: sai o mais antigo, a/b/c, e as pastas dele
	confere("teto de bytes")
	if _, err := os.Stat(filepath.Join(root, "a")); !os.IsNotExist(err) {
		t.Error("o mais antigo e as pastas vazias deviam ter saído")
	}
	if _, err := os.Stat(filepath.Join(root, "f.txt")); err != nil {
		t.Error(err)
	}
	maxPublicoBytes = 10
	if err := a.gravarPublico("publico/g", []byte(strings.Repeat("x", 11))); err == nil {
		t.Error("documento maior que o teto foi gravado")
	}
	confere("recusou")
}

// o login para de testar a senha depois de erros demais, por IP e por conta, mesmo com a senha certa.
func TestLoginLimite(t *testing.T) {
	a, srv, _ := wsApp(t)
	tenta := func(name, pass, ip string) int {
		t.Helper()
		req, _ := http.NewRequest("POST", srv.URL+"/_login", strings.NewReader(url.Values{"name": {name}, "pass": {pass}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-Real-Ip", ip)
		c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		res, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	for range a.limLoginIP.max {
		if st := tenta("ana", "errada-123", "10.3.0.1"); st != 401 {
			t.Fatalf("senha errada: %d", st)
		}
	}
	if st := tenta("bia", "senha-forte", "10.3.0.1"); st != 429 {
		t.Fatalf("IP no limite: %d, queria 429", st)
	}
	if st := tenta("bia", "senha-forte", "10.3.0.2"); st != 303 {
		t.Fatalf("outro IP com a senha certa: %d", st)
	}
	// de muitos IPs, a conta enche e barra quem já errou nela
	for i := range a.limLoginNome.max - a.limLoginIP.max {
		if st := tenta("ana", "errada-123", fmt.Sprintf("10.4.0.%d", i)); st != 401 {
			t.Fatalf("tentativa %d: %d", i, st)
		}
	}
	// o dono, em outro IP, continua entrando: errar a senha dos outros não deixa ninguém de fora
	if st := tenta("ana", "senha-forte", "10.5.0.1"); st != 303 {
		t.Fatalf("dono com a conta cheia: %d, queria 303", st)
	}
	// com a conta cheia, um IP só tem as poucas tentativas do limite por par
	for range a.limLoginPar.max {
		if st := tenta("ana", "errada-123", "10.5.0.2"); st != 401 {
			t.Fatalf("IP novo errando: %d", st)
		}
	}
	if st := tenta("ana", "senha-forte", "10.5.0.2"); st != 429 {
		t.Fatalf("IP que errou com a conta cheia: %d, queria 429", st)
	}
	// nome que não pode existir não vira chave
	tenta(strings.Repeat("x", 500), "errada-123", "10.6.0.1")
	if len(a.limLoginNome.cont) != 1 {
		t.Fatalf("chaves de nome: %d", len(a.limLoginNome.cont))
	}
}

// erros em paralelo não passam muito do limite: a conferência se repete depois da fila do bcrypt.
func TestLoginConcorrenteRespeitaLimite(t *testing.T) {
	a, srv, _ := wsApp(t)
	const pedidos = 60
	var erradas atomic.Int64
	var wg sync.WaitGroup
	for range pedidos {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, _ := http.NewRequest("POST", srv.URL+"/_login", strings.NewReader("name=ana&pass=errada-123"))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("X-Real-Ip", "10.7.0.1")
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Error(err)
				return
			}
			res.Body.Close()
			if res.StatusCode == 401 {
				erradas.Add(1)
			}
		}()
	}
	wg.Wait()
	if got, teto := erradas.Load(), int64(a.limLoginIP.max+cap(a.vagasBcrypt)); got > teto {
		t.Fatalf("%d senhas testadas, teto %d", got, teto)
	}
}

// o bloqueio por IP não perde para onde a pessoa ia depois do login.
func TestLoginBloqueadoGuardaNext(t *testing.T) {
	a, srv, _ := wsApp(t)
	a.limLoginIP = novoLimite(0, time.Minute)
	res, err := http.PostForm(srv.URL+"/_login", url.Values{"name": {"ana"}, "pass": {"x"}, "next": {"/ana/notas"}})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != 429 || !strings.Contains(string(b), `value="/ana/notas"`) {
		t.Fatalf("status %d, página sem o next", res.StatusCode)
	}
}

// HSTS só em HTTPS (atrás do proxy, pelo X-Forwarded-Proto), e a CSP em todas as respostas.
func TestCabecalhosDeSeguranca(t *testing.T) {
	_, srv, _ := wsApp(t)
	pega := func(proto string) http.Header {
		req, _ := http.NewRequest("GET", srv.URL+"/_login", nil)
		if proto != "" {
			req.Header.Set("X-Forwarded-Proto", proto)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.Header
	}
	if h := pega("https"); !strings.Contains(h.Get("Strict-Transport-Security"), "max-age=") {
		t.Errorf("sem HSTS em HTTPS: %v", h)
	}
	if h := pega(""); h.Get("Strict-Transport-Security") != "" {
		t.Error("HSTS em HTTP")
	}
	for _, d := range []string{"frame-ancestors 'none'", "object-src 'none'", "base-uri 'none'", "form-action 'self'"} {
		if !strings.Contains(pega("").Get("Content-Security-Policy"), d) {
			t.Errorf("CSP sem %s", d)
		}
	}
}

// a sessão que cai enquanto o corpo chega não grava: ela é conferida de novo depois da leitura.
func TestSaveSessaoCaiDuranteOCorpo(t *testing.T) {
	a, srv, login := wsApp(t)
	ana := login("ana")
	pr, pw := io.Pipe()
	req, _ := http.NewRequest("POST", srv.URL+"/ana/lento", pr)
	fim := make(chan int)
	go func() {
		res, err := ana.Do(req)
		if err != nil {
			t.Error(err)
			fim <- 0
			return
		}
		res.Body.Close()
		fim <- res.StatusCode
	}()
	pw.Write([]byte("começo"))
	time.Sleep(200 * time.Millisecond) // o handler já passou da primeira conferência e espera o corpo
	if _, err := a.db.Exec(`DELETE FROM sessions`); err != nil {
		t.Fatal(err)
	}
	pw.Close()
	if st := <-fim; st != 401 {
		t.Fatalf("status %d, queria 401", st)
	}
	if _, err := os.Stat(a.docPath("ana/lento")); !os.IsNotExist(err) {
		t.Fatal("gravou com a sessão já revogada")
	}
}

// corpo que para no meio não prende a conexão: o prazo vence, e o servidor responde e fecha.
func TestLoginCorpoParado(t *testing.T) {
	old := prazoLogin
	prazoLogin = 200 * time.Millisecond
	t.Cleanup(func() { prazoLogin = old })
	_, srv, _ := wsApp(t)
	c, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	fmt.Fprintf(c, "POST /_login HTTP/1.1\r\nHost: x\r\nContent-Type: application/x-www-form-urlencoded\r\nContent-Length: 1000\r\n\r\nname=ana")
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadAll(c); err != nil {
		t.Fatalf("a conexão ficou presa: %v", err)
	}
}

func TestLoginCorpoGrande(t *testing.T) {
	_, srv, _ := wsApp(t)
	res, err := http.Post(srv.URL+"/_login", "application/x-www-form-urlencoded", strings.NewReader("name=ana&pass="+strings.Repeat("x", 1<<20)))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatalf("status %d", res.StatusCode)
	}
}

func TestPublicoLimitePorIP(t *testing.T) {
	a, srv, _ := wsApp(t)
	a.limPublico = novoLimite(2, time.Minute)
	for i, want := range []int{204, 204, 429} {
		if st, _ := postStatus(t, &http.Client{}, srv.URL+"/publico/ritmo", "x"); st != want {
			t.Fatalf("POST %d: %d, queria %d", i+1, st, want)
		}
	}
}

// trocar a senha corta também as chaves de máquina.
func TestPasswdRevogaMaquinas(t *testing.T) {
	a, _, _ := wsApp(t)
	var uid int64
	if err := a.db.QueryRow(`SELECT id FROM users WHERE name = 'ana'`).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	chave, err := a.criarMaquina(uid, "notebook")
	if err != nil {
		t.Fatal(err)
	}
	var biaID int64
	if err := a.db.QueryRow(`SELECT id FROM users WHERE name = 'bia'`).Scan(&biaID); err != nil {
		t.Fatal(err)
	}
	chaveBia, err := a.criarMaquina(biaID, "notebook")
	if err != nil {
		t.Fatal(err)
	}
	if err := runCLI(a.db, []string{"passwd", "ana"}, strings.NewReader("outra-senha\n")); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, err := a.maquinaDaChave(chave); ok || err != nil {
		t.Fatalf("chave da ana continua valendo: %v, %v", ok, err)
	}
	if _, _, ok, err := a.maquinaDaChave(chaveBia); !ok || err != nil {
		t.Fatalf("chave da bia caiu: %v, %v", ok, err)
	}
}

// /publico não tem dono: só admin apaga, a aba aberta cai e os totais acompanham.
func TestAdminApagaPublico(t *testing.T) {
	a, srv, login := wsApp(t)
	if err := runCLI(a.db, []string{"admin", "ana"}, nil); err != nil {
		t.Fatal(err)
	}
	ana, bia, anon := login("ana"), login("bia"), &http.Client{}
	post(t, anon, srv.URL+"/publico/pasta/ruim", "text/plain", "conteúdo ruim")
	if !strings.Contains(pagina(t, ana, srv.URL+"/publico/pasta/ruim"), `action="/_delete/publico/pasta/ruim"`) {
		t.Error("admin não vê o botão de apagar")
	}
	if strings.Contains(pagina(t, bia, srv.URL+"/publico/pasta/ruim"), "/_delete/") {
		t.Error("quem não é admin vê o botão de apagar")
	}
	for _, c := range []*http.Client{bia, anon} {
		if st, _ := postStatus(t, c, srv.URL+"/_delete/publico/pasta/ruim", ""); st != 403 && st != 401 {
			t.Fatalf("apagou sem ser admin: %d", st)
		}
	}
	aba := dialWS(t, srv, anon, "publico/pasta/ruim", "c1", "")
	if st, _ := postStatus(t, ana, srv.URL+"/_delete/publico/pasta/ruim", ""); st != 200 {
		t.Fatalf("admin apaga: %d", st)
	}
	if ce := closedWith(t, aba.c); ce.Code != 4000 {
		t.Fatalf("aba fechou com %v", ce)
	}
	if _, err := os.Stat(filepath.Join(a.docsDir, publico, "pasta")); !os.IsNotExist(err) {
		t.Error("arquivo e pasta deviam ter saído")
	}
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	if n, b := countEntries(filepath.Join(a.docsDir, publico)); n != a.pubItens || b != a.pubBytes {
		t.Errorf("pasta %d e %d, totais %d e %d", n, b, a.pubItens, a.pubBytes)
	}
}
