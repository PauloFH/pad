package main

import (
	"io"
	"net/http"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func TestArvore(t *testing.T) {
	var docs []docRow
	for _, n := range []string{"ana/a", "ana/p/x", "ana/p/q/r/s/t", "bia/z"} {
		docs = append(docs, docRow{Name: n})
	}
	doc := func(nome, caminho string) noArvore { return noArvore{Nome: nome, Caminho: caminho} }
	pasta := func(nome, caminho string, filhos ...noArvore) noArvore {
		return noArvore{Nome: nome, Caminho: caminho, Pasta: true, Filhos: filhos}
	}
	want := []noArvore{
		pasta("p", "ana/p",
			pasta("q", "ana/p/q", pasta("r", "ana/p/q/r", pasta("s", "ana/p/q/r/s", doc("t", "ana/p/q/r/s/t")))),
			doc("x", "ana/p/x")),
		doc("a", "ana/a"),
	}
	if got := arvore(docs, "ana"); !reflect.DeepEqual(got, want) {
		t.Errorf("arvore =\n%+v\nquero\n%+v", got, want)
	}
}

// página de um caminho, com o cliente dado
func pagina(t *testing.T, c *http.Client, u string) string {
	t.Helper()
	res, err := c.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 {
		t.Fatalf("%s: status %d", u, res.StatusCode)
	}
	return string(b)
}

func TestRaizVisitanteVeApresentacao(t *testing.T) {
	_, srv, login := wsApp(t)
	page := pagina(t, &http.Client{}, srv.URL+"/")
	for _, s := range []string{"Um bloco de notas em cada endereço", `href="/publico"`, `href="/_login"`, "/_static/landing.js", `rel="icon"`} {
		if !strings.Contains(page, s) {
			t.Errorf("apresentação sem %q", s)
		}
	}
	// com sessão a raiz continua sendo a lista de documentos
	page = pagina(t, login("ana"), srv.URL+"/")
	if !strings.Contains(page, `id="coluna"`) || strings.Contains(page, "landing.js") {
		t.Error("quem tem sessão não recebeu os documentos em /")
	}
}

func TestVisitanteSemColuna(t *testing.T) {
	_, srv, login := wsApp(t)
	ana := login("ana")
	post(t, ana, srv.URL+"/ana/aberto", "text/plain", "oi")
	post(t, ana, srv.URL+"/ana/segredo", "text/plain", "psiu")
	post(t, ana, srv.URL+"/_public/ana/aberto", "application/x-www-form-urlencoded", "public=1")
	for _, u := range []string{"/publico/x", "/ana/aberto"} {
		page := pagina(t, &http.Client{}, srv.URL+u)
		for _, s := range []string{`id="coluna"`, `id="col"`, `id="abas"`, "segredo"} {
			if strings.Contains(page, s) {
				t.Errorf("visitante em %s recebeu %q", u, s)
			}
		}
	}
	// o visitante no documento público vê o selo e o Entrar que volta para ele
	page := pagina(t, &http.Client{}, srv.URL+"/ana/aberto")
	for _, s := range []string{`class="selo"`, "só leitura", `href="/_login?next=/ana%2faberto"`} {
		if !strings.Contains(page, s) {
			t.Errorf("documento público para visitante sem %q", s)
		}
	}
}

func TestEditorComColuna(t *testing.T) {
	_, srv, login := wsApp(t)
	ana, bia := login("ana"), login("bia")
	post(t, ana, srv.URL+"/ana/a", "text/plain", "a")
	post(t, ana, srv.URL+"/ana/p/x", "text/plain", "x")
	post(t, bia, srv.URL+"/bia/z", "text/plain", "z")
	post(t, bia, srv.URL+"/_share/bia/z", "application/x-www-form-urlencoded", "tipo=user&alvo=ana")
	page := pagina(t, ana, srv.URL+"/ana/a")
	i := strings.Index(page, `id="coluna"`)
	if i < 0 {
		t.Fatal(`editor sem id="coluna"`)
	}
	coluna := page[i:]
	for _, s := range []string{`href="/ana/p/x"`, `href="/bia/z"`, `id="col"`, `id="abas"`} {
		if !strings.Contains(page, s) {
			t.Errorf("editor sem %q", s)
		}
	}
	if !strings.Contains(coluna, "ana/p/x") || !strings.Contains(coluna, "bia/z") {
		t.Error("a coluna não lista ana/p/x e o compartilhado bia/z")
	}
	if !regexp.MustCompile(`<a [^>]*href="/ana/a"[^>]*aria-current="page"`).MatchString(coluna) {
		t.Error(`o documento atual não tem aria-current="page"`)
	}
	if n := strings.Count(page, `aria-current="page"`); n != 1 {
		t.Errorf("aria-current aparece %d vezes", n)
	}
	// logado na área pública também tem a coluna
	if page := pagina(t, ana, srv.URL+"/publico/x"); !strings.Contains(page, `id="coluna"`) {
		t.Error("logado no /publico sem a coluna")
	}
}
