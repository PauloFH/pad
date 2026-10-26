package main

import (
	"crypto/rand"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

const (
	maxDoc     = 2 << 20 // 2 MiB por documento
	sessionTTL = 30 * 24 * time.Hour
)

//go:embed templates.html
var tmplFS embed.FS

var (
	tmpl = template.Must(template.ParseFS(tmplFS, "templates.html"))
	// o nome vira caminho de arquivo: sem "..", "/" inicial, "//" ou ponto
	validName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*(/[A-Za-z0-9_-]+)*$`)
	// usado quando o usuário não existe, para o login levar o mesmo tempo
	dummyHash, _ = bcrypt.GenerateFromPassword([]byte("dummy"), bcrypt.DefaultCost)
)

const schema = `
CREATE TABLE IF NOT EXISTS users (id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE, hash BLOB NOT NULL);
CREATE TABLE IF NOT EXISTS groups (id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE);
CREATE TABLE IF NOT EXISTS members (
	group_id INTEGER NOT NULL REFERENCES groups ON DELETE CASCADE,
	user_id INTEGER NOT NULL REFERENCES users ON DELETE CASCADE,
	PRIMARY KEY (group_id, user_id));
CREATE TABLE IF NOT EXISTS sessions (
	token TEXT PRIMARY KEY,
	user_id INTEGER NOT NULL REFERENCES users ON DELETE CASCADE,
	expires INTEGER NOT NULL);
-- sem CASCADE no dono: apagar usuário com documentos falha em vez de sumir com eles
CREATE TABLE IF NOT EXISTS docs (
	name TEXT PRIMARY KEY,
	owner_id INTEGER NOT NULL REFERENCES users,
	updated INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS user_shares (
	doc TEXT NOT NULL REFERENCES docs ON DELETE CASCADE,
	user_id INTEGER NOT NULL REFERENCES users ON DELETE CASCADE,
	PRIMARY KEY (doc, user_id));
CREATE TABLE IF NOT EXISTS group_shares (
	doc TEXT NOT NULL REFERENCES docs ON DELETE CASCADE,
	group_id INTEGER NOT NULL REFERENCES groups ON DELETE CASCADE,
	PRIMARY KEY (doc, group_id));
`

type app struct {
	db      *sql.DB
	docsDir string
	// serializa salvar/apagar: sem isso, um autosave em curso recria o arquivo de um doc recém-apagado
	writeMu sync.Mutex
}

type user struct {
	ID   int64
	Name string
}

func main() {
	dataDir := env("PAD_DATA", "data")
	a, err := openApp(dataDir)
	if err != nil {
		log.Fatal(err)
	}
	if len(os.Args) > 1 {
		if err := runCLI(a.db, os.Args[1:], os.Stdin); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	srv := &http.Server{Addr: env("PAD_ADDR", ":8080"), Handler: a.routes(), ReadHeaderTimeout: 10 * time.Second}
	log.Printf("pad ouvindo em %s, dados em %s", srv.Addr, dataDir)
	log.Fatal(srv.ListenAndServe())
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func openApp(dataDir string) (*app, error) {
	docsDir := filepath.Join(dataDir, "docs")
	if err := os.MkdirAll(docsDir, 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dataDir, "pad.db")+
		"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite tem um escritor só; uma conexão evita SQLITE_BUSY
	if _, err := db.Exec(schema); err != nil {
		return nil, err
	}
	return &app{db: db, docsDir: docsDir}, nil
}

func (a *app) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /_login", a.loginPage)
	mux.HandleFunc("POST /_login", a.login)
	mux.HandleFunc("POST /_logout", a.logout)
	mux.HandleFunc("GET /{$}", a.auth(a.home))
	mux.HandleFunc("GET /{name...}", a.auth(a.view))
	mux.HandleFunc("POST /{name...}", a.auth(a.save))
	mux.HandleFunc("POST /_share/{name...}", a.auth(a.share))
	mux.HandleFunc("POST /_unshare/{name...}", a.auth(a.share))
	mux.HandleFunc("POST /_delete/{name...}", a.auth(a.del))
	return http.NewCrossOriginProtection().Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'")
		mux.ServeHTTP(w, r)
	}))
}

// --- sessão ---

func (a *app) auth(h func(http.ResponseWriter, *http.Request, user)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var u user
		err := sql.ErrNoRows
		if c, cerr := r.Cookie("sid"); cerr == nil {
			err = a.db.QueryRow(`SELECT u.id, u.name FROM sessions s JOIN users u ON u.id = s.user_id
				WHERE s.token = ? AND s.expires > ?`, c.Value, time.Now().Unix()).Scan(&u.ID, &u.Name)
		}
		switch {
		case err == nil:
			h(w, r, u)
		case !errors.Is(err, sql.ErrNoRows):
			serverError(w, err)
		case r.Method == http.MethodGet:
			http.Redirect(w, r, "/_login?next="+url.QueryEscape(r.URL.Path), http.StatusSeeOther)
		default:
			http.Error(w, "sessão expirada, entre de novo", http.StatusUnauthorized)
		}
	}
}

type loginData struct{ Next, Err string }

func (a *app) loginPage(w http.ResponseWriter, r *http.Request) {
	render(w, http.StatusOK, "login", loginData{Next: r.URL.Query().Get("next")})
}

func (a *app) login(w http.ResponseWriter, r *http.Request) {
	name, pass, next := r.FormValue("name"), r.FormValue("pass"), r.FormValue("next")
	var id int64
	hash := dummyHash
	err := a.db.QueryRow(`SELECT id, hash FROM users WHERE name = ?`, name).Scan(&id, &hash)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		serverError(w, err)
		return
	}
	if bcrypt.CompareHashAndPassword(hash, []byte(pass)) != nil || err != nil {
		render(w, http.StatusUnauthorized, "login", loginData{next, "usuário ou senha incorretos"})
		return
	}
	token, now := rand.Text(), time.Now()
	if _, err := a.db.Exec(`DELETE FROM sessions WHERE expires <= ?`, now.Unix()); err != nil {
		serverError(w, err)
		return
	}
	if _, err := a.db.Exec(`INSERT INTO sessions (token, user_id, expires) VALUES (?, ?, ?)`,
		token, id, now.Add(sessionTTL).Unix()); err != nil {
		serverError(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: "sid", Value: token, Path: "/", Expires: now.Add(sessionTTL),
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
	})
	// só caminhos de documento: "//x", "/\x" ou "/\t/x" levariam para outro site
	if next != "/" && !validName.MatchString(strings.TrimPrefix(next, "/")) {
		next = "/"
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (a *app) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("sid"); err == nil {
		a.db.Exec(`DELETE FROM sessions WHERE token = ?`, c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: "sid", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/_login", http.StatusSeeOther)
}

// --- documentos ---

// access diz se o documento existe, se u pode abri-lo/editá-lo e se u é o dono.
func (a *app) access(doc string, uid int64) (exists, allowed, owner bool, err error) {
	var ownerID int64
	err = a.db.QueryRow(`SELECT owner_id, owner_id = ?2
		OR EXISTS (SELECT 1 FROM user_shares WHERE doc = ?1 AND user_id = ?2)
		OR EXISTS (SELECT 1 FROM group_shares g JOIN members m ON m.group_id = g.group_id
			WHERE g.doc = ?1 AND m.user_id = ?2)
		FROM docs WHERE name = ?1`, doc, uid).Scan(&ownerID, &allowed)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, false, nil
	}
	return err == nil, allowed, ownerID == uid, err
}

func (a *app) docPath(name string) string {
	return filepath.Join(a.docsDir, filepath.FromSlash(name)+".txt")
}

func (a *app) readDoc(name string) (string, error) {
	b, err := os.ReadFile(a.docPath(name))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return string(b), err
}

// writeDoc grava num temporário e renomeia, para nunca deixar arquivo pela metade.
func (a *app) writeDoc(name string, body []byte) error {
	path := a.docPath(name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(body); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func docName(w http.ResponseWriter, r *http.Request) (string, bool) {
	name := r.PathValue("name")
	if len(name) > 200 || !validName.MatchString(name) {
		fail(w, http.StatusNotFound, "nome de documento inválido (use letras, números, - e _ separados por /)")
		return "", false
	}
	return name, true
}

type docRow struct{ Name, Owner, Updated string }

func (a *app) home(w http.ResponseWriter, r *http.Request, u user) {
	rows, err := a.db.Query(`SELECT d.name, o.name, d.updated FROM docs d JOIN users o ON o.id = d.owner_id
		WHERE d.owner_id = ?1
		OR d.name IN (SELECT doc FROM user_shares WHERE user_id = ?1)
		OR d.name IN (SELECT g.doc FROM group_shares g JOIN members m ON m.group_id = g.group_id WHERE m.user_id = ?1)
		ORDER BY d.updated DESC`, u.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	var docs []docRow
	for rows.Next() {
		var d docRow
		var t int64
		if err := rows.Scan(&d.Name, &d.Owner, &t); err != nil {
			serverError(w, err)
			return
		}
		if d.Owner == u.Name {
			d.Owner = ""
		}
		d.Updated = time.Unix(t, 0).Format("02/01/2006 15:04")
		docs = append(docs, d)
	}
	if err := rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	render(w, http.StatusOK, "home", struct {
		Me   string
		Docs []docRow
	}{u.Name, docs})
}

type docPage struct {
	Name, Body, Me, OwnerName string
	Exists, Owner, NotFound   bool
	Users, Groups             []string
}

func (a *app) view(w http.ResponseWriter, r *http.Request, u user) {
	name, ok := docName(w, r)
	if !ok {
		return
	}
	exists, allowed, owner, err := a.access(name, u.ID)
	if err != nil {
		serverError(w, err)
		return
	}
	if exists && !allowed {
		fail(w, http.StatusForbidden, "você não tem acesso a este documento")
		return
	}
	p := docPage{Name: name, Me: u.Name, Exists: exists, Owner: owner, NotFound: r.URL.Query().Has("erro")}
	if exists {
		if err := a.db.QueryRow(`SELECT u.name FROM docs d JOIN users u ON u.id = d.owner_id WHERE d.name = ?`,
			name).Scan(&p.OwnerName); err != nil {
			serverError(w, err)
			return
		}
		if p.Body, err = a.readDoc(name); err != nil {
			serverError(w, err)
			return
		}
	}
	if owner {
		if p.Users, err = names(a.db, `SELECT u.name FROM user_shares s JOIN users u ON u.id = s.user_id
			WHERE s.doc = ? ORDER BY 1`, name); err != nil {
			serverError(w, err)
			return
		}
		if p.Groups, err = names(a.db, `SELECT g.name FROM group_shares s JOIN groups g ON g.id = s.group_id
			WHERE s.doc = ? ORDER BY 1`, name); err != nil {
			serverError(w, err)
			return
		}
	}
	render(w, http.StatusOK, "doc", p)
}

func (a *app) save(w http.ResponseWriter, r *http.Request, u user) {
	name, ok := docName(w, r)
	if !ok {
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxDoc))
	if err != nil {
		http.Error(w, "documento grande demais", http.StatusRequestEntityTooLarge)
		return
	}
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	// tenta criar (quem cria vira dono); se já existe, confere a permissão
	res, err := a.db.Exec(`INSERT INTO docs (name, owner_id, updated) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`,
		name, u.ID, time.Now().Unix())
	if err != nil {
		serverError(w, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		_, allowed, _, err := a.access(name, u.ID)
		if err != nil {
			serverError(w, err)
			return
		}
		if !allowed {
			http.Error(w, "sem acesso", http.StatusForbidden)
			return
		}
		if _, err := a.db.Exec(`UPDATE docs SET updated = ? WHERE name = ?`, time.Now().Unix(), name); err != nil {
			serverError(w, err)
			return
		}
	}
	if err := a.writeDoc(name, body); err != nil {
		serverError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// share atende /_share e /_unshare; só o dono mexe nos compartilhamentos.
func (a *app) share(w http.ResponseWriter, r *http.Request, u user) {
	name, ok := docName(w, r)
	if !ok {
		return
	}
	if !a.mustOwn(w, name, u) {
		return
	}
	table, col, ref := "user_shares", "user_id", "users"
	if r.FormValue("tipo") == "group" {
		table, col, ref = "group_shares", "group_id", "groups"
	}
	var id int64
	err := a.db.QueryRow(`SELECT id FROM `+ref+` WHERE name = ?`, r.FormValue("alvo")).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		http.Redirect(w, r, "/"+name+"?erro", http.StatusSeeOther)
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	q := `INSERT OR IGNORE INTO ` + table + ` (doc, ` + col + `) VALUES (?, ?)`
	if strings.HasPrefix(r.URL.Path, "/_unshare/") {
		q = `DELETE FROM ` + table + ` WHERE doc = ? AND ` + col + ` = ?`
	}
	if _, err := a.db.Exec(q, name, id); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/"+name, http.StatusSeeOther)
}

func (a *app) del(w http.ResponseWriter, r *http.Request, u user) {
	name, ok := docName(w, r)
	if !ok {
		return
	}
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	if !a.mustOwn(w, name, u) {
		return
	}
	if _, err := a.db.Exec(`DELETE FROM docs WHERE name = ?`, name); err != nil {
		serverError(w, err)
		return
	}
	if err := os.Remove(a.docPath(name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Print(err)
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *app) mustOwn(w http.ResponseWriter, name string, u user) bool {
	exists, _, owner, err := a.access(name, u.ID)
	if err != nil {
		serverError(w, err)
		return false
	}
	if !exists || !owner {
		fail(w, http.StatusForbidden, "só o dono do documento pode fazer isso")
		return false
	}
	return true
}

// --- utilidades ---

func names(db *sql.DB, q string, args ...any) ([]string, error) {
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func render(w http.ResponseWriter, status int, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Print(err)
	}
}

func fail(w http.ResponseWriter, status int, msg string) { render(w, status, "msg", msg) }

func serverError(w http.ResponseWriter, err error) {
	log.Print(err)
	fail(w, http.StatusInternalServerError, "erro interno")
}
