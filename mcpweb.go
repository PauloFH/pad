package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
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

// mcpRoutes é a área MCP de cada usuário (/{usuario}/_mcp...), separada do mux principal; o routes() desvia para cá.
func (a *app) mcpRoutes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /{usuario}/_mcp", a.mcpRPC)
	mux.HandleFunc("DELETE /{usuario}/_mcp", a.mcpFim)
	mux.HandleFunc("GET /{usuario}/_mcp", func(w http.ResponseWriter, r *http.Request) {
		// o GET de um cliente MCP (abrir o fluxo SSE, que não oferecemos) não tem cookie: responde 405 em vez de ir ao login
		if r.Header.Get("Authorization") != "" {
			w.Header().Set("Allow", "POST, DELETE")
			http.Error(w, "use POST ou DELETE", http.StatusMethodNotAllowed)
			return
		}
		a.daConta(false, a.painel)(w, r)
	})
	mux.HandleFunc("POST /{usuario}/_mcp/maquinas", a.daConta(false, a.novaMaquina))
	mux.HandleFunc("POST /{usuario}/_mcp/maquinas/{id}/revogar", a.daConta(false, a.revogar))
	mux.HandleFunc("GET /{usuario}/_mcp/conectados", a.daConta(true, a.conectados))
	mux.HandleFunc("POST /{usuario}/_mcp/c", a.daConta(false, a.novaConversa))
	mux.HandleFunc("GET /{usuario}/_mcp/c/{nome}", a.daConta(false, a.chat))
	mux.HandleFunc("GET /{usuario}/_mcp/c/{nome}/msgs", a.daConta(true, a.chatMsgs))
	mux.HandleFunc("POST /{usuario}/_mcp/c/{nome}/msgs", a.daConta(true, a.chatEnviar))
	mux.HandleFunc("POST /{usuario}/_mcp/c/{nome}/apagar", a.daConta(false, a.chatApagar))
	return mux
}

// daConta exige a sessão e que {usuario} seja quem está logado; a área de outro usuário dá 404 (simples nas rotas JSON).
func (a *app) daConta(json bool, h func(http.ResponseWriter, *http.Request, user)) http.HandlerFunc {
	return a.auth(func(w http.ResponseWriter, r *http.Request, u user) {
		switch {
		case r.PathValue("usuario") == u.Name:
			h(w, r, u)
		case json:
			http.NotFound(w, r)
		default:
			render(w, http.StatusNotFound, "msg", msgPage{Msg: "página não encontrada ou sem acesso. Sua área MCP fica em /" + u.Name + "/_mcp", Me: u.Name})
		}
	})
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
	w.Header().Set("Cache-Control", "no-store") // a página mostra a chave, que não aparece de novo
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
		p.Nova = comandos(r, u.Name, nome, chave)
	}
	render(w, status, "mcp", p)
}

// comandos monta o que a pessoa cola na máquina; a base vem da própria requisição.
func comandos(r *http.Request, usuario, nome, chave string) *novaChave {
	scheme := "http"
	if isHTTPS(r) {
		scheme = "https"
	}
	base := scheme + "://" + r.Host + "/" + usuario + "/_mcp"
	return &novaChave{
		Nome:   nome,
		Chave:  chave,
		Claude: `claude mcp add --scope user --transport http pad ` + base + ` --header "Authorization: Bearer ` + chave + `"`,
		Codex: "# ~/.codex/config.toml\n[mcp_servers.pad]\nurl = \"" + base + "\"\nbearer_token_env_var = \"PAD_MCP_CHAVE\"\ndefault_tools_approval_mode = \"approve\"\n" +
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
	http.Redirect(w, r, "/"+u.Name+"/_mcp", http.StatusSeeOther)
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

// novaConversa cria a conversa com a primeira mensagem da pessoa; erro volta no painel.
func (a *app) novaConversa(w http.ResponseWriter, r *http.Request, u user) {
	nome, _ := curto(r.FormValue("nome"))
	err := a.criarConversa(u.ID, nome, humana(u, r.FormValue("mensagem")))
	if err == nil {
		http.Redirect(w, r, "/"+u.Name+"/_mcp/c/"+nome, http.StatusSeeOther)
		return
	}
	if !errors.Is(err, errNomeInvalido) && !errors.Is(err, errConversaExiste) && !errors.Is(err, errMsgVazia) && !errors.Is(err, errMsgGrande) {
		serverError(w, err)
		return
	}
	p, perr := a.painelMCP(u)
	if perr != nil {
		serverError(w, perr)
		return
	}
	p.Err = err.Error()
	render(w, http.StatusBadRequest, "mcp", p)
}

func humana(u user, texto string) mensagem {
	return mensagem{Autor: u.Name, Rotulo: u.Name, Humano: true, Texto: texto}
}

// conversaDaRota acha a conversa de {nome}; nome inválido e conversa de outra conta dão o mesmo 404.
func (a *app) conversaDaRota(w http.ResponseWriter, r *http.Request, u user) (string, bool) {
	nome, ok := curto(r.PathValue("nome"))
	if ok {
		_, err := conversaID(a.db, u.ID, nome)
		if err == nil {
			return nome, true
		}
		if !errors.Is(err, errSemConversa) {
			serverError(w, err)
			return "", false
		}
	}
	fail(w, http.StatusNotFound, "conversa não encontrada")
	return "", false
}

func (a *app) chat(w http.ResponseWriter, r *http.Request, u user) {
	nome, ok := a.conversaDaRota(w, r, u)
	if !ok {
		return
	}
	render(w, http.StatusOK, "mcp-conversa", struct {
		Me, Nome string
		Admin    bool
	}{u.Name, nome, u.Admin})
}

// esperaWeb é var para os testes baixarem; fica abaixo dos 60 s que proxies costumam cortar.
var esperaWeb = 25 * time.Second

type msgJSON struct {
	ID     int64  `json:"id"`
	Rotulo string `json:"rotulo"`
	Humano bool   `json:"humano"`
	Em     int64  `json:"em"`
	Texto  string `json:"texto"`
}

// paginaChat é quantas mensagens o chat traz por vez ao abrir e a cada "mensagens anteriores".
var paginaChat = 50

// chatMsgs devolve as mensagens depois de ?depois=; com ?esperar=1 e nada novo, segura até chegar algo.
// Ao abrir (depois=0 sem esperar) e com ?antes= vem só uma página, com "mais" dizendo se há anteriores.
func (a *app) chatMsgs(w http.ResponseWriter, r *http.Request, u user) {
	nome, ok := a.conversaDaRota(w, r, u)
	if !ok {
		return
	}
	q := r.URL.Query()
	depois, _ := strconv.ParseInt(q.Get("depois"), 10, 64)
	antes, _ := strconv.ParseInt(q.Get("antes"), 10, 64)
	m := a.mcp
	sinal := m.sinal() // antes da consulta: mensagem que chega no meio ainda acorda a espera
	var ms []mensagem
	var mais bool
	var err error
	if antes > 0 || (depois == 0 && q.Get("esperar") != "1") {
		ms, mais, err = a.mensagensAntes(u.ID, nome, antes, paginaChat)
	} else {
		ms, err = a.mensagens(u.ID, nome, depois)
	}
	if err == nil && len(ms) == 0 && antes == 0 && q.Get("esperar") == "1" {
		timer := time.NewTimer(esperaWeb)
		defer timer.Stop()
		select {
		case <-sinal:
		case <-timer.C:
		case <-r.Context().Done():
		case <-m.fim:
		}
		ms, err = a.mensagens(u.ID, nome, depois)
	}
	if errors.Is(err, errSemConversa) { // apagada durante a espera
		fail(w, http.StatusNotFound, "conversa não encontrada")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	// a pessoa viu: o "N novas" dela no painel zera; as anteriores (?antes=) não mudam o que é novo
	if len(ms) > 0 && antes == 0 {
		if err := a.lidoPor(u.ID, nome, u.Name); err != nil {
			log.Print(err)
		}
	}
	out := struct {
		Mensagens []msgJSON `json:"mensagens"`
		Agentes   []string  `json:"agentes"`
		Mais      bool      `json:"mais"`
	}{[]msgJSON{}, []string{}, mais}
	for _, x := range ms {
		out.Mensagens = append(out.Mensagens, msgJSON{x.ID, x.Rotulo, x.Humano, x.Em.Unix(), x.Texto})
	}
	out.Agentes = append(out.Agentes, m.agentesNa(u.ID, nome, time.Now())...)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(out)
}

// chatEnviar grava a mensagem da pessoa. O corpo aceita o dobro do limite porque o JSON escapa quebras de linha e aspas.
func (a *app) chatEnviar(w http.ResponseWriter, r *http.Request, u user) {
	nome, ok := a.conversaDaRota(w, r, u)
	if !ok {
		return
	}
	var p struct{ Texto string }
	r.Body = http.MaxBytesReader(w, r.Body, 2*maxMensagem+1024)
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		var grande *http.MaxBytesError
		if errors.As(err, &grande) {
			http.Error(w, errMsgGrande.Error(), http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "corpo inválido", http.StatusBadRequest)
		}
		return
	}
	_, err := a.enviarMensagem(u.ID, nome, humana(u, p.Texto))
	switch {
	case errors.Is(err, errMsgGrande):
		http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
	case errors.Is(err, errMsgVazia):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, errSemConversa): // apagada entre a abertura da página e o envio
		http.Error(w, err.Error(), http.StatusNotFound)
	case err != nil:
		log.Print(err)
		http.Error(w, "erro interno", http.StatusInternalServerError)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (a *app) chatApagar(w http.ResponseWriter, r *http.Request, u user) {
	nome, ok := a.conversaDaRota(w, r, u)
	if !ok {
		return
	}
	if err := a.apagarConversa(u.ID, nome); err != nil && !errors.Is(err, errSemConversa) {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/"+u.Name+"/_mcp", http.StatusSeeOther)
}
