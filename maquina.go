package main

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// maxCurto é o tamanho máximo dos nomes de conversa, apelido e máquina.
const maxCurto = 60

type maquina struct {
	ID            int64
	Nome          string
	Criada, Vista time.Time
}

var (
	validCurto       = regexp.MustCompile(fmt.Sprintf(`^[a-z0-9][a-z0-9_-]{0,%d}$`, maxCurto-1))
	errMaquinaExiste = falha("já existe uma máquina com esse nome")
)

// curto normaliza nomes de conversa, apelido e máquina e diz se são válidos.
func curto(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	return s, validCurto.MatchString(s)
}

// criarMaquina devolve a chave, que só aparece aqui: o banco guarda apenas o hash.
func (a *app) criarMaquina(uid int64, nome string) (string, error) {
	nome, ok := curto(nome)
	if !ok {
		return "", errNomeInvalido
	}
	chave := "pad_" + rand.Text()
	hash := sha256.Sum256([]byte(chave))
	res, err := a.db.Exec(`INSERT INTO mcp_maquinas (user_id, nome, hash, criada) VALUES (?, ?, ?, ?)
		ON CONFLICT (user_id, nome) DO NOTHING`, uid, nome, hash[:], time.Now().Unix())
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", errMaquinaExiste
	}
	return chave, nil
}

func (a *app) maquinaDaChave(chave string) (m maquina, u user, ok bool, err error) {
	hash := sha256.Sum256([]byte(chave))
	var criada, vista int64
	err = a.db.QueryRow(`SELECT m.id, m.nome, m.criada, m.vista, u.id, u.name, u.admin
		FROM mcp_maquinas m JOIN users u ON u.id = m.user_id WHERE m.hash = ?`, hash[:]).
		Scan(&m.ID, &m.Nome, &criada, &vista, &u.ID, &u.Name, &u.Admin)
	if errors.Is(err, sql.ErrNoRows) {
		return m, u, false, nil
	}
	m.Criada, m.Vista = time.Unix(criada, 0), time.Unix(vista, 0)
	return m, u, err == nil, err
}

func (a *app) listarMaquinas(uid int64) ([]maquina, error) {
	rows, err := a.db.Query(`SELECT id, nome, criada, vista FROM mcp_maquinas WHERE user_id = ? ORDER BY nome`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ms []maquina
	for rows.Next() {
		var m maquina
		var criada, vista int64
		if err := rows.Scan(&m.ID, &m.Nome, &criada, &vista); err != nil {
			return nil, err
		}
		m.Criada, m.Vista = time.Unix(criada, 0), time.Unix(vista, 0)
		ms = append(ms, m)
	}
	return ms, rows.Err()
}

func (a *app) revogarMaquina(uid, id int64) error {
	res, err := a.db.Exec(`DELETE FROM mcp_maquinas WHERE id = ? AND user_id = ?`, id, uid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		a.mcp.derrubar(func(s *sessao) bool { return s.maquinaID == id })
	}
	return nil
}

// marcarVista grava no máximo uma vez por minuto, para não escrever a cada chamada.
func (a *app) marcarVista(id int64, now time.Time) error {
	_, err := a.db.Exec(`UPDATE mcp_maquinas SET vista = ? WHERE id = ? AND vista < ?`,
		now.Unix(), id, now.Add(-time.Minute).Unix())
	return err
}
