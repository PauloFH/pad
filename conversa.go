package main

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"slices"
	"strings"
	"sync"
	"time"
)

const maxMensagem = 64 << 10

type mensagem struct {
	ID            int64
	Autor, Rotulo string // Autor (apelido ou login) é a chave do "outro autor"; Rotulo é o que aparece
	Humano        bool
	Em            time.Time
	Texto         string
}

type conversaRow struct {
	Nome, UltimoRotulo string
	UltimaEm           time.Time
	NaoLidas           int
}

var (
	errConversaExiste = errors.New("já existe uma conversa com esse nome")
	errSemConversa    = errors.New("conversa não encontrada")
	errMsgGrande      = errors.New("mensagem passa de 64 KB")
	errMsgVazia       = errors.New("mensagem vazia")
	errNomeInvalido   = errors.New("nome inválido (use minúsculas, números, - e _)")
)

// mcpState é o que o MCP mantém em memória entre chamadas.
type mcpState struct {
	mu       sync.Mutex
	novidade chan struct{}      // fecha quando chega mensagem nova
	sessoes  map[string]*sessao // por id
	fim      chan struct{}      // fecha no desligamento do servidor, para nenhum esperar segurá-lo
	fimOnce  sync.Once
}

func newMcpState() *mcpState {
	return &mcpState{novidade: make(chan struct{}), sessoes: map[string]*sessao{}, fim: make(chan struct{})}
}

// sinal deve ser pego antes de consultar o banco: assim uma mensagem que chega
// entre a consulta e a espera ainda acorda quem espera.
func (m *mcpState) sinal() <-chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.novidade
}

func (m *mcpState) avisar() {
	m.mu.Lock()
	defer m.mu.Unlock()
	close(m.novidade)
	m.novidade = make(chan struct{})
}

type consulta interface {
	QueryRow(query string, args ...any) *sql.Row
	Query(query string, args ...any) (*sql.Rows, error)
	Exec(query string, args ...any) (sql.Result, error)
}

// conversaID normaliza o nome e acha a conversa do usuário.
func conversaID(q consulta, uid int64, nome string) (int64, error) {
	nome, ok := curto(nome)
	if !ok {
		return 0, errNomeInvalido
	}
	var id int64
	err := q.QueryRow(`SELECT id FROM mcp_conversas WHERE user_id = ? AND nome = ?`, uid, nome).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, errSemConversa
	}
	return id, err
}

func validarMensagem(m mensagem) error {
	if strings.TrimSpace(m.Texto) == "" {
		return errMsgVazia
	}
	if len(m.Texto) > maxMensagem {
		return errMsgGrande
	}
	return nil
}

func inserirMensagem(q consulta, cid int64, m mensagem) (int64, error) {
	res, err := q.Exec(`INSERT INTO mcp_mensagens (conversa_id, autor, rotulo, humano, em, texto) VALUES (?, ?, ?, ?, ?, ?)`,
		cid, m.Autor, m.Rotulo, m.Humano, time.Now().Unix(), m.Texto)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (a *app) criarConversa(uid int64, nome string, autor mensagem) error {
	nome, ok := curto(nome)
	if !ok {
		return errNomeInvalido
	}
	if err := validarMensagem(autor); err != nil {
		return err
	}
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT INTO mcp_conversas (user_id, nome, criada) VALUES (?, ?, ?)
		ON CONFLICT (user_id, nome) DO NOTHING`, uid, nome, time.Now().Unix())
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errConversaExiste
	}
	cid, err := res.LastInsertId()
	if err != nil {
		return err
	}
	if _, err := inserirMensagem(tx, cid, autor); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	a.mcp.avisar()
	return nil
}

func (a *app) enviarMensagem(uid int64, conversa string, m mensagem) (int64, error) {
	if err := validarMensagem(m); err != nil {
		return 0, err
	}
	cid, err := conversaID(a.db, uid, conversa)
	if err != nil {
		return 0, err
	}
	id, err := inserirMensagem(a.db, cid, m)
	if err != nil {
		return 0, err
	}
	a.mcp.avisar()
	return id, nil
}

// lerMensagens devolve as mensagens com id > depois, e só as de outros autores se outrosDe não for vazio.
func lerMensagens(q consulta, cid, depois int64, outrosDe string) ([]mensagem, error) {
	return escanear(q.Query(`SELECT id, autor, rotulo, humano, em, texto FROM mcp_mensagens
		WHERE conversa_id = ? AND id > ? AND autor <> ? ORDER BY id`, cid, depois, outrosDe))
}

// mensagensAntes devolve as n mensagens anteriores a antes (0: as mais recentes), em ordem, e se há outras mais antigas.
// É a página do chat no navegador: uma conversa longa não chega inteira de uma vez.
func (a *app) mensagensAntes(uid int64, conversa string, antes int64, n int) ([]mensagem, bool, error) {
	cid, err := conversaID(a.db, uid, conversa)
	if err != nil {
		return nil, false, err
	}
	if antes <= 0 {
		antes = math.MaxInt64
	}
	ms, err := escanear(a.db.Query(`SELECT id, autor, rotulo, humano, em, texto FROM mcp_mensagens
		WHERE conversa_id = ? AND id < ? ORDER BY id DESC LIMIT ?`, cid, antes, n+1))
	mais := len(ms) > n
	if mais {
		ms = ms[:n]
	}
	slices.Reverse(ms)
	return ms, mais, err
}

func escanear(rows *sql.Rows, err error) ([]mensagem, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ms []mensagem
	for rows.Next() {
		var m mensagem
		var em int64
		if err := rows.Scan(&m.ID, &m.Autor, &m.Rotulo, &m.Humano, &em, &m.Texto); err != nil {
			return nil, err
		}
		m.Em = time.Unix(em, 0)
		ms = append(ms, m)
	}
	return ms, rows.Err()
}

// mensagens é para o navegador e só lê; o lido da pessoa o chatMsgs grava à parte, com lidoPor.
func (a *app) mensagens(uid int64, conversa string, depois int64) ([]mensagem, error) {
	cid, err := conversaID(a.db, uid, conversa)
	if err != nil {
		return nil, err
	}
	return lerMensagens(a.db, cid, depois, "")
}

// lidoPor põe em dia o lido de quem leu pelo navegador; é pelo login, que o apresentar nunca dá a um agente.
func (a *app) lidoPor(uid int64, conversa, login string) error {
	cid, err := conversaID(a.db, uid, conversa)
	if err != nil {
		return err
	}
	return marcarLido(a.db, cid, login)
}

// marcarLido grava ate = maior id da conversa.
func marcarLido(q consulta, cid int64, apelido string) error {
	_, err := q.Exec(`INSERT INTO mcp_lido (conversa_id, apelido, ate)
		VALUES (?, ?, (SELECT COALESCE(MAX(id), 0) FROM mcp_mensagens WHERE conversa_id = ?))
		ON CONFLICT (conversa_id, apelido) DO UPDATE SET ate = excluded.ate`, cid, apelido, cid)
	return err
}

// desde é o ponto a partir do qual há novidade para o apelido: o que ele já leu
// ou, se nunca leu, a última fala dele (quem acabou de entrar não recebe o histórico).
const desdeSQL = `COALESCE(
	(SELECT ate FROM mcp_lido WHERE conversa_id = c.id AND apelido = :apelido),
	(SELECT MAX(id) FROM mcp_mensagens WHERE conversa_id = c.id AND autor = :apelido), 0)`

// lerConversa e novidades amarram a transação ao ctx: numa chamada cancelada o Commit recusa e o lido não é gravado.
func (a *app) lerConversa(ctx context.Context, uid int64, conversa, apelido string, tudo bool) ([]mensagem, error) {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	cid, err := conversaID(tx, uid, conversa)
	if err != nil {
		return nil, err
	}
	var ate int64
	err = tx.QueryRow(`SELECT ate FROM mcp_lido WHERE conversa_id = ? AND apelido = ?`, cid, apelido).Scan(&ate)
	if errors.Is(err, sql.ErrNoRows) {
		tudo = true
	} else if err != nil {
		return nil, err
	}
	var ms []mensagem
	if tudo {
		ms, err = lerMensagens(tx, cid, 0, "")
	} else {
		ms, err = lerMensagens(tx, cid, ate, apelido)
	}
	if err != nil {
		return nil, err
	}
	if err := marcarLido(tx, cid, apelido); err != nil {
		return nil, err
	}
	return ms, tx.Commit()
}

func (a *app) novidades(ctx context.Context, uid int64, conversas []string, apelido string) (map[string][]mensagem, error) {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	ids := map[string]int64{} // nome -> id
	if len(conversas) == 0 {
		rows, err := tx.Query(`SELECT nome, id FROM mcp_conversas WHERE user_id = ?`, uid)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var nome string
			var id int64
			if err := rows.Scan(&nome, &id); err != nil {
				rows.Close()
				return nil, err
			}
			ids[nome] = id
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	for _, nome := range conversas {
		id, err := conversaID(tx, uid, nome)
		if err != nil {
			return nil, err
		}
		nome, _ = curto(nome)
		ids[nome] = id
	}
	out := map[string][]mensagem{}
	for nome, cid := range ids {
		var desde int64
		err := tx.QueryRow(`SELECT `+desdeSQL+` FROM mcp_conversas c WHERE c.id = :id`,
			sql.Named("apelido", apelido), sql.Named("id", cid)).Scan(&desde)
		if err != nil {
			return nil, err
		}
		ms, err := lerMensagens(tx, cid, desde, apelido)
		if err != nil {
			return nil, err
		}
		if len(ms) == 0 {
			continue
		}
		if err := marcarLido(tx, cid, apelido); err != nil {
			return nil, err
		}
		out[nome] = ms
	}
	return out, tx.Commit()
}

func (a *app) listarConversas(uid int64, apelido string) ([]conversaRow, error) {
	rows, err := a.db.Query(`SELECT c.nome, u.rotulo, u.em,
		(SELECT COUNT(*) FROM mcp_mensagens WHERE conversa_id = c.id AND autor <> :apelido AND id > `+desdeSQL+`)
		FROM mcp_conversas c
		JOIN mcp_mensagens u ON u.id = (SELECT MAX(id) FROM mcp_mensagens WHERE conversa_id = c.id)
		WHERE c.user_id = :uid ORDER BY u.id DESC`,
		sql.Named("apelido", apelido), sql.Named("uid", uid))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cs []conversaRow
	for rows.Next() {
		var c conversaRow
		var em int64
		if err := rows.Scan(&c.Nome, &c.UltimoRotulo, &em, &c.NaoLidas); err != nil {
			return nil, err
		}
		c.UltimaEm = time.Unix(em, 0)
		cs = append(cs, c)
	}
	return cs, rows.Err()
}

func (a *app) apagarConversa(uid int64, nome string) error {
	nome, ok := curto(nome)
	if !ok {
		return errNomeInvalido
	}
	res, err := a.db.Exec(`DELETE FROM mcp_conversas WHERE user_id = ? AND nome = ?`, uid, nome)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errSemConversa
	}
	return nil
}
