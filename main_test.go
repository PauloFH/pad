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
	check(status(ana, "POST", "/notas/hoje", "oi <b>"), 204, "ana cria")
	if b, _ := os.ReadFile(filepath.Join(dir, "docs", "notas", "hoje.txt")); string(b) != "oi <b>" {
		t.Errorf("arquivo = %q", b)
	}
	check(status(bia, "GET", "/notas/hoje", ""), 403, "bia sem acesso lê")
	check(status(bia, "POST", "/notas/hoje", "x"), 403, "bia sem acesso grava")
	check(status(bia, "POST", "/_share/notas/hoje", "tipo=user&alvo=bia"), 403, "bia compartilha doc alheio")
	check(status(ana, "POST", "/_share/notas/hoje", "tipo=user&alvo=bia"), 200, "ana compartilha com bia")
	check(status(bia, "POST", "/notas/hoje", "editado"), 204, "bia grava")
	check(status(caio, "GET", "/notas/hoje", ""), 403, "caio antes do grupo")
	check(status(ana, "POST", "/_share/notas/hoje", "tipo=group&alvo=time"), 200, "ana compartilha com time")
	check(status(caio, "GET", "/notas/hoje", ""), 200, "caio pelo grupo")
	check(status(ana, "POST", "/_unshare/notas/hoje", "tipo=group&alvo=time"), 200, "ana remove time")
	check(status(caio, "GET", "/notas/hoje", ""), 403, "caio depois de remover")
	// "/../" puro o ServeMux já limpa; codificado chega ao handler e precisa ser barrado
	check(status(ana, "GET", "/..%2F..%2Fetc%2Fpasswd", ""), 404, "path traversal")
	check(status(ana, "POST", "/a%2F..%2F..%2Fx", "x"), 404, "path traversal na gravação")
	check(status(ana, "POST", "/_logout", ""), 401, "logout leva ao login")
	check(status(ana, "GET", "/", ""), 401, "depois do logout")

	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for next, want := range map[string]string{"/notas/hoje": "/notas/hoje", "/\t/evil.com": "/", "//evil.com": "/", "/\\evil.com": "/", "https://evil.com": "/"} {
		res, err := noFollow.PostForm(srv.URL+"/_login", url.Values{"name": {"bia"}, "pass": {"senha-forte"}, "next": {next}})
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if got := res.Header.Get("Location"); got != want {
			t.Errorf("next=%q redirecionou para %q, queria %q", next, got, want)
		}
	}

	// convites: só admin gera; o link cria uma conta, uma vez só
	ana = login("ana", "senha-forte")
	check(status(ana, "POST", "/_convite", ""), 403, "não admin convida")
	if err := runCLI(a.db, []string{"admin", "ana"}, nil); err != nil {
		t.Fatal(err)
	}
	res, err := ana.Post(srv.URL+"/_convite", "", nil)
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
	if code, _ := form(davi, url.Values{"name": {"bia"}, "pass": {"senha-forte"}, "pass2": {"senha-forte"}}); code != 400 {
		t.Errorf("nome repetido: status %d", code)
	}
	if code, _ := form(davi, url.Values{"name": {"davi"}, "pass": {"curta"}, "pass2": {"curta"}}); code != 400 {
		t.Errorf("senha curta: status %d", code)
	}
	if code, path := form(davi, url.Values{"name": {"davi"}, "pass": {"senha-forte"}, "pass2": {"senha-forte"}}); code != 200 || path != "/" {
		t.Errorf("cadastro: status %d em %s", code, path)
	}
	check(status(davi, "GET", "/", ""), 200, "davi entra logado")
	check(status(davi, "GET", join, ""), 404, "convite reusado")

	if err := runCLI(a.db, []string{"userdel", "ana"}, nil); err == nil {
		t.Error("apagou usuária que ainda é dona de documento")
	}
}
