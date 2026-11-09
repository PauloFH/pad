package main

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// appComUsuarios cria um app em diretório temporário com os usuários, de id 1 em diante na ordem dada.
func appComUsuarios(t *testing.T, names ...string) *app {
	t.Helper()
	a, err := openApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if err := runCLI(a, []string{"useradd", n}, strings.NewReader("senha-forte\n")); err != nil {
			t.Fatal(n, err)
		}
	}
	return a
}

func TestMaquinas(t *testing.T) {
	a := appComUsuarios(t, "ana", "bia")
	chave, err := a.criarMaquina(1, " Notebook ")
	if err != nil || !strings.HasPrefix(chave, "pad_") || len(chave) <= 20 {
		t.Fatalf("chave %q, err %v", chave, err)
	}
	if _, err := a.criarMaquina(1, "notebook"); !errors.Is(err, errMaquinaExiste) {
		t.Fatalf("nome repetido: %v", err)
	}
	if _, err := a.criarMaquina(2, "notebook"); err != nil {
		t.Fatalf("o nome é por usuário: %v", err)
	}
	if _, err := a.criarMaquina(1, "com espaço"); err == nil || !strings.Contains(err.Error(), "nome inválido") {
		t.Fatalf("nome inválido: %v", err)
	}
	m, u, ok, err := a.maquinaDaChave(chave)
	if err != nil || !ok || m.Nome != "notebook" || u.Name != "ana" {
		t.Fatalf("maquinaDaChave: %+v %+v %v %v", m, u, ok, err)
	}
	if _, _, ok, _ := a.maquinaDaChave("pad_errada"); ok {
		t.Fatal("chave errada aceita")
	}
	var n int
	a.db.QueryRow(`SELECT count(*) FROM mcp_maquinas WHERE hash = ?`, []byte(chave)).Scan(&n)
	if n != 0 {
		t.Fatal("a chave não pode ficar no banco")
	}
	ms, _ := a.listarMaquinas(1)
	if len(ms) != 1 {
		t.Fatalf("listarMaquinas: %+v", ms)
	}
	a.revogarMaquina(2, ms[0].ID) // não é dela: nada muda
	if _, _, ok, _ := a.maquinaDaChave(chave); !ok {
		t.Fatal("bia revogou máquina da ana")
	}
	a.revogarMaquina(1, ms[0].ID)
	if _, _, ok, _ := a.maquinaDaChave(chave); ok {
		t.Fatal("chave revogada ainda vale")
	}
}

func TestMarcarVistaGravaNoMaximoUmaVezPorMinuto(t *testing.T) {
	a := appComUsuarios(t, "ana")
	chave, _ := a.criarMaquina(1, "notebook")
	m, _, _, _ := a.maquinaDaChave(chave)
	t0 := time.Unix(1_000_000, 0)
	a.marcarVista(m.ID, t0)
	a.marcarVista(m.ID, t0.Add(30*time.Second))
	ms, _ := a.listarMaquinas(1)
	if !ms[0].Vista.Equal(t0) {
		t.Fatalf("vista = %v, queria %v", ms[0].Vista, t0)
	}
	a.marcarVista(m.ID, t0.Add(2*time.Minute))
	ms, _ = a.listarMaquinas(1)
	if !ms[0].Vista.Equal(t0.Add(2 * time.Minute)) {
		t.Fatalf("vista = %v", ms[0].Vista)
	}
}
