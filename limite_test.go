package main

import (
	"testing"
	"time"
)

func TestLimite(t *testing.T) {
	l := novoLimite(2, time.Minute)
	t0 := time.Unix(1000, 0)
	for i, want := range []bool{true, true, false} {
		if got := l.permite("a", t0); got != want {
			t.Fatalf("evento %d: %v", i, got)
		}
	}
	if !l.permite("b", t0) {
		t.Fatal("uma chave gastou a cota da outra")
	}
	if !l.cheio("a", t0) || l.cheio("b", t0) {
		t.Fatal("cheio errado")
	}
	if !l.permite("a", t0.Add(time.Minute)) {
		t.Fatal("a janela nova não zerou")
	}
	if len(l.cont) != 1 {
		t.Fatalf("janela nova guardou %d chaves", len(l.cont))
	}
}

func TestRitmo(t *testing.T) {
	r := ritmo{porSeg: 1000, folga: 2000}
	t0 := time.Unix(1000, 0)
	if d := r.gastar(1000, t0); d != 0 {
		t.Fatalf("dentro da folga esperou %v", d)
	}
	d := r.gastar(2000, t0)
	if d <= 0 || d > ritmoEsperaMax {
		t.Fatalf("acima da folga esperou %v", d)
	}
	if d := r.gastar(1<<30, t0); d != ritmoEsperaMax {
		t.Fatalf("a espera passou do teto: %v", d)
	}
	r = ritmo{porSeg: 1000, folga: 2000}
	r.gastar(3000, t0)
	if d := r.gastar(0, t0.Add(10*time.Second)); d != 0 {
		t.Fatalf("a dívida não caiu com o tempo: %v", d)
	}
}
