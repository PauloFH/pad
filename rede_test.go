package main

import (
	"compress/gzip"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
)

// pedir faz um GET pedindo gzip à mão: assim o cliente do Go não descomprime sozinho e dá para ver os cabeçalhos.
func pedir(t *testing.T, url string, h map[string]string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	for k, v := range h {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var r io.Reader = res.Body
	if res.Header.Get("Content-Encoding") == "gzip" {
		if r, err = gzip.NewReader(res.Body); err != nil {
			t.Fatal(err)
		}
	}
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return res, string(b)
}

func TestCompressaoECacheDosEstaticos(t *testing.T) {
	a, err := openApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.routes())
	defer srv.Close()
	gz := map[string]string{"Accept-Encoding": "gzip, br"}

	res, html := pedir(t, srv.URL+"/", gz)
	if res.Header.Get("Content-Encoding") != "gzip" || !strings.Contains(res.Header.Get("Vary"), "Accept-Encoding") ||
		!strings.Contains(html, "Um bloco de notas em cada endereço") {
		t.Fatalf("landing: Content-Encoding %q, Vary %q, html %.80q", res.Header.Get("Content-Encoding"), res.Header.Get("Vary"), html)
	}
	if res, plano := pedir(t, srv.URL+"/", map[string]string{"Accept-Encoding": "identity"}); res.Header.Get("Content-Encoding") != "" || plano != html {
		t.Errorf("sem gzip no Accept-Encoding: Content-Encoding %q, corpo igual %v", res.Header.Get("Content-Encoding"), plano == html)
	}

	shell := regexp.MustCompile(`/_static/shell\.js\?v=[0-9a-f]{16}`).FindString(html)
	if shell == "" {
		t.Fatal("a landing não referencia o shell.js versionado")
	}
	res, js := pedir(t, srv.URL+shell, gz)
	if res.Header.Get("Cache-Control") != umAno || res.Header.Get("Content-Encoding") != "gzip" || !strings.Contains(js, "PadShell") {
		t.Fatalf("%s: Cache-Control %q, Content-Encoding %q", shell, res.Header.Get("Cache-Control"), res.Header.Get("Content-Encoding"))
	}
	tag := res.Header.Get("ETag")
	if !strings.HasPrefix(tag, `W/"`) {
		t.Errorf("ETag do corpo comprimido deveria ser fraco: %q", tag)
	}
	if res.Header.Get("Accept-Ranges") != "" {
		t.Errorf("Accept-Ranges %q no corpo comprimido: o Range conta bytes do arquivo sem compressão", res.Header.Get("Accept-Ranges"))
	}
	if res, _ := pedir(t, srv.URL+shell, map[string]string{"Accept-Encoding": "gzip", "If-None-Match": tag}); res.StatusCode != http.StatusNotModified ||
		res.Header.Get("Vary") != "Accept-Encoding" || res.Header.Get("ETag") != tag {
		t.Errorf("If-None-Match com o ETag fraco: status %d, Vary %q, ETag %q", res.StatusCode, res.Header.Get("Vary"), res.Header.Get("ETag"))
	}
	// um ?v= antigo (página em cache de antes do deploy) não pode prender o arquivo novo por um ano
	if res, _ := pedir(t, srv.URL+"/_static/shell.js?v=0000000000000000", gz); res.Header.Get("Cache-Control") != "no-cache" {
		t.Errorf("?v= antigo: Cache-Control %q", res.Header.Get("Cache-Control"))
	}
	// uma fonte que não existe não pode ficar um ano no cache como 404 (o FileServer apaga o Cache-Control no erro)
	if res, _ := pedir(t, srv.URL+"/_static/fonts/nao-existe.woff2", gz); res.StatusCode != 404 || res.Header.Get("Cache-Control") == umAno {
		t.Errorf("fonte inexistente: status %d, Cache-Control %q", res.StatusCode, res.Header.Get("Cache-Control"))
	}
	// a fonte já vem comprimida: o woff2 não passa pelo gzip
	if res, _ := pedir(t, srv.URL+"/_static/fonts/jetbrains-mono-latin-400-normal.woff2", gz); res.Header.Get("Content-Encoding") != "" {
		t.Errorf("woff2 com Content-Encoding %q", res.Header.Get("Content-Encoding"))
	}
}

func TestStEstaticoInexistente(t *testing.T) {
	if _, err := st("nao-existe.js"); err == nil {
		t.Error("st de um arquivo que não existe deveria falhar")
	}
}

// capturaLog devolve o que o pacote log escreveu durante f.
func capturaLog(t *testing.T, f func()) string {
	var b strings.Builder
	log.SetOutput(&b)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	f()
	log.SetOutput(os.Stderr)
	return b.String()
}

func TestLentasPeloPadraoDaRota(t *testing.T) {
	velho := limiteLenta
	limiteLenta = 0
	t.Cleanup(func() { limiteLenta = velho })
	mux := http.NewServeMux()
	mux.HandleFunc("GET /x/{nome}", func(http.ResponseWriter, *http.Request) {})
	srv := httptest.NewServer(lentas(mux))
	defer srv.Close()
	saida := capturaLog(t, func() {
		pedir(t, srv.URL+"/x/segredo", nil)
		pedir(t, srv.URL+"/x/espera?esperar=1", nil) // long-poll: demora de propósito
	})
	if !strings.Contains(saida, "lenta: GET /x/{nome}") || strings.Contains(saida, "segredo") || strings.Count(saida, "lenta:") != 1 {
		t.Errorf("log: %q", saida)
	}
}

func TestVitais(t *testing.T) {
	a, err := openApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.routes())
	defer srv.Close()
	enviar := func(corpo string) int {
		res, err := http.Post(srv.URL+"/_vitais", "application/json", strings.NewReader(corpo))
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	var st int
	saida := capturaLog(t, func() {
		st = enviar(`{"pagina":"/ana/pessoal/cpf","lcp":812.4,"inp":40,"cls":0.0123,"fcp":500,"ttfb":120}`)
	})
	if st != 204 || !strings.Contains(saida, "vitais: /{usuario}/{doc} lcp=812ms inp=40ms cls=0.012 fcp=500ms ttfb=120ms") || strings.Contains(saida, "cpf") {
		t.Errorf("status %d, log %q", st, saida)
	}
	if saida := capturaLog(t, func() { enviar(`{"pagina":"/","lcp":1e308,"cls":-5}`) }); !strings.Contains(saida, "lcp=1000000ms") || !strings.Contains(saida, "cls=0.000") {
		t.Errorf("valores absurdos chegam ao log: %q", saida)
	}
	if st := enviar(`não é json`); st != 400 {
		t.Errorf("corpo inválido: status %d", st)
	}
	for i := 0; i < 40; i++ {
		st = enviar(`{}`)
	}
	if st != http.StatusTooManyRequests {
		t.Errorf("sem limite por IP: status %d", st)
	}
}

func TestTipoDePagina(t *testing.T) {
	for p, quero := range map[string]string{
		"/": "/", "/_login": "/_login", "/_join/abc": "/_join/{token}", "/publico/a/b": "/publico/{doc}",
		"/ana/notas": "/{usuario}/{doc}", "/ana/_mcp": "/{usuario}/_mcp", "/ana/_mcp/c/x": "/{usuario}/_mcp/c/{nome}",
		"/ana": "outra", "/X\nlenta: falsa": "outra", "": "outra",
	} {
		if got := tipoDePagina(p); got != quero {
			t.Errorf("tipoDePagina(%q) = %q, quero %q", p, got, quero)
		}
	}
}

func TestAceitaGzip(t *testing.T) {
	for ae, quero := range map[string]bool{
		"gzip": true, "gzip, deflate, br, zstd": true, "br;q=1.0, gzip;q=0.8": true, " gzip ; q=0.5": true,
		"gzip;q=0": false, "gzip;q=0.0": false, "br, gzip;q=0": false, "x-gzip": false, "": false, "identity": false,
	} {
		if got := aceitaGzip(ae); got != quero {
			t.Errorf("aceitaGzip(%q) = %v, quero %v", ae, got, quero)
		}
	}
}
