package main

import (
	"archive/zip"
	"bufio"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// backupsGuardados é o MANTER do deploy.sh: a política de privacidade e o apagar conta prometem esse número.
const backupsGuardados = 7

// apagarTudo atende o pedido de eliminação (LGPD, art. 18, VI): a conta, os documentos dela e, em cascata,
// sessões, compartilhamentos, participação em grupos, convites gerados, máquinas e conversas. Ficam os grupos,
// os documentos dos outros e o que a pessoa escreveu em /publico, que não guarda autor. Devolve quantos documentos saíram.
func (a *app) apagarTudo(uid int64, nome string) (int, error) {
	// o nome vira caminho no RemoveAll: uma conta "publico" de antes da regra levaria a área pública junto
	if checkUserName(nome) != nil {
		return 0, fmt.Errorf("nome de conta %q fora do formato: apague à mão", nome)
	}
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	docs, err := names(a.db, `SELECT name FROM docs WHERE owner_id = ?`, uid)
	if err != nil {
		return 0, err
	}
	// o banco primeiro: sem a sessão e sem a linha em docs, ninguém reabre os documentos depois do kill
	tx, err := a.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM docs WHERE owner_id = ?`, uid); err != nil {
		return 0, err
	}
	if res, err := tx.Exec(`DELETE FROM users WHERE id = ?`, uid); err != nil {
		return 0, err
	} else if n, _ := res.RowsAffected(); n != 1 {
		return 0, errors.New("usuário não encontrado")
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	// as abas dele em documentos dos outros caem sozinhas no recheckLoop, que não acha mais a sessão
	for _, d := range docs {
		a.hub.kill(d, "documento apagado")
	}
	a.mcp.derrubar(func(s *sessao) bool { return s.u.ID == uid })
	// documento novo só nasce em /<nome>/... (view), então todos moram em docs/<nome>/. A conta já saiu: uma falha
	// aqui não tem como ser repetida pela tela nem pelo CLI, então fica no log para a limpeza à mão.
	pasta := filepath.Join(a.docsDir, nome)
	if err := os.RemoveAll(pasta); err != nil {
		log.Printf("apagar-tudo: a conta saiu, mas sobrou %s (apague à mão): %v", pasta, err)
	}
	return len(docs), nil
}

// doConta é o que o apagarTudo leva; a tela e o CLI mostram antes de confirmar.
type doConta struct{ Docs, Maquinas, Conversas int }

func (a *app) contarDaConta(uid int64) (c doConta, err error) {
	err = a.db.QueryRow(`SELECT (SELECT count(*) FROM docs WHERE owner_id = ?1),
		(SELECT count(*) FROM mcp_maquinas WHERE user_id = ?1), (SELECT count(*) FROM mcp_conversas WHERE user_id = ?1)`,
		uid).Scan(&c.Docs, &c.Maquinas, &c.Conversas)
	return c, err
}

type apagarContaPage struct {
	Me string
	doConta
	Err string
}

func (a *app) apagarContaTela(w http.ResponseWriter, status int, u user, msg string) {
	c, err := a.contarDaConta(u.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	render(w, status, "apagar-conta", apagarContaPage{u.Name, c, msg})
}

// apagarConta é a exclusão pelo próprio titular, confirmada com a senha.
func (a *app) apagarConta(w http.ResponseWriter, r *http.Request, u user) {
	if r.Method == http.MethodGet {
		a.apagarContaTela(w, http.StatusOK, u, "")
		return
	}
	formularioDeSenha(w, r)
	var hash []byte
	if err := a.db.QueryRow(`SELECT hash FROM users WHERE id = ?`, u.ID).Scan(&hash); err != nil {
		serverError(w, err)
		return
	}
	err := a.confereSenha(r, u.Name, hash, r.FormValue("pass"))
	switch {
	case errors.Is(err, errTentativas):
		a.apagarContaTela(w, http.StatusTooManyRequests, u, err.Error())
		return
	case errors.Is(err, errOcupado):
		a.apagarContaTela(w, http.StatusServiceUnavailable, u, err.Error())
		return
	case errors.Is(err, errSenha):
		a.apagarContaTela(w, http.StatusUnauthorized, u, err.Error())
		return
	case err != nil: // a conexão caiu na fila: ninguém lê a resposta
		return
	}
	if _, err := a.apagarTudo(u.ID, u.Name); err != nil {
		serverError(w, err)
		return
	}
	apagarCookieSessao(w)
	render(w, http.StatusOK, "msg", msgPage{Msg: "Sua conta e seus documentos foram apagados. As cópias de segurança são trocadas a cada atualização do pad: seus dados somem delas depois das próximas " + strconv.Itoa(backupsGuardados) + "."})
}

// travaDados segura o pad.lock da pasta de dados. O servidor o segura enquanto roda (espera=true: um servidor novo
// aguarda o anterior sair); o apagar-tudo tenta sem esperar. O flock vale entre contêineres com o mesmo volume,
// que é o caso do docker compose run ao lado do serviço.
func travaDados(dir string, espera bool) (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(dir, "pad.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	modo := syscall.LOCK_EX
	if !espera {
		modo |= syscall.LOCK_NB
	}
	if err := syscall.Flock(int(f.Fd()), modo); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// apagarTudoCLI é o caminho do pedido por e-mail. O servidor no ar guarda documentos abertos na memória e não
// veria a exclusão feita por outro processo, então aqui o pad tem de estar parado.
func apagarTudoCLI(a *app, nome string, stdin io.Reader) error {
	trava, err := travaDados(a.dataDir, false)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return errors.New("o pad está no ar: pare o serviço (docker compose stop) e rode com docker compose run --rm")
	}
	if err != nil {
		return err
	}
	defer trava.Close() // fechar solta o flock; segurá-lo até o fim impede o servidor de subir no meio
	var uid int64
	err = a.db.QueryRow(`SELECT id FROM users WHERE name = ?`, nome).Scan(&uid)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("usuário %q não existe", nome)
	}
	if err != nil {
		return err
	}
	c, err := a.contarDaConta(uid)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "apaga %s com %d documentos, %d máquinas e %d conversas, sem volta.\ndigite o nome de novo para confirmar: ",
		nome, c.Docs, c.Maquinas, c.Conversas)
	linha, _ := bufio.NewReader(stdin).ReadString('\n')
	if strings.TrimSpace(linha) != nome {
		return errors.New("nome diferente: nada foi apagado")
	}
	n, err := a.apagarTudo(uid, nome)
	if err != nil {
		return err
	}
	fmt.Printf("%s apagado com %d documentos. Os %d backups em ~/pad-backups ainda têm os dados até saírem da rotação.\n", nome, n, backupsGuardados)
	return nil
}

// exportar entrega num .zip tudo o que é da conta (LGPD, art. 18, II e V): o texto de cada documento,
// as conversas com as mensagens e um conta.json com grupos, compartilhamentos e máquinas (sem o hash da chave).
// Dos documentos dos outros vai só o nome: o texto é de quem o escreveu.
func (a *app) exportar(w http.ResponseWriter, r *http.Request, u user) {
	docs, err := names(a.db, `SELECT name FROM docs WHERE owner_id = ? ORDER BY name`, u.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	conta, err := a.contaJSON(u)
	if err != nil {
		serverError(w, err)
		return
	}
	conversas, err := a.conversasJSON(u.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="pad-%s-%s.zip"`, u.Name, time.Now().Format("2006-01-02")))
	z := zip.NewWriter(w)
	falhou := func(err error) { log.Print("exportar: ", err) } // o 200 já saiu: o zip fica truncado e o navegador acusa
	for _, d := range docs {
		// o texto aberto ao vivo pode estar só na memória: grava antes, para o zip levar o que está na tela
		if err := a.hub.flushName(d); err != nil {
			falhou(err)
			return
		}
		body, err := a.readDoc(d)
		if err != nil {
			falhou(err)
			return
		}
		f, err := z.Create("documentos/" + strings.TrimPrefix(d, u.Name+"/") + ".txt")
		if err == nil {
			_, err = io.WriteString(f, body)
		}
		if err != nil {
			falhou(err)
			return
		}
	}
	for nome, v := range map[string]any{"conta.json": conta, "conversas.json": conversas} {
		f, err := z.Create(nome)
		if err == nil {
			enc := json.NewEncoder(f)
			enc.SetIndent("", "  ")
			err = enc.Encode(v)
		}
		if err != nil {
			falhou(err)
			return
		}
	}
	if err := z.Close(); err != nil {
		falhou(err)
	}
}

func quandoISO(s int64) string {
	if s == 0 {
		return ""
	}
	return time.Unix(s, 0).UTC().Format(time.RFC3339)
}

func (a *app) contaJSON(u user) (any, error) {
	type maquina struct{ Nome, Cadastrada, VistaPorUltimo string }
	type compartilhamento struct {
		Documento string
		Com       []string // "usuário x" ou "grupo y"
		Publico   bool
	}
	c := struct {
		Usuario, ExportadoEm string
		Administrador        bool
		Grupos               []string
		Documentos           []compartilhamento
		CompartilhadosComigo []string
		Maquinas             []maquina
	}{Usuario: u.Name, Administrador: u.Admin, ExportadoEm: quandoISO(time.Now().Unix()),
		// lista vazia sai [] e não null: o arquivo é para pessoas lerem
		Documentos: []compartilhamento{}, Maquinas: []maquina{}}
	var err error
	if c.Grupos, err = names(a.db, `SELECT g.name FROM groups g JOIN members m ON m.group_id = g.id WHERE m.user_id = ? ORDER BY 1`, u.ID); err != nil {
		return nil, err
	}
	if c.CompartilhadosComigo, err = names(a.db, `SELECT doc FROM user_shares WHERE user_id = ?1
		UNION SELECT s.doc FROM group_shares s JOIN members m ON m.group_id = s.group_id WHERE m.user_id = ?1 ORDER BY 1`, u.ID); err != nil {
		return nil, err
	}
	rows, err := a.db.Query(`SELECT d.name, d.public, coalesce((SELECT group_concat(x, char(10)) FROM (
			SELECT 'usuário ' || u.name AS x FROM user_shares s JOIN users u ON u.id = s.user_id WHERE s.doc = d.name
			UNION ALL SELECT 'grupo ' || g.name FROM group_shares s JOIN groups g ON g.id = s.group_id WHERE s.doc = d.name)), '')
		FROM docs d WHERE d.owner_id = ? ORDER BY d.name`, u.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		d := compartilhamento{Com: []string{}}
		var com string
		if err := rows.Scan(&d.Documento, &d.Publico, &com); err != nil {
			return nil, err
		}
		if com != "" {
			d.Com = strings.Split(com, "\n")
		}
		c.Documentos = append(c.Documentos, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	ms, err := a.db.Query(`SELECT nome, criada, vista FROM mcp_maquinas WHERE user_id = ? ORDER BY nome`, u.ID)
	if err != nil {
		return nil, err
	}
	defer ms.Close()
	for ms.Next() {
		var m maquina
		var criada, vista int64
		if err := ms.Scan(&m.Nome, &criada, &vista); err != nil {
			return nil, err
		}
		m.Cadastrada, m.VistaPorUltimo = quandoISO(criada), quandoISO(vista)
		c.Maquinas = append(c.Maquinas, m)
	}
	return c, ms.Err()
}

func (a *app) conversasJSON(uid int64) (any, error) {
	type mensagem struct {
		Id                       int64
		Autor, Rotulo, Em, Texto string
		Humano                   bool
	}
	type leitura struct {
		Apelido     string
		AteMensagem int64 // Id da última mensagem que o apelido já leu
	}
	type conversa struct {
		Nome, Criada string
		Mensagens    []mensagem
		Leituras     []leitura
	}
	rows, err := a.db.Query(`SELECT c.nome, c.criada, m.id, m.autor, m.rotulo, m.humano, m.em, m.texto
		FROM mcp_conversas c LEFT JOIN mcp_mensagens m ON m.conversa_id = c.id WHERE c.user_id = ? ORDER BY c.nome, m.id`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cs := []*conversa{}
	for rows.Next() {
		var nome string
		var criada int64
		var autor, rotulo, texto sql.NullString
		var humano sql.NullBool
		var id, em sql.NullInt64
		if err := rows.Scan(&nome, &criada, &id, &autor, &rotulo, &humano, &em, &texto); err != nil {
			return nil, err
		}
		if len(cs) == 0 || cs[len(cs)-1].Nome != nome {
			cs = append(cs, &conversa{Nome: nome, Criada: quandoISO(criada), Mensagens: []mensagem{}, Leituras: []leitura{}})
		}
		if autor.Valid { // conversa sem mensagem vem do LEFT JOIN com tudo nulo
			c := cs[len(cs)-1]
			c.Mensagens = append(c.Mensagens, mensagem{id.Int64, autor.String, rotulo.String, quandoISO(em.Int64), texto.String, humano.Bool})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	lidos, err := a.db.Query(`SELECT c.nome, l.apelido, l.ate FROM mcp_lido l JOIN mcp_conversas c ON c.id = l.conversa_id
		WHERE c.user_id = ? ORDER BY c.nome, l.apelido`, uid)
	if err != nil {
		return nil, err
	}
	defer lidos.Close()
	porNome := map[string]*conversa{}
	for _, c := range cs {
		porNome[c.Nome] = c
	}
	for lidos.Next() {
		var nome string
		var l leitura
		if err := lidos.Scan(&nome, &l.Apelido, &l.AteMensagem); err != nil {
			return nil, err
		}
		if c := porNome[nome]; c != nil {
			c.Leituras = append(c.Leituras, l)
		}
	}
	return cs, lidos.Err()
}
