package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

type novaChave struct{ Nome, Chave, Claude, Codex string }

type painelPage struct {
	Me         string
	Admin      bool
	Conversas  []conversaRow
	Agentes    map[string][]string
	Maquinas   []maquina
	Conectados []agenteView
	Resumo     string
	Nova       *novaChave
	Err        string
}

// resumo conta os agentes por tipo, em ordem alfabética; Claude e Codex com maiúscula, os outros pelo próprio nome.
func resumo(ags []agenteView) string {
	if len(ags) == 0 {
		return "nenhum agente conectado"
	}
	por := map[string]int{}
	for _, a := range ags {
		por[a.Tipo]++
	}
	tipos := make([]string, 0, len(por))
	for t := range por {
		tipos = append(tipos, t)
	}
	slices.Sort(tipos)
	partes := make([]string, len(tipos))
	for i, t := range tipos {
		nome := t
		if t == "claude" || t == "codex" {
			nome = maiuscula(t)
		}
		partes[i] = fmt.Sprintf("%d %s", por[t], nome)
	}
	s := "agentes"
	if len(ags) == 1 {
		s = "agente"
	}
	return fmt.Sprintf("%d %s: %s", len(ags), s, strings.Join(partes, ", "))
}

// painelMCP junta o que a página mostra; a nova chave (se houver) quem chama põe depois.
func (a *app) painelMCP(u user) (painelPage, error) {
	now := time.Now()
	p := painelPage{Me: u.Name, Admin: u.Admin, Agentes: map[string][]string{}}
	var err error
	if p.Conversas, err = a.listarConversas(u.ID, u.Name); err != nil {
		return p, err
	}
	for _, c := range p.Conversas {
		p.Agentes[c.Nome] = a.mcp.agentesNa(u.ID, c.Nome, now)
	}
	if p.Maquinas, err = a.listarMaquinas(u.ID); err != nil {
		return p, err
	}
	p.Conectados = a.mcp.conectados(u.ID, now)
	p.Resumo = resumo(p.Conectados)
	return p, nil
}

func (a *app) painel(w http.ResponseWriter, r *http.Request, u user) {
	p, err := a.painelMCP(u)
	if err != nil {
		serverError(w, err)
		return
	}
	render(w, http.StatusOK, "mcp", p)
}

func (a *app) novaMaquina(w http.ResponseWriter, r *http.Request, u user) {
	chave, err := a.criarMaquina(u.ID, r.FormValue("nome"))
	status := http.StatusOK
	if errors.Is(err, errNomeInvalido) || errors.Is(err, errMaquinaExiste) {
		status = http.StatusBadRequest
	} else if err != nil {
		serverError(w, err)
		return
	}
	p, perr := a.painelMCP(u)
	if perr != nil {
		serverError(w, perr)
		return
	}
	if err != nil {
		p.Err = err.Error()
	} else {
		nome, _ := curto(r.FormValue("nome"))
		p.Nova = comandos(r, nome, chave)
	}
	render(w, status, "mcp", p)
}

// comandos monta o que a pessoa cola na máquina; a base vem da própria requisição.
func comandos(r *http.Request, nome, chave string) *novaChave {
	scheme := "http"
	if isHTTPS(r) {
		scheme = "https"
	}
	base := scheme + "://" + r.Host + "/_mcp"
	return &novaChave{
		Nome:   nome,
		Chave:  chave,
		Claude: `claude mcp add --scope user --transport http pad ` + base + ` --header "Authorization: Bearer ` + chave + `"`,
		Codex: "# ~/.codex/config.toml\n[mcp_servers.pad]\nurl = \"" + base + "\"\nbearer_token_env_var = \"PAD_MCP_CHAVE\"\n" +
			"# e no shell: export PAD_MCP_CHAVE=" + chave,
	}
}

func (a *app) revogar(w http.ResponseWriter, r *http.Request, u user) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		fail(w, http.StatusNotFound, "máquina não encontrada")
		return
	}
	if err := a.revogarMaquina(u.ID, id); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/_mcp", http.StatusSeeOther)
}

func (a *app) conectados(w http.ResponseWriter, r *http.Request, u user) {
	ags := a.mcp.conectados(u.ID, time.Now())
	type linha struct {
		Rotulo    string   `json:"rotulo"`
		Apelido   string   `json:"apelido"`
		Maquina   string   `json:"maquina"`
		Tipo      string   `json:"tipo"`
		Conversas []string `json:"conversas"`
		Ultima    string   `json:"ultima"`
	}
	out := struct {
		Resumo  string  `json:"resumo"`
		Agentes []linha `json:"agentes"`
	}{resumo(ags), []linha{}}
	for _, g := range ags {
		out.Agentes = append(out.Agentes, linha{g.Rotulo, g.Apelido, g.Maquina, g.Tipo, append([]string{}, g.Conversas...), hora(g.Ultima)})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}
