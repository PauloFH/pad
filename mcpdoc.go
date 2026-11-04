package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
)

// Documentos pelo MCP. A escrita passa pelo liveDoc como uma op de aba: o agente
// manda o texto novo, e a diferença dele para o texto lido é transformada pelo que
// as pessoas digitaram depois, em vez de cobrir o documento inteiro.

// leitura é o que o agente leu de um documento, a base da próxima escrita dele.
type leitura struct {
	doc    *liveDoc
	rev    int
	texto  []uint16
	versao string
}

var (
	errMudou     = falha("documento mudou desde a sua leitura, leia de novo")
	errSemAcesso = falha("documento não encontrado ou sem acesso")
	errNaoExiste = falha("documento não existe; para criar, use escrever_documento com versao vazia")
)

func versaoDe(rev int, texto []uint16) string {
	h := sha256.Sum256([]byte(stringOf(texto)))
	return fmt.Sprintf("%d-%x", rev, h[:6])
}

// caminhoDoc aceita /ana/notas, ana/notas, @pad /ana/notas, o endereço completo e pad://ana/notas.
func caminhoDoc(s string) (string, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimSpace(strings.TrimPrefix(s, "@pad "))
	if r, ok := strings.CutPrefix(s, "pad://"); ok {
		s = r
	} else if u, err := url.Parse(s); err == nil && u.Host != "" {
		s = u.Path
	}
	s = strings.TrimPrefix(s, "/")
	if len(s) > 200 || !validName.MatchString(s) || strings.Count(s, "/") > maxDepth {
		return "", false
	}
	return s, true
}

// diffOp troca o miolo diferente e mantém o prefixo e o sufixo comuns, sem
// separar as metades de um par substituto.
func diffOp(de, para []uint16) Op {
	p := 0
	for p < len(de) && p < len(para) && de[p] == para[p] {
		p++
	}
	if p > 0 && isHigh(de[p-1]) {
		p--
	}
	s := 0
	for s < len(de)-p && s < len(para)-p && de[len(de)-1-s] == para[len(para)-1-s] {
		s++
	}
	if s > 0 && isLow(de[len(de)-s]) {
		s--
	}
	var b opBuilder
	b.add(Comp{Retain: p})
	b.add(Comp{Delete: len(de) - p - s})
	b.add(Comp{Insert: stringOf(para[p : len(para)-s])})
	b.add(Comp{Retain: s})
	return Op(b)
}

// podeLer diz se o agente lê o documento; o que não existe não é aberto no hub.
func (a *app) podeLer(s *sessao, nome string) error {
	if isPublico(nome) {
		// como o view: no /publico, arquivo vazio é documento que não existe
		body, err := a.readDoc(nome)
		if err != nil {
			return err
		}
		if body == "" && !a.hub.connected(nome) {
			return errNaoExiste
		}
		return nil
	}
	exists, allowed, _, public, err := a.access(nome, s.uid)
	switch {
	case err != nil:
		return err
	case allowed || public:
		return nil
	case !exists && strings.HasPrefix(nome, s.login+"/"):
		return errNaoExiste
	}
	return errSemAcesso // mesma resposta para privado e inexistente dos outros
}

// abrirLeitura tira o retrato do documento no hub.
func (a *app) abrirLeitura(s *sessao, nome string) (leitura, error) {
	if err := a.podeLer(s, nome); err != nil {
		return leitura{}, err
	}
	d, err := a.hub.open(nome)
	if err != nil {
		return leitura{}, err
	}
	defer a.hub.leave(d, nil)
	d.mu.Lock()
	l := leitura{doc: d, rev: d.rev, texto: d.text} // d.text nunca muda no lugar: o aplicar troca a fatia
	d.mu.Unlock()
	l.versao = versaoDe(l.rev, l.texto)
	return l, nil
}

func (a *app) lerDocumento(s *sessao, caminho string) (texto, versao string, err error) {
	nome, ok := caminhoDoc(caminho)
	if !ok {
		return "", "", falha("caminho de documento inválido")
	}
	l, err := a.abrirLeitura(s, nome)
	if err != nil {
		return "", "", err
	}
	a.mcp.guardarLeitura(s, nome, l)
	return stringOf(l.texto), l.versao, nil
}

// escreverDocumento devolve a leitura nova e se outros editaram junto (o texto final não é o enviado).
func (a *app) escreverDocumento(s *sessao, caminho, texto, versao string) (nova leitura, juntou bool, err error) {
	nome, ok := caminhoDoc(caminho)
	if !ok {
		return nova, false, falha("caminho de documento inválido")
	}
	criar := false
	if !isPublico(nome) {
		exists, allowed, _, _, err := a.access(nome, s.uid)
		if err != nil {
			return nova, false, err
		}
		// como o save: só se cria dentro do próprio usuário
		criar = !exists && versao == "" && strings.HasPrefix(nome, s.login+"/")
		if !allowed && !criar {
			return nova, false, errSemAcesso
		}
	}
	var base leitura // versão vazia: parte do texto vazio
	if versao != "" {
		l, ok := a.mcp.leitura(s, nome)
		if !ok || l.versao != versao {
			return nova, false, errMudou
		}
		base = l
	}
	// o textarea normaliza as quebras; sem isso os comprimentos divergem nas abas
	texto = strings.ReplaceAll(strings.ReplaceAll(texto, "\r\n", "\n"), "\r", "\n")
	novo := utf16Of(texto)
	op := diffOp(base.texto, novo)

	d, err := a.hub.open(nome)
	if err != nil {
		return nova, false, err
	}
	defer a.hub.leave(d, nil)
	if criar {
		// como no ws.go: depois do open, sob writeMu, e só se um del não descartou o documento
		a.writeMu.Lock()
		d.mu.Lock()
		gone := d.gone != (closeReq{})
		d.mu.Unlock()
		if !gone {
			_, err = a.db.Exec(`INSERT INTO docs (name, owner_id, updated) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`,
				nome, s.uid, time.Now().Unix())
		}
		a.writeMu.Unlock()
		if err != nil {
			return nova, false, err
		}
	}

	d.mu.Lock()
	rev := d.rev
	switch {
	case d.gone != (closeReq{}):
		err = falha("documento apagado; leia de novo")
	case base.doc == d && base.rev >= d.rev-len(d.log):
		rev = base.rev // o log ainda tem o que veio depois da leitura: junta
	case !slices.Equal(d.text, base.texto):
		err = errMudou
	}
	// op que não muda nada não vira revisão nem mensagem às abas
	if noop := len(op) == 0 || len(op) == 1 && op[0].Retain > 0; err == nil && !noop {
		if op, err = d.aplicar(nil, rev, op); err == nil {
			out, _ := json.Marshal(map[string]any{"t": "op", "rev": d.rev, "op": op, "by": ""})
			for q := range d.peers {
				d.enqueue(q, out)
			}
			a.hub.armFlush(d)
		}
	}
	nova = leitura{doc: d, rev: d.rev, texto: d.text}
	d.mu.Unlock()
	if err != nil {
		return leitura{}, false, err
	}
	nova.versao = versaoDe(nova.rev, nova.texto)
	// a base guardada é o texto que o agente conhece: se outros editaram junto, a resposta leva o texto atual
	a.mcp.guardarLeitura(s, nome, nova)
	return nova, !slices.Equal(nova.texto, novo), nil
}

func (m *mcpState) leitura(s *sessao, nome string) (leitura, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := s.leituras[nome]
	return l, ok
}

// guardarLeitura fica só com a última leitura de cada documento.
func (m *mcpState) guardarLeitura(s *sessao, nome string, l leitura) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s.leituras[nome] = l
}

func (c *chamada) lerDocumento(raw json.RawMessage) (string, error) {
	var p struct {
		Caminho string `json:"caminho"`
	}
	if err := lerArgs(raw, &p); err != nil {
		return "", err
	}
	texto, versao, err := c.a.lerDocumento(c.s, p.Caminho)
	if err != nil {
		return "", err
	}
	return "versao: " + versao + "\n\n" + texto, nil
}

func (c *chamada) escreverDocumento(raw json.RawMessage) (string, error) {
	var p struct {
		Caminho string `json:"caminho"`
		Texto   string `json:"texto"`
		Versao  string `json:"versao"`
	}
	if err := lerArgs(raw, &p); err != nil {
		return "", err
	}
	nova, juntou, err := c.a.escreverDocumento(c.s, p.Caminho, p.Texto, p.Versao)
	if err != nil {
		return "", err
	}
	out := "gravado (versao: " + nova.versao + ")"
	if juntou {
		out += "; outros editaram junto, o texto agora é:\n\n" + stringOf(nova.texto)
	}
	return out, nil
}

// --- resources: os documentos no @ do terminal ---

func (a *app) mcpRecursos(s *sessao) (any, *rpcErro) {
	docs, err := a.userDocs(user{ID: s.uid, Name: s.login})
	if err != nil {
		return nil, &rpcErro{-32603, paraAgente(err)}
	}
	docs = docs[:min(len(docs), 1000)] // shortcut: lista até 1000; paginar se alguém passar disso
	out := make([]map[string]any, len(docs))
	for i, d := range docs {
		out[i] = map[string]any{"uri": "pad://" + d.Name, "name": "/" + d.Name, "mimeType": "text/plain"}
	}
	return map[string]any{"resources": out}, nil
}

func (a *app) mcpLerRecurso(s *sessao, params json.RawMessage) (any, *rpcErro) {
	var p struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcErro{-32602, "parâmetros inválidos"}
	}
	nome, ok := caminhoDoc(p.URI)
	if !ok {
		return nil, &rpcErro{-32602, errSemAcesso.Error()}
	}
	l, err := a.abrirLeitura(s, nome)
	switch {
	case err == errSemAcesso || err == errNaoExiste:
		return nil, &rpcErro{-32602, errSemAcesso.Error()}
	case err != nil:
		return nil, &rpcErro{-32603, paraAgente(err)}
	}
	return map[string]any{"contents": []map[string]any{
		{"uri": "pad://" + nome, "mimeType": "text/plain", "text": stringOf(l.texto)}}}, nil
}
