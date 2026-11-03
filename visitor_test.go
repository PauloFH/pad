package main

import (
	"encoding/base64"
	"strings"
	"testing"
)

func novoApp(t *testing.T, dir string) *app {
	t.Helper()
	a, err := openApp(dir)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestVisitorIdaEVolta(t *testing.T) {
	a := novoApp(t, t.TempDir())
	id, name, token := a.newVisitor()
	gid, gname, ok := a.parseVisitor(token)
	if !ok || gid != id || gname != name {
		t.Fatalf("parseVisitor = %q %q %v; quer %q %q true", gid, gname, ok, id, name)
	}
}

func TestVisitorAdulterado(t *testing.T) {
	a := novoApp(t, t.TempDir())
	id, _, token := a.newVisitor()
	_, sig, _ := strings.Cut(token, ".")
	forjado := base64.RawURLEncoding.EncodeToString([]byte(id+"|Capivara Falsa")) + "." + sig
	if _, _, ok := a.parseVisitor(forjado); ok {
		t.Error("nome trocado foi aceito")
	}
	if _, _, ok := novoApp(t, t.TempDir()).parseVisitor(token); ok {
		t.Error("token de outra chave foi aceito")
	}
	for _, lixo := range []string{"", ".", "abc", "a.b", "!!!.@@@", token + ".x", strings.Split(token, ".")[0]} {
		if _, _, ok := a.parseVisitor(lixo); ok {
			t.Errorf("lixo %q foi aceito", lixo)
		}
	}
}

func TestVisitorChavePersiste(t *testing.T) {
	dir := t.TempDir()
	id, name, token := novoApp(t, dir).newVisitor()
	gid, gname, ok := novoApp(t, dir).parseVisitor(token)
	if !ok || gid != id || gname != name {
		t.Fatal("token antigo recusado após reabrir")
	}
}

func TestNomesConcordam(t *testing.T) {
	validos := map[string]bool{}
	animais := map[string]bool{}
	for _, b := range bichos {
		if b.nome == "" || animais[b.nome] {
			t.Fatalf("bicho vazio ou duplicado: %q", b.nome)
		}
		animais[b.nome] = true
		for _, adj := range adjetivos {
			if adj.m == "" || adj.f == "" {
				t.Fatalf("adjetivo vazio: %+v", adj)
			}
			if b.fem {
				validos[b.nome+" "+adj.f] = true
			} else {
				validos[b.nome+" "+adj.m] = true
			}
		}
	}
	if len(bichos) < 30 || len(adjetivos) < 30 {
		t.Fatalf("listas curtas: %d bichos, %d adjetivos", len(bichos), len(adjetivos))
	}
	adjs := map[string]bool{}
	for _, adj := range adjetivos {
		if adjs[adj.m] || adjs[adj.f] {
			t.Fatalf("adjetivo duplicado: %+v", adj)
		}
		adjs[adj.m], adjs[adj.f] = true, true
	}
	a := novoApp(t, t.TempDir())
	for range 2000 {
		_, name, _ := a.newVisitor()
		if !validos[name] {
			t.Fatalf("nome inválido: %q", name)
		}
	}
}
