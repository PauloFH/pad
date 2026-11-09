package main

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestQuando(t *testing.T) {
	agora := time.Date(2026, 11, 3, 15, 0, 0, 0, time.Local)
	casos := []struct {
		t    time.Time
		want string
	}{
		{agora.Add(-30 * time.Second), "agora"},
		{agora.Add(-5 * time.Minute), "há 5 min"},
		{agora.Add(-3 * time.Hour), "há 3 h"},
		{time.Date(2026, 11, 2, 23, 0, 0, 0, time.Local), "ontem"},
		{time.Date(2026, 10, 12, 9, 0, 0, 0, time.Local), "12 out"},
		{time.Date(2025, 10, 12, 9, 0, 0, 0, time.Local), "12 out 2025"},
	}
	for _, c := range casos {
		if got := quando(c.t, agora); got != c.want {
			t.Errorf("quando(%v) = %q, quero %q", c.t, got, c.want)
		}
	}
}

func TestQuemEsta(t *testing.T) {
	casos := []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{"ana"}, "Ana está aqui"},
		{[]string{"ana", "bia"}, "Ana e Bia estão aqui"},
		{[]string{"ana", "bia", "caio"}, "3 pessoas aqui"},
	}
	for _, c := range casos {
		if got := quemEsta(c.in); got != c.want {
			t.Errorf("quemEsta(%v) = %q, quero %q", c.in, got, c.want)
		}
	}
}

func TestEntradaVazia(t *testing.T) {
	_, srv, login := wsApp(t)
	page := pagina(t, login("ana"), srv.URL+"/")
	for _, s := range []string{`id="coluna"`, `<span>novo documento</span>`, "Nenhum documento ainda"} {
		if !strings.Contains(page, s) {
			t.Errorf("entrada vazia sem %q", s)
		}
	}
	for _, s := range []string{`class="cartao"`, "Continuar de onde parou", "Compartilhados com você"} {
		if strings.Contains(page, s) {
			t.Errorf("entrada vazia com %q", s)
		}
	}
}

func TestEntradaRecentes(t *testing.T) {
	_, srv, login := wsApp(t)
	ana, bia := login("ana"), login("bia")
	post(t, ana, srv.URL+"/ana/a", "text/plain", "a")
	post(t, ana, srv.URL+"/ana/p/x", "text/plain", "x")
	post(t, bia, srv.URL+"/bia/z", "text/plain", "z")
	post(t, bia, srv.URL+"/bia/fora", "text/plain", "não compartilhado")
	post(t, bia, srv.URL+"/_share/bia/z", "application/x-www-form-urlencoded", "tipo=user&alvo=ana")
	dialWS(t, srv, bia, "bia/z", "b1", "") // a bia editando agora
	page := pagina(t, ana, srv.URL+"/")
	i, j := strings.Index(page, "Continuar de onde parou"), strings.Index(page, "Compartilhados com você")
	if i < 0 || j < i {
		t.Fatalf("seções fora de ordem ou ausentes: %d, %d", i, j)
	}
	if n := strings.Count(page[i:j], `class="cartao"`); n != 3 {
		t.Errorf("continuar com %d blocos, quero 3", n)
	}
	comp := page[j:]
	if n := strings.Count(comp, `class="cartao"`); n != 1 {
		t.Errorf("compartilhados com %d blocos, quero 1", n)
	}
	for _, s := range []string{`href="/bia/z"`, "por Bia", "agora", "Bia está aqui"} {
		if !strings.Contains(comp, s) {
			t.Errorf("bloco compartilhado sem %q", s)
		}
	}
	if strings.Contains(page, "bia/fora") {
		t.Error("a entrada mostra documento da bia não compartilhado")
	}
}

// até 9 blocos em "continuar", do editado há menos tempo ao mais antigo; a própria pessoa não conta como "está aqui"
func TestEntradaTetoEOrdem(t *testing.T) {
	a, srv, login := wsApp(t)
	ana := login("ana")
	for i := range 10 {
		n := fmt.Sprintf("ana/d%d", i)
		post(t, ana, srv.URL+"/"+n, "text/plain", "x")
		if _, err := a.db.Exec(`UPDATE docs SET updated = ? WHERE name = ?`, time.Now().Add(time.Duration(i)*-time.Hour).Unix(), n); err != nil {
			t.Fatal(err)
		}
	}
	dialWS(t, srv, ana, "ana/d0", "a1", "") // a ana com o documento aberto noutra aba
	page := pagina(t, ana, srv.URL+"/")
	var got []string
	for _, m := range regexp.MustCompile(`class="cartao" href="/([^"]+)"`).FindAllStringSubmatch(page, -1) {
		got = append(got, m[1])
	}
	want := []string{"ana/d0", "ana/d1", "ana/d2", "ana/d3", "ana/d4", "ana/d5", "ana/d6", "ana/d7", "ana/d8"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("blocos %v, quero %v", got, want)
	}
	if strings.Contains(page, "Ana está aqui") {
		t.Error("a própria pessoa aparece no está aqui")
	}
	if !strings.Contains(page, "<h1") {
		t.Error("entrada sem h1")
	}
	if i := strings.Index(page, `class="criar"`); i < 0 || strings.Contains(page[i:i+300], "autofocus") {
		t.Error("campo de criar com autofocus (abre o teclado no celular)")
	}
}
