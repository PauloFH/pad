package main

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestPainelCompartilhar(t *testing.T) {
	a, err := openApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range [][]string{{"useradd", "ana"}, {"useradd", "bia"}, {"groupadd", "time"}} {
		if err := runCLI(a.db, c, strings.NewReader("senha-forte\n")); err != nil {
			t.Fatal(c, err)
		}
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
	for _, c := range []string{"tipo=user&alvo=bia", "tipo=group&alvo=time"} {
		if _, err := ana.Post(srv.URL+"/_share/ana/doc", "application/x-www-form-urlencoded", strings.NewReader(c)); err != nil {
			t.Fatal(err)
		}
	}
	get := func(path string) string {
		res, err := ana.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return string(b)
	}
	tem := func(page string, ss ...string) {
		t.Helper()
		for _, s := range ss {
			if !strings.Contains(page, s) {
				t.Errorf("falta %q na página", s)
			}
		}
	}

	priv := get("/ana/doc")
	tem(priv, `role="switch"`, `aria-checked="false"`, `name="public" value="1"`,
		`action="/_share/ana/doc"`, `action="/_unshare/ana/doc"`, `action="/_public/ana/doc"`, `action="/_delete/ana/doc"`,
		`name="tipo" value="user"`, `name="tipo" value="group"`, `name="alvo"`, `confirm(`,
		`/_static/compartilhar.js`)
	if strings.Contains(priv, `Usuário ou grupo não encontrado`) || strings.Contains(priv, `data-aberto`) {
		t.Error("painel aberto ou com erro sem falha")
	}

	if _, err := ana.PostForm(srv.URL+"/_public/ana/doc", url.Values{"public": {"1"}}); err != nil {
		t.Fatal(err)
	}
	pub := get("/ana/doc")
	tem(pub, `aria-checked="true"`, `name="public" value="0"`)
	if strings.Contains(pub, `aria-checked="false"`) {
		t.Error("documento público com a chave desligada")
	}

	// alvo inexistente: o painel já nasce aberto com o erro
	tem(get("/ana/doc?erro=1"), `Usuário ou grupo não encontrado.`, `data-aberto`)
}
