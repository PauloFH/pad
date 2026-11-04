package main

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func msg(autor, rotulo, texto string) mensagem {
	return mensagem{Autor: autor, Rotulo: rotulo, Texto: texto}
}

func textos(ms []mensagem) []string {
	var t []string
	for _, m := range ms {
		t = append(t, m.Texto)
	}
	return t
}

func quer(t *testing.T, got []mensagem, err error, want ...string) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if g := strings.Join(textos(got), "|"); g != strings.Join(want, "|") {
		t.Fatalf("mensagens %q, quero %q", g, want)
	}
}

func TestConversaLido(t *testing.T) {
	a := appComUsuarios(t, "ana")
	pai := msg("pai", "pai · claude · desktop", "faz o login")
	if err := a.criarConversa(1, "login", pai); err != nil {
		t.Fatal(err)
	}
	if err := a.criarConversa(1, "login", pai); !errors.Is(err, errConversaExiste) {
		t.Fatalf("repetida: %v", err)
	}
	ms, err := a.lerConversa(t.Context(), 1, "login", "filho", false)
	quer(t, ms, err, "faz o login") // primeira leitura traz tudo
	ms, err = a.lerConversa(t.Context(), 1, "login", "filho", false)
	quer(t, ms, err)
	if _, err := a.enviarMensagem(1, "login", msg("filho", "filho · codex · note", "dúvida")); err != nil {
		t.Fatal(err)
	}
	ms, err = a.lerConversa(t.Context(), 1, "login", "filho", false)
	quer(t, ms, err) // a própria não volta
	nv, err := a.novidades(t.Context(), 1, nil, "pai")
	if err != nil || len(nv) != 1 {
		t.Fatalf("novidades %v %v", nv, err)
	}
	quer(t, nv["login"], nil, "dúvida")
	nv, err = a.novidades(t.Context(), 1, nil, "pai")
	if err != nil || len(nv) != 0 {
		t.Fatalf("segunda vez: %v %v", nv, err)
	}
	h := mensagem{Autor: "ana", Humano: true, Rotulo: "ana", Texto: "eu"}
	if _, err := a.enviarMensagem(1, "login", h); err != nil {
		t.Fatal(err)
	}
	nv, err = a.novidades(t.Context(), 1, []string{"login"}, "pai")
	quer(t, nv["login"], err, "eu")
	if !nv["login"][0].Humano {
		t.Error("Humano perdido")
	}
	ms, err = a.lerConversa(t.Context(), 1, "login", "filho", true)
	quer(t, ms, err, "faz o login", "dúvida", "eu")

	// apelido novo que já falou: só o que veio depois da última fala dele
	if _, err := a.enviarMensagem(1, "login", msg("outro", "outro · claude · x", "oi")); err != nil {
		t.Fatal(err)
	}
	h.Texto = "depois"
	if _, err := a.enviarMensagem(1, "login", h); err != nil {
		t.Fatal(err)
	}
	nv, err = a.novidades(t.Context(), 1, nil, "outro")
	quer(t, nv["login"], err, "depois")

	if _, err := a.enviarMensagem(1, "login", msg("pai", "pai", strings.Repeat("x", 64<<10+1))); !errors.Is(err, errMsgGrande) {
		t.Errorf("grande: %v", err)
	}
	if _, err := a.enviarMensagem(1, "login", msg("pai", "pai", " \n")); !errors.Is(err, errMsgVazia) {
		t.Errorf("vazia: %v", err)
	}
	if _, err := a.enviarMensagem(1, "nao-existe", msg("pai", "pai", "x")); !errors.Is(err, errSemConversa) {
		t.Errorf("sem conversa: %v", err)
	}
	if _, err := a.lerConversa(t.Context(), 1, "nao-existe", "pai", false); !errors.Is(err, errSemConversa) {
		t.Errorf("ler sem conversa: %v", err)
	}
	if _, err := a.novidades(t.Context(), 1, []string{"nao-existe"}, "pai"); !errors.Is(err, errSemConversa) {
		t.Errorf("novidades sem conversa: %v", err)
	}
	if err := a.criarConversa(1, "Com Espaço", pai); err == nil || !strings.Contains(err.Error(), "nome inválido") {
		t.Errorf("nome inválido: %v", err)
	}
	if err := a.criarConversa(1, "vazia", msg("pai", "pai", "  ")); !errors.Is(err, errMsgVazia) {
		t.Errorf("criar vazia: %v", err)
	}
	if _, err := a.lerConversa(t.Context(), 1, "vazia", "pai", false); !errors.Is(err, errSemConversa) {
		t.Errorf("criar vazia não deve deixar a conversa: %v", err)
	}
}

func TestConversasPorUsuarioEMensagensSoLe(t *testing.T) {
	a := appComUsuarios(t, "ana", "bia")
	if err := a.criarConversa(1, "c", msg("pai", "pai", "a")); err != nil {
		t.Fatal(err)
	}
	if err := a.criarConversa(2, "c", msg("pai", "pai", "b")); err != nil {
		t.Fatalf("o nome é por usuário: %v", err)
	}
	ms, err := a.mensagens(1, "c", 0)
	quer(t, ms, err, "a")
	if _, err := a.mensagens(1, "c", ms[0].ID); err != nil {
		t.Fatal(err)
	}
	// mensagens não marca como lido: a primeira leitura do agente ainda traz tudo
	ms, err = a.lerConversa(t.Context(), 1, "c", "filho", false)
	quer(t, ms, err, "a")
	if ms, err = a.mensagens(1, "c", ms[0].ID); err != nil || len(ms) != 0 {
		t.Fatalf("depois do id: %v %v", ms, err)
	}
	if _, err := a.mensagens(2, "x", 0); !errors.Is(err, errSemConversa) {
		t.Errorf("sem conversa: %v", err)
	}
}

func TestListarEApagarConversas(t *testing.T) {
	a := appComUsuarios(t, "ana")
	a.criarConversa(1, "velha", msg("pai", "pai · claude · m", "1"))
	a.criarConversa(1, "nova", msg("filho", "filho · codex · m", "2"))
	a.enviarMensagem(1, "velha", msg("filho", "filho · codex · m", "3"))
	cs, err := a.listarConversas(1, "pai")
	if err != nil || len(cs) != 2 {
		t.Fatalf("%v %v", cs, err)
	}
	if cs[0].Nome != "velha" || cs[0].UltimoRotulo != "filho · codex · m" || cs[0].NaoLidas != 1 {
		t.Errorf("velha (a mais recente): %+v", cs[0])
	}
	if cs[1].Nome != "nova" || cs[1].NaoLidas != 1 {
		t.Errorf("nova: %+v", cs[1])
	}
	if d := time.Since(cs[0].UltimaEm); d < 0 || d > time.Minute {
		t.Errorf("UltimaEm %v", cs[0].UltimaEm)
	}
	// lerConversa zera o contador só dessa conversa e só para esse apelido
	if _, err := a.lerConversa(t.Context(), 1, "velha", "pai", false); err != nil {
		t.Fatal(err)
	}
	cs, _ = a.listarConversas(1, "pai")
	if cs[0].NaoLidas != 0 || cs[1].NaoLidas != 1 {
		t.Errorf("depois de ler: %+v", cs)
	}
	cs, _ = a.listarConversas(1, "filho")
	if cs[0].NaoLidas != 0 || cs[1].NaoLidas != 0 {
		t.Errorf("filho falou nas duas: %+v", cs)
	}
	if err := a.apagarConversa(1, "velha"); err != nil {
		t.Fatal(err)
	}
	if err := a.apagarConversa(1, "velha"); !errors.Is(err, errSemConversa) {
		t.Errorf("apagar de novo: %v", err)
	}
	var n int
	a.db.QueryRow(`SELECT (SELECT COUNT(*) FROM mcp_mensagens) + (SELECT COUNT(*) FROM mcp_lido)`).Scan(&n)
	if n != 1 { // sobra só a mensagem de "nova"
		t.Errorf("cascata deixou %d linhas", n)
	}
}

func TestSinalNovidade(t *testing.T) {
	a := appComUsuarios(t, "ana")
	s := a.mcp.sinal()
	if err := a.criarConversa(1, "c", msg("pai", "pai", "x")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s:
	case <-time.After(time.Second):
		t.Fatal("criarConversa não avisou")
	}
	s = a.mcp.sinal()
	if _, err := a.enviarMensagem(1, "c", msg("pai", "pai", "y")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s:
	case <-time.After(time.Second):
		t.Fatal("enviarMensagem não avisou")
	}
}
