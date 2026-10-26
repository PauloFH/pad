package main

import (
	"bufio"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

const usage = `uso:
  pad                      inicia o servidor
  pad useradd NOME         cria usuário (senha pelo stdin)
  pad passwd NOME          troca a senha (senha pelo stdin) e encerra as sessões
  pad userdel NOME         apaga usuário (falha se ele ainda for dono de documentos)
  pad groupadd GRUPO       cria grupo
  pad groupdel GRUPO       apaga grupo
  pad join GRUPO NOME      põe o usuário no grupo
  pad leave GRUPO NOME     tira o usuário do grupo
  pad list                 lista usuários e seus grupos`

func runCLI(db *sql.DB, args []string, stdin io.Reader) error {
	arity := map[string]int{"useradd": 1, "passwd": 1, "userdel": 1, "groupadd": 1, "groupdel": 1, "join": 2, "leave": 2, "list": 0}
	if n, ok := arity[args[0]]; !ok || len(args)-1 != n {
		return errors.New(usage)
	}
	a := args[1:]
	switch args[0] {
	case "useradd", "passwd":
		hash, err := readPassword(stdin)
		if err != nil {
			return err
		}
		if args[0] == "useradd" {
			return exec1(db, `INSERT INTO users (name, hash) VALUES (?, ?)`, a[0], hash)
		}
		if err := exec1(db, `UPDATE users SET hash = ? WHERE name = ?`, hash, a[0]); err != nil {
			return err
		}
		_, err = db.Exec(`DELETE FROM sessions WHERE user_id = (SELECT id FROM users WHERE name = ?)`, a[0])
		return err
	case "userdel":
		return exec1(db, `DELETE FROM users WHERE name = ?`, a[0])
	case "groupadd":
		return exec1(db, `INSERT INTO groups (name) VALUES (?)`, a[0])
	case "groupdel":
		return exec1(db, `DELETE FROM groups WHERE name = ?`, a[0])
	case "join":
		return exec1(db, `INSERT OR IGNORE INTO members SELECT g.id, u.id FROM groups g, users u WHERE g.name = ? AND u.name = ?`, a[0], a[1])
	case "leave":
		return exec1(db, `DELETE FROM members WHERE group_id = (SELECT id FROM groups WHERE name = ?)
			AND user_id = (SELECT id FROM users WHERE name = ?)`, a[0], a[1])
	}
	lines, err := names(db, `SELECT u.name || COALESCE(': ' || group_concat(g.name, ', '), '') FROM users u
		LEFT JOIN members m ON m.user_id = u.id LEFT JOIN groups g ON g.id = m.group_id
		GROUP BY u.id ORDER BY u.name`)
	if err != nil {
		return err
	}
	fmt.Println(strings.Join(lines, "\n"))
	return nil
}

// exec1 exige que exatamente uma linha mude, para nome digitado errado não passar calado.
func exec1(db *sql.DB, q string, args ...any) error {
	res, err := db.Exec(q, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return errors.New("nada mudou: confira os nomes (ou o usuário já está no grupo)")
	}
	return nil
}

func readPassword(r io.Reader) ([]byte, error) {
	fmt.Fprint(os.Stderr, "senha: ")
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && line == "" {
		return nil, err
	}
	p := strings.TrimRight(line, "\r\n")
	if len(p) < 8 {
		return nil, errors.New("a senha precisa de pelo menos 8 caracteres")
	}
	return bcrypt.GenerateFromPassword([]byte(p), bcrypt.DefaultCost)
}
