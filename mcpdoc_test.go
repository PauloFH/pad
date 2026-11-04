package main

import (
	"encoding/json"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestCaminhoDoc(t *testing.T) {
	for _, s := range []string{"/ana/notas", "ana/notas", "@pad /ana/notas", "https://pad.exemplo/ana/notas", "pad://ana/notas", " @pad  /ana/notas "} {
		if got, ok := caminhoDoc(s); !ok || got != "ana/notas" {
			t.Errorf("%q: %q %v", s, got, ok)
		}
	}
	for _, s := range []string{"../x", "/ana/", "a/b/c/d/e/f/g", "", "@pad", "ana//x", "ana/x.txt"} {
		if got, ok := caminhoDoc(s); ok {
			t.Errorf("%q devia falhar, veio %q", s, got)
		}
	}
	if got, ok := caminhoDoc("a/b/c/d/e/f"); !ok || got != "a/b/c/d/e/f" {
		t.Errorf("6 níveis (5 pastas) vale: %q %v", got, ok)
	}
}

func TestDiffOp(t *testing.T) {
	for _, c := range []struct {
		de, para string
		quer     Op
	}{
		{"abc", "abXc", Op{{Retain: 2}, {Insert: "X"}, {Retain: 1}}},
		{"", "x", Op{{Insert: "x"}}},
		{"x", "", Op{{Delete: 1}}},
		{"a😀b", "a😃b", Op{{Retain: 1}, {Delete: 2}, {Insert: "😃"}, {Retain: 1}}},
		{"😀a", "😃a", Op{{Delete: 2}, {Insert: "😃"}, {Retain: 1}}},
		{"a😀", "a😃", Op{{Retain: 1}, {Delete: 2}, {Insert: "😃"}}},
	} {
		if got := diffOp(utf16Of(c.de), utf16Of(c.para)); !slices.Equal(got, c.quer) {
			t.Errorf("%q -> %q: %v, queria %v", c.de, c.para, got, c.quer)
		}
	}
	rng := rand.New(rand.NewPCG(1, 2))
	pedacos := []string{"a", "b", "😀", "😃", "\n", "ç"}
	aleatorio := func() string {
		var b strings.Builder
		for range rng.IntN(8) {
			b.WriteString(pedacos[rng.IntN(len(pedacos))])
		}
		return b.String()
	}
	for range 200 {
		de, para := aleatorio(), aleatorio()
		got, err := diffOp(utf16Of(de), utf16Of(para)).Apply(utf16Of(de))
		if err != nil || stringOf(got) != para {
			t.Fatalf("%q -> %q: %q, %v", de, para, stringOf(got), err)
		}
	}
}

// docApp sobe o servidor do wsApp e dá um agente de ana já apresentado.
func docApp(t *testing.T) (*app, *httptest.Server, *mcpCliente, func(string) *http.Client) {
	t.Helper()
	a, srv, login := wsApp(t)
	ag := mcpCli(t, srv, chaveDe(t, a, 1, "note"), "claude-code") // ana é o usuário 1
	ag.quer("apresentar", map[string]any{"apelido": "pai"}, false, "você é pai")
	return a, srv, ag, login
}

// versaoLida tira a versão da primeira linha do ler_documento.
func versaoLida(t *testing.T, ag *mcpCliente, caminho string) (versao, texto string) {
	t.Helper()
	out := ag.quer("ler_documento", map[string]any{"caminho": caminho}, false, "versao: ")
	cab, texto, ok := strings.Cut(out, "\n\n")
	if !ok {
		t.Fatalf("formato: %q", out)
	}
	return strings.TrimPrefix(cab, "versao: "), texto
}

func get(t *testing.T, c *http.Client, u string) string {
	t.Helper()
	res, err := c.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return string(b)
}

func TestLerEscreverDocumento(t *testing.T) {
	a, srv, ag, login := docApp(t)
	url := srv.URL
	ana := login("ana")
	post(t, ana, url+"/ana/notas", "text/plain", "um")
	v1, texto := versaoLida(t, ag, "@pad /ana/notas")
	if texto != "um" {
		t.Fatalf("texto lido %q", texto)
	}
	v2 := ag.quer("escrever_documento", map[string]any{"caminho": "/ana/notas", "texto": "um dois", "versao": v1}, false, "gravado (versao: ")
	if !strings.Contains(get(t, ana, url+"/ana/notas"), "um dois") {
		t.Fatal("o GET não mostra o texto gravado")
	}
	ag.quer("escrever_documento", map[string]any{"caminho": "/ana/notas", "texto": "velha", "versao": v1}, true, "documento mudou desde a sua leitura, leia de novo")
	// a escrita conta como leitura: escreve de novo sem reler
	v3 := strings.TrimSuffix(strings.TrimPrefix(v2, "gravado (versao: "), ")")
	ag.quer("escrever_documento", map[string]any{"caminho": "ana/notas", "texto": "um dois\r\ntrês", "versao": v3}, false, "gravado")
	if _, texto = versaoLida(t, ag, "ana/notas"); texto != "um dois\ntrês" {
		t.Fatalf("depois da segunda escrita: %q", texto)
	}

	post(t, login("bia"), url+"/bia/x", "text/plain", "da bia")
	ag.quer("ler_documento", map[string]any{"caminho": "/bia/x"}, true, "documento não encontrado ou sem acesso")
	ag.quer("escrever_documento", map[string]any{"caminho": "/bia/x", "texto": "oi", "versao": ""}, true, "sem acesso")
	ag.quer("escrever_documento", map[string]any{"caminho": "/bia/novo", "texto": "oi", "versao": ""}, true, "sem acesso")
	ag.quer("ler_documento", map[string]any{"caminho": "/bia/nada"}, true, "sem acesso") // não revela o que não existe
	ag.quer("ler_documento", map[string]any{"caminho": "../x"}, true, "caminho")

	ag.quer("ler_documento", map[string]any{"caminho": "/ana/novo"}, true, "documento não existe; para criar, use escrever_documento com versao vazia")
	if a.hub.connected("ana/novo") || a.hub.docs["ana/novo"] != nil {
		t.Fatal("ler um documento que não existe não devia abri-lo")
	}
	ag.quer("escrever_documento", map[string]any{"caminho": "/ana/novo", "texto": "novo", "versao": ""}, false, "gravado")
	var dono string
	if err := a.db.QueryRow(`SELECT u.name FROM docs d JOIN users u ON u.id = d.owner_id WHERE d.name = 'ana/novo'`).Scan(&dono); err != nil || dono != "ana" {
		t.Fatalf("dono %q, %v", dono, err)
	}
	if b, _ := a.readDoc("ana/novo"); b != "novo" {
		t.Fatalf("no disco: %q", b)
	}
	// versão vazia sobre documento com texto não sobrescreve
	ag.quer("escrever_documento", map[string]any{"caminho": "/ana/novo", "texto": "outro", "versao": ""}, true, "leia de novo")

	ag.quer("ler_documento", map[string]any{"caminho": "/publico/mural"}, true, "não existe")
	ag.quer("escrever_documento", map[string]any{"caminho": "/publico/mural", "texto": "oi", "versao": ""}, false, "gravado")
	if _, texto = versaoLida(t, ag, "/publico/mural"); texto != "oi" {
		t.Fatalf("publico: %q", texto)
	}
}

func TestEscreverJuntaComQuemDigita(t *testing.T) {
	_, srv, ag, login := docApp(t)
	url := srv.URL
	ana := login("ana")
	post(t, ana, url+"/ana/notas", "text/plain", "linha1\nlinha2")
	tc := dialWS(t, srv, ana, "ana/notas", "c1", "")
	v, _ := versaoLida(t, ag, "/ana/notas")
	if err := tc.edit(Op{{Insert: "X"}, {Retain: 13}}, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := tc.until(isType("ack")); err != nil {
		t.Fatal(err)
	}
	ag.quer("escrever_documento", map[string]any{"caminho": "/ana/notas", "texto": "linha1\nlinha2\nlinha3", "versao": v}, false, "gravado")
	m, err := tc.until(isType("op"))
	if err != nil || m.By != "" {
		t.Fatalf("op: %+v, %v", m, err)
	}
	if got := stringOf(tc.text); got != "Xlinha1\nlinha2\nlinha3" {
		t.Fatalf("a aba ficou com %q", got)
	}
	if _, texto := versaoLida(t, ag, "/ana/notas"); texto != "Xlinha1\nlinha2\nlinha3" {
		t.Fatalf("o agente lê %q", texto)
	}
}

func TestEscreverTrechoTravado(t *testing.T) {
	_, srv, ag, login := docApp(t)
	url := srv.URL
	ana := login("ana")
	post(t, ana, url+"/ana/notas", "text/plain", "linha1\nlinha2")
	tc := dialWS(t, srv, ana, "ana/notas", "c1", "")
	if err := tc.write(map[string]any{"t": "sel", "rev": tc.rev, "a": 7, "h": 13}); err != nil {
		t.Fatal(err)
	}
	if _, err := tc.until(func(m wsMsg) bool {
		return m.T == "peers" && slices.ContainsFunc(m.Peers, func(p peerView) bool { return p.ID == tc.init.Me && p.Lock })
	}); err != nil {
		t.Fatal(err)
	}
	v, _ := versaoLida(t, ag, "/ana/notas")
	ag.quer("escrever_documento", map[string]any{"caminho": "/ana/notas", "texto": "linha1\nLINHA2", "versao": v}, true, "trecho travado por ana")
	if _, texto := versaoLida(t, ag, "/ana/notas"); texto != "linha1\nlinha2" {
		t.Fatalf("o texto mudou: %q", texto)
	}
	// fora da trava passa
	ag.quer("escrever_documento", map[string]any{"caminho": "/ana/notas", "texto": "LINHA1\nlinha2", "versao": v}, false, "gravado")
}

func TestResources(t *testing.T) {
	a, srv, ag, login := docApp(t)
	url := srv.URL
	post(t, login("ana"), url+"/ana/notas", "text/plain", "um")
	post(t, login("bia"), url+"/bia/x", "text/plain", "da bia")
	res, _ := ag.call("resources/list", map[string]any{})
	var lista struct {
		Resources []struct{ URI, Name, MimeType string }
	}
	if err := json.Unmarshal(res, &lista); err != nil {
		t.Fatal(err)
	}
	if len(lista.Resources) != 1 || lista.Resources[0].URI != "pad://ana/notas" || lista.Resources[0].Name != "/ana/notas" || lista.Resources[0].MimeType != "text/plain" {
		t.Fatalf("resources/list: %s", res)
	}
	res, _ = ag.call("resources/read", map[string]any{"uri": "pad://ana/notas"})
	var lido struct {
		Contents []struct{ URI, MimeType, Text string }
	}
	if err := json.Unmarshal(res, &lido); err != nil || len(lido.Contents) != 1 || lido.Contents[0].Text != "um" ||
		lido.Contents[0].URI != "pad://ana/notas" || lido.Contents[0].MimeType != "text/plain" {
		t.Fatalf("resources/read: %s, %v", res, err)
	}
	for _, uri := range []string{"pad://bia/x", "pad://../x", "pad://ana/nada"} {
		_, b := ag.post(`{"jsonrpc":"2.0","id":3,"method":"resources/read","params":{"uri":"` + uri + `"}}`)
		var r struct{ Error *rpcErro }
		if json.Unmarshal(b, &r) != nil || r.Error == nil || r.Error.Code != -32602 || r.Error.Message != "documento não encontrado ou sem acesso" {
			t.Errorf("%s: %s", uri, b)
		}
	}
	// resources/read não conta como leitura para o escrever
	ag.sessaoEm(a, func(s *sessao) {
		if len(s.leituras) != 0 {
			t.Errorf("leituras: %v", s.leituras)
		}
	})
}

// Fora da memória o log se perdeu: a escrita só passa se o texto no disco é o lido.
func TestEscreverDepoisDeSairDaMemoria(t *testing.T) {
	old := keepAlive
	keepAlive = 20 * time.Millisecond
	t.Cleanup(func() { keepAlive = old })
	a, srv, ag, login := docApp(t)
	ana := login("ana")
	post(t, ana, srv.URL+"/ana/notas", "text/plain", "um")
	fora := func() {
		eventually(t, "o documento não saiu da memória", func() bool {
			a.hub.mu.Lock()
			defer a.hub.mu.Unlock()
			return a.hub.docs["ana/notas"] == nil
		})
	}
	v, _ := versaoLida(t, ag, "/ana/notas")
	fora()
	ag.quer("escrever_documento", map[string]any{"caminho": "/ana/notas", "texto": "um dois", "versao": v}, false, "gravado")
	v, _ = versaoLida(t, ag, "/ana/notas")
	fora()
	post(t, ana, srv.URL+"/ana/notas", "text/plain", "da ana")
	ag.quer("escrever_documento", map[string]any{"caminho": "/ana/notas", "texto": "do agente", "versao": v}, true, "leia de novo")
	if b, _ := a.readDoc("ana/notas"); b != "da ana" {
		t.Fatalf("no disco: %q", b)
	}
}
