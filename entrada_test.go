package main

import (
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
	for _, s := range []string{`id="coluna"`, `placeholder="nome do novo documento"`, "Nenhum documento ainda"} {
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
