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
