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
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// apagarTudo atende o pedido de eliminação (LGPD, art. 18, VI): a conta, os documentos dela e, em cascata,
// sessões, compartilhamentos, participação em grupos, convites gerados, máquinas e conversas. Ficam os grupos,
// os documentos dos outros e o que a pessoa escreveu em /publico, que não guarda autor. Devolve quantos documentos saíram.
func (a *app) apagarTudo(uid int64, nome string) (int, error) {
	// o nome vira caminho no RemoveAll: uma conta "publico" de antes da regra levaria a área pública junto
	if !validUserName(nome) {
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
	// documento novo só nasce em /<nome>/... (view), então todos moram em docs/<nome>/. A conta já saiu: uma falha
	// aqui não tem como ser repetida pela tela nem pelo CLI, então fica no log para a limpeza à mão.
	pasta := filepath.Join(a.docsDir, nome)
	if err := os.RemoveAll(pasta); err != nil {
		log.Printf("apagar-tudo: a conta saiu, mas sobrou %s (apague à mão): %v", pasta, err)
	}
	return len(docs), nil
}

type apagarContaPage struct {
	Me                        string
	Docs, Maquinas, Conversas int
	Err                       string
}

func (a *app) apagarContaTela(w http.ResponseWriter, status int, u user, msg string) {
	p := apagarContaPage{Me: u.Name, Err: msg}
	if err := a.db.QueryRow(`SELECT (SELECT count(*) FROM docs WHERE owner_id = ?1),
		(SELECT count(*) FROM mcp_maquinas WHERE user_id = ?1), (SELECT count(*) FROM mcp_conversas WHERE user_id = ?1)`,
		u.ID).Scan(&p.Docs, &p.Maquinas, &p.Conversas); err != nil {
		serverError(w, err)
		return
	}
	render(w, status, "apagar-conta", p)
}

// apagarConta é a exclusão pelo próprio titular, confirmada com a senha.
func (a *app) apagarConta(w http.ResponseWriter, r *http.Request, u user) {
	if r.Method == http.MethodGet {
		a.apagarContaTela(w, http.StatusOK, u, "")
		return
	}
	// tamanho e prazo como no login: a senha passa pela mesma fila do bcrypt
	http.NewResponseController(w).SetReadDeadline(time.Now().Add(prazoLogin))
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	ok, err := a.confereSenha(r, u, r.FormValue("pass"))
	switch {
	case errors.Is(err, errTentativas):
		a.apagarContaTela(w, http.StatusTooManyRequests, u, err.Error())
		return
	case err != nil:
		serverError(w, err)
		return
	case !ok:
		a.apagarContaTela(w, http.StatusUnauthorized, u, "senha incorreta")
		return
	}
	if _, err := a.apagarTudo(u.ID, u.Name); err != nil {
		serverError(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "sid", Path: "/", MaxAge: -1})
	render(w, http.StatusOK, "msg", msgPage{Msg: "Sua conta e seus documentos foram apagados. As cópias de segurança são trocadas a cada atualização do pad: seus dados somem delas depois das próximas 7."})
}

var errTentativas = errors.New("tentativas demais, espere alguns minutos")

// confereSenha usa a fila do bcrypt e os limites do login (senhaBloqueada): a tela de apagar não vira atalho
// para adivinhar a senha de quem deixou a sessão aberta.
func (a *app) confereSenha(r *http.Request, u user, pass string) (bool, error) {
	ip := clientIP(r)
	if a.senhaBloqueada(ip, u.Name, time.Now()) {
		return false, errTentativas
	}
	var hash []byte
	if err := a.db.QueryRow(`SELECT hash FROM users WHERE id = ?`, u.ID).Scan(&hash); err != nil {
		return false, err
	}
	select {
	case a.vagasBcrypt <- struct{}{}:
	case <-r.Context().Done():
		return false, r.Context().Err()
	case <-time.After(10 * time.Second):
		return false, errors.New("servidor ocupado, tente de novo")
	}
	// de novo com a vaga: numa rajada, todos passam pela primeira conferência antes de o primeiro erro contar
	if a.senhaBloqueada(ip, u.Name, time.Now()) {
		<-a.vagasBcrypt
		return false, errTentativas
	}
	errSenha := bcrypt.CompareHashAndPassword(hash, []byte(pass))
	<-a.vagasBcrypt
	if errSenha != nil {
		a.senhaErrada(ip, u.Name, time.Now())
		return false, nil
	}
	return true, nil
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
func apagarTudoCLI(a *app, dataDir, nome string, stdin io.Reader) error {
	trava, err := travaDados(dataDir, false)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return errors.New("o pad está no ar: pare o serviço (docker compose stop) e rode com docker compose run --rm")
	}
	if err != nil {
		return err
	}
	defer trava.Close() // fechar solta o flock; segurá-lo até o fim impede o servidor de subir no meio
	var uid int64
	var docs, maquinas, conversas int
	err = a.db.QueryRow(`SELECT id, (SELECT count(*) FROM docs WHERE owner_id = u.id),
		(SELECT count(*) FROM mcp_maquinas WHERE user_id = u.id), (SELECT count(*) FROM mcp_conversas WHERE user_id = u.id)
		FROM users u WHERE name = ?`, nome).Scan(&uid, &docs, &maquinas, &conversas)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("usuário %q não existe", nome)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "apaga %s com %d documentos, %d máquinas e %d conversas, sem volta.\ndigite o nome de novo para confirmar: ",
		nome, docs, maquinas, conversas)
	linha, _ := bufio.NewReader(stdin).ReadString('\n')
	if strings.TrimSpace(linha) != nome {
		return errors.New("nome diferente: nada foi apagado")
	}
	n, err := a.apagarTudo(uid, nome)
	if err != nil {
		return err
	}
	fmt.Printf("%s apagado com %d documentos. Os 7 backups em ~/pad-backups ainda têm os dados até saírem da rotação.\n", nome, n)
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
		if com != "" { // sem compartilhamento fica o [] do começo
			d.Com = strings.Split(com, "\n")
		}
		c.Documentos = append(c.Documentos, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, l := range []*[]string{&c.Grupos, &c.CompartilhadosComigo} {
		if *l == nil {
			*l = []string{}
		}
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
