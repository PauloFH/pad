package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
	_ "time/tzdata"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

const (
	maxDoc        = 2 << 20 // 2 MiB por documento
	maxPublicoDoc = 256 << 10
	maxDepth      = 5 // níveis de pasta abaixo do usuário (ou de /publico)
	sessionTTL    = 30 * 24 * time.Hour
	inviteTTL     = 7 * 24 * time.Hour
	minUser       = 2
	maxUser       = 40
	senhaMin      = 8
	senhaMax      = 72 // o bcrypt ignora o que passa de 72 bytes
)

//go:embed templates.html
var tmplFS embed.FS

//go:embed static/*.js static/*.css static/fonts/*
var staticFS embed.FS

var (
	tmpl = template.Must(template.New("").Funcs(template.FuncMap{"ramo": ramo, "corta": corta, "hora": hora, "st": st,
		"quando": func(t time.Time) string { return quando(t, time.Now()) }, "lim": func() any { return limites }}).
		ParseFS(tmplFS, "templates.html"))
	// limites são os números que as páginas mostram em texto e atributos, tirados do que o servidor aplica
	limites = struct {
		UserMax, SenhaMin, SenhaMax, NomeCurto, DiasConvite, DiasSessao, SegundosTrava, Backups, PublicoMilItens int
		PublicoDoc, PublicoTotal                                                                                 string
	}{maxUser, senhaMin, senhaMax, maxCurto, int(inviteTTL / (24 * time.Hour)), int(sessionTTL / (24 * time.Hour)),
		int(lockIdle / time.Second), backupsGuardados, maxPublicoItems / 1000, tamanho(maxPublicoDoc), tamanho(maxPublicoBytes)}
	// o nome vira caminho de arquivo: sem "..", "/" inicial, "//" ou ponto
	validName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*(/[A-Za-z0-9_-]+)*$`)
	// o usuário é o primeiro trecho do endereço (/paulo/notas): só minúsculas, para Paulo e paulo não coexistirem
	validUser = regexp.MustCompile(fmt.Sprintf(`^[a-z0-9][a-z0-9_-]{%d,%d}$`, minUser-1, maxUser-1))
	// usado quando o usuário não existe: o login leva o mesmo tempo, e a senha sorteada nunca confere
	dummyHash, _ = bcrypt.GenerateFromPassword([]byte(rand.Text()), bcrypt.DefaultCost)
)

const schema = `
CREATE TABLE IF NOT EXISTS users (
	id INTEGER PRIMARY KEY,
	name TEXT NOT NULL UNIQUE,
	hash BLOB NOT NULL,
	admin INTEGER NOT NULL DEFAULT 0);
-- used é flag e não FK: apagar quem usou o convite não pode reabri-lo
CREATE TABLE IF NOT EXISTS invites (
	token TEXT PRIMARY KEY,
	created_by INTEGER NOT NULL REFERENCES users ON DELETE CASCADE,
	expires INTEGER NOT NULL,
	used INTEGER NOT NULL DEFAULT 0);
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
	updated INTEGER NOT NULL,
	public INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS user_shares (
	doc TEXT NOT NULL REFERENCES docs ON DELETE CASCADE,
	user_id INTEGER NOT NULL REFERENCES users ON DELETE CASCADE,
	PRIMARY KEY (doc, user_id));
CREATE TABLE IF NOT EXISTS group_shares (
	doc TEXT NOT NULL REFERENCES docs ON DELETE CASCADE,
	group_id INTEGER NOT NULL REFERENCES groups ON DELETE CASCADE,
	PRIMARY KEY (doc, group_id));
CREATE TABLE IF NOT EXISTS secrets (k TEXT PRIMARY KEY, v BLOB NOT NULL);
-- hash e não a chave: quem lê o banco não consegue se passar pela máquina
CREATE TABLE IF NOT EXISTS mcp_maquinas (
	id INTEGER PRIMARY KEY,
	user_id INTEGER NOT NULL REFERENCES users ON DELETE CASCADE,
	nome TEXT NOT NULL,
	hash BLOB NOT NULL UNIQUE,
	criada INTEGER NOT NULL,
	vista INTEGER NOT NULL DEFAULT 0,
	UNIQUE (user_id, nome));
CREATE TABLE IF NOT EXISTS mcp_conversas (
	id INTEGER PRIMARY KEY,
	user_id INTEGER NOT NULL REFERENCES users ON DELETE CASCADE,
	nome TEXT NOT NULL,
	criada INTEGER NOT NULL,
	UNIQUE (user_id, nome));
CREATE TABLE IF NOT EXISTS mcp_mensagens (
	id INTEGER PRIMARY KEY,
	conversa_id INTEGER NOT NULL REFERENCES mcp_conversas ON DELETE CASCADE,
	autor TEXT NOT NULL, rotulo TEXT NOT NULL, humano INTEGER NOT NULL,
	em INTEGER NOT NULL, texto TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS mcp_mensagens_conversa ON mcp_mensagens (conversa_id, id);
-- a lista de documentos (toda página) filtra por dono e por quem recebeu: sem eles o SQLite varre docs
CREATE INDEX IF NOT EXISTS docs_dono ON docs (owner_id);
CREATE INDEX IF NOT EXISTS user_shares_usuario ON user_shares (user_id);
CREATE INDEX IF NOT EXISTS members_usuario ON members (user_id);
-- o "lido" é por apelido do agente, não por pessoa: dois agentes da mesma pessoa leem separado
CREATE TABLE IF NOT EXISTS mcp_lido (
	conversa_id INTEGER NOT NULL REFERENCES mcp_conversas ON DELETE CASCADE,
	apelido TEXT NOT NULL, ate INTEGER NOT NULL,
	PRIMARY KEY (conversa_id, apelido));
`

type app struct {
	db      *sql.DB
	dataDir string // pad.db, pad.lock e docs/
	docsDir string
	// serializa salvar/apagar: sem isso, um autosave em curso recria o arquivo de um doc recém-apagado
	writeMu sync.Mutex
	// totais de /publico, medidos na primeira gravação; sob writeMu
	pubMedido bool
	pubItens  int
	pubBytes  int64
	// assina os tokens de visitante do /publico
	visitorKey                            []byte
	hub                                   *hub // documentos abertos na edição ao vivo
	mcp                                   *mcpState
	limWS                                 *limite       // conexões novas por IP
	limPublico                            *limite       // POST em /publico por IP
	limVitais                             *limite       // Web Vitals por IP (vão para o log)
	limLoginIP, limLoginNome, limLoginPar *limite       // senhas erradas por IP, por conta e por IP nessa conta
	vagasBcrypt                           chan struct{} // bcrypt custa caro de propósito: pedidos demais juntos não podem tomar a CPU
}

type user struct {
	ID    int64
	Name  string
	Admin bool
}

func main() {
	dataDir := env("PAD_DATA", "data")
	a, err := openApp(dataDir)
	if err != nil {
		log.Fatal(err)
	}
	if len(os.Args) > 1 {
		if err := runCLI(a, os.Args[1:], os.Stdin); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	// o apagar-tudo de outro contêiner vê esta trava e recusa; o defer mantém o arquivo vivo (o GC fecharia e soltaria)
	trava, err := travaDados(dataDir, true)
	if err != nil {
		log.Fatal(err)
	}
	defer trava.Close()
	srv := &http.Server{Addr: env("PAD_ADDR", ":8080"), Handler: a.routes(), ReadHeaderTimeout: 10 * time.Second}
	srv.RegisterOnShutdown(a.mcp.encerrar) // senão o Shutdown espera os esperar de até 50 s
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	go func() {
		log.Printf("pad ouvindo em %s, dados em %s", srv.Addr, dataDir)
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()
	<-ctx.Done()
	// o Shutdown não espera os WebSockets (conexões sequestradas): o hub os fecha
	// com 1001 e espera cada um gravar; o flushAll pega o que sobrar
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil {
		log.Print(err)
	}
	a.hub.closeAll(sctx)
	a.hub.flushAll()
	log.Print("pad encerrado")
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
	// colunas que chegaram depois da primeira versão: bancos antigos ganham elas aqui
	for _, m := range [][2]string{
		{`SELECT admin FROM users LIMIT 0`, `ALTER TABLE users ADD COLUMN admin INTEGER NOT NULL DEFAULT 0`},
		{`SELECT public FROM docs LIMIT 0`, `ALTER TABLE docs ADD COLUMN public INTEGER NOT NULL DEFAULT 0`},
	} {
		if _, err := db.Exec(m[0]); err != nil {
			if _, err := db.Exec(m[1]); err != nil {
				return nil, err
			}
		}
	}
	key, err := loadVisitorKey(db)
	if err != nil {
		return nil, err
	}
	a := &app{db: db, dataDir: dataDir, docsDir: docsDir, visitorKey: key, mcp: newMcpState(),
		limWS: novoLimite(60, time.Minute), limPublico: novoLimite(120, time.Minute), limVitais: novoLimite(30, time.Minute),
		limLoginIP: novoLimite(10, 5*time.Minute), limLoginNome: novoLimite(20, 15*time.Minute), limLoginPar: novoLimite(3, 15*time.Minute),
		vagasBcrypt: make(chan struct{}, max(1, runtime.NumCPU()/2))}
	a.hub = &hub{a: a, docs: map[string]*liveDoc{}, perIP: map[string]int{}, perDoc: map[string]int{}}
	return a, nil
}

// staticSub é static/ sem o prefixo, para servir em /_static/.
var staticSub = func() fs.FS { sub, _ := fs.Sub(staticFS, "static"); return sub }()

// etags é o hash de cada arquivo de static/, calculado uma vez: o conteúdo embutido não muda enquanto o binário roda.
var etags = func() map[string]string {
	m := map[string]string{}
	fs.WalkDir(staticSub, ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			b, _ := fs.ReadFile(staticSub, p)
			m[p] = fmt.Sprintf(`"%x"`, sha256.Sum256(b))
		}
		return err
	})
	return m
}()

// versao é o trecho do hash que vai na URL do estático (?v=): muda quando o arquivo muda.
func versao(etag string) string { return etag[1:17] }

// st é o endereço versionado de um estático, para o template: /_static/ot.js?v=<hash>.
func st(p string) (string, error) {
	tag, ok := etags[p]
	if !ok {
		return "", fmt.Errorf("estático inexistente: %s", p)
	}
	return "/_static/" + p + "?v=" + versao(tag), nil
}

const umAno = "public, max-age=31536000, immutable"

// estaticos guarda por um ano o que vem com o ?v= do conteúdo atual: o binário embute os arquivos e muda a cada
// deploy, e aí a URL muda junto. Sem o ?v= (ou com um antigo) revalida a cada carga; com o ETag volta 304 sem corpo.
func estaticos(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") { // sem listagem do diretório
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		if tag, ok := etags[strings.TrimPrefix(r.URL.Path, "/_static/")]; ok {
			w.Header().Set("ETag", tag) // o http.FileServer responde 304 ao If-None-Match igual
			if r.URL.Query().Get("v") == versao(tag) {
				w.Header().Set("Cache-Control", umAno)
			}
		}
		if strings.HasSuffix(r.URL.Path, ".woff2") { // o mime do Go não conhece .woff2 sem /etc/mime.types (imagem do Docker)
			w.Header().Set("Content-Type", "font/woff2")
			// shortcut: a fonte fica um ano no cache sem ?v= (o url() do CSS e o preload precisam da mesma URL);
			// ao trocar o conteúdo de uma fonte, troque também o nome do arquivo
			w.Header().Set("Cache-Control", umAno)
		}
		h.ServeHTTP(w, r)
	})
}

func (a *app) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /_login", a.loginPage)
	mux.HandleFunc("POST /_login", a.login)
	mux.HandleFunc("POST /_logout", a.logout)
	mux.HandleFunc("GET /_join/{token}", a.join)
	mux.HandleFunc("GET /_privacidade", func(w http.ResponseWriter, r *http.Request) { render(w, http.StatusOK, "privacidade", nil) })
	mux.HandleFunc("GET /_conta/apagar", a.auth(a.apagarConta))
	mux.HandleFunc("GET /_conta/exportar", a.auth(a.exportar))
	mux.HandleFunc("POST /_conta/apagar", a.auth(a.apagarConta))
	mux.HandleFunc("GET /_termos", func(w http.ResponseWriter, r *http.Request) { render(w, http.StatusOK, "termos", nil) })
	mux.HandleFunc("POST /_join/{token}", a.join)
	mux.HandleFunc("GET /_convite", a.auth(a.invite))
	mux.HandleFunc("POST /_convite", a.auth(a.invite))
	mux.Handle("GET /_static/", estaticos(http.StripPrefix("/_static/", http.FileServerFS(staticSub))))
	mux.HandleFunc("GET /_ws/{name...}", a.liveWS)
	mux.HandleFunc("POST /_vitais", a.vitais)
	mux.HandleFunc("GET /{$}", a.inicio)
	mux.HandleFunc("GET /{name...}", a.view)  // sem auth: documento público abre sem login
	mux.HandleFunc("POST /{name...}", a.save) // confere a sessão por dentro: /publico grava sem login
	mux.HandleFunc("POST /_public/{name...}", a.auth(a.setPublic))
	mux.HandleFunc("POST /_share/{name...}", a.auth(a.share))
	mux.HandleFunc("POST /_unshare/{name...}", a.auth(a.share))
	mux.HandleFunc("POST /_delete/{name...}", a.auth(a.del))
	mux.HandleFunc("GET /_mcp", a.auth(func(w http.ResponseWriter, r *http.Request, u user) {
		http.Redirect(w, r, "/"+u.Name+"/_mcp", http.StatusSeeOther)
	}))
	mcp := a.mcpRoutes()
	return http.NewCrossOriginProtection().Handler(lentas(comprime(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		// shortcut: sem script-src porque as páginas têm scripts e onsubmit inline; tirá-los antes de restringir
		w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'; object-src 'none'; base-uri 'none'; form-action 'self'")
		if isHTTPS(r) {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		// /{usuario}/_mcp... fica num mux à parte: o padrão se sobrepõe a /_static/, /_ws/, /_join/ e /_public/ sem ser mais
		// específico que nenhum, e o ServeMux entra em pânico ao registrar os dois no mesmo mux.
		if u, resto, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/"); ok && validUser.MatchString(u) &&
			(resto == "_mcp" || strings.HasPrefix(resto, "_mcp/")) {
			mcp.ServeHTTP(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	}))))
}

// --- sessão ---

func (a *app) auth(h func(http.ResponseWriter, *http.Request, user)) http.HandlerFunc {
	return a.exigeSessao(true, h)
}

// exigeSessao chama h com quem está logado. Sem sessão, o GET de uma página vai ao login; o resto, e toda rota
// JSON, leva 401, que o fetch reconhece sem seguir o redirecionamento.
func (a *app) exigeSessao(pagina bool, h func(http.ResponseWriter, *http.Request, user)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok, err := a.currentUser(w, r)
		switch {
		case err != nil:
			serverError(w, err)
		case ok:
			h(w, r, u)
		case pagina && r.Method == http.MethodGet:
			toLogin(w, r)
		default:
			http.Error(w, "sessão expirada, entre de novo", http.StatusUnauthorized)
		}
	}
}

// currentUser lê a sessão do cookie e a renova; ok é false quando não há sessão válida.
func (a *app) currentUser(w http.ResponseWriter, r *http.Request) (u user, ok bool, err error) {
	c, err := r.Cookie(cookieSessao)
	if err != nil {
		return u, false, nil
	}
	now := time.Now()
	var expires int64
	err = a.db.QueryRow(`SELECT u.id, u.name, u.admin, s.expires FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token = ? AND s.expires > ?`, c.Value, now.Unix()).Scan(&u.ID, &u.Name, &u.Admin, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return u, false, nil
	}
	if err != nil {
		return u, false, err
	}
	// renovação deslizante, no máximo uma vez por dia para não gravar no banco a cada clique
	if time.Unix(expires, 0).Sub(now) < sessionTTL-24*time.Hour {
		if _, err := a.db.Exec(`UPDATE sessions SET expires = ? WHERE token = ?`, now.Add(sessionTTL).Unix(), c.Value); err != nil {
			return u, false, err
		}
		setSessionCookie(w, r, c.Value, now.Add(sessionTTL))
	}
	return u, true, nil
}

const cookieSessao = "sid"

func apagarCookieSessao(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: cookieSessao, Path: "/", MaxAge: -1})
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name: cookieSessao, Value: token, Path: "/", Expires: expires,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: isHTTPS(r),
	})
}

func toLogin(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/_login?next="+url.QueryEscape(r.URL.Path), http.StatusSeeOther)
}

type loginData struct{ Next, Err string }

func (a *app) loginPage(w http.ResponseWriter, r *http.Request) {
	render(w, http.StatusOK, "login", loginData{Next: r.URL.Query().Get("next")})
}

// prazoLogin limita o corpo lento dos formulários com senha; var para o teste baixar. Fica acima de esperaBcrypt:
// vencido antes, ele cancelaria o r.Context() de quem só espera a vaga.
var prazoLogin = 30 * time.Second

const esperaBcrypt = 10 * time.Second

// formularioDeSenha limita tamanho e prazo antes de ler: sem isso o formulário pode ter dezenas de MB e não custa
// nada a quem envia.
func formularioDeSenha(w http.ResponseWriter, r *http.Request) {
	http.NewResponseController(w).SetReadDeadline(time.Now().Add(prazoLogin))
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
}

func (a *app) login(w http.ResponseWriter, r *http.Request) {
	formularioDeSenha(w, r)
	name, pass, next := normUser(r.FormValue("name")), r.FormValue("pass"), r.FormValue("next")
	var id int64
	hash := dummyHash
	if err := a.db.QueryRow(`SELECT id, hash FROM users WHERE name = ?`, name).Scan(&id, &hash); err != nil && !errors.Is(err, sql.ErrNoRows) {
		serverError(w, err)
		return
	}
	err := a.confereSenha(r, name, hash, pass)
	switch {
	case errors.Is(err, errTentativas):
		render(w, http.StatusTooManyRequests, "login", loginData{next, err.Error()})
		return
	case errors.Is(err, errOcupado):
		render(w, http.StatusServiceUnavailable, "login", loginData{next, err.Error()})
		return
	case errors.Is(err, errSenha):
		render(w, http.StatusUnauthorized, "login", loginData{next, "usuário ou senha incorretos"})
		return
	case err != nil: // a conexão caiu na fila: ninguém lê a resposta
		return
	}
	if err := a.startSession(w, r, id); err != nil {
		serverError(w, err)
		return
	}
	// só caminhos de documento: "//x", "/\\x" ou "/\t/x" levariam para outro site
	if next != "/" && !validName.MatchString(strings.TrimPrefix(next, "/")) {
		next = "/"
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

var (
	errSenha      = falha("senha incorreta")
	errTentativas = falha("tentativas demais, espere alguns minutos")
	errOcupado    = falha("servidor ocupado, tente de novo")
)

// confereSenha compara pass com hash na fila do bcrypt, contando o erro nos limites do login: nil é senha certa;
// senão errSenha, errTentativas, errOcupado ou o erro do r.Context(). O login e o apagar conta passam por aqui,
// para nenhuma tela virar atalho para adivinhar senha.
func (a *app) confereSenha(r *http.Request, name string, hash []byte, pass string) error {
	ip := clientIP(r)
	if a.senhaBloqueada(ip, name, time.Now()) {
		return errTentativas
	}
	select {
	case a.vagasBcrypt <- struct{}{}:
	case <-r.Context().Done():
		return r.Context().Err()
	case <-time.After(esperaBcrypt):
		return errOcupado
	}
	// de novo com a vaga: quem esperou na fila entrou antes de os erros dos outros serem contados
	if a.senhaBloqueada(ip, name, time.Now()) {
		<-a.vagasBcrypt
		return errTentativas
	}
	err := bcrypt.CompareHashAndPassword(hash, []byte(pass))
	<-a.vagasBcrypt
	if err != nil {
		a.senhaErrada(ip, name, time.Now())
		return errSenha
	}
	return nil
}

// senhaBloqueada diz se ip já gastou as tentativas de senha, no total ou nesta conta.
// A conta cheia só barra quem já errou nela: senão qualquer um deixaria o dono de fora errando a senha dele de propósito.
func (a *app) senhaBloqueada(ip, name string, t time.Time) bool {
	return a.limLoginIP.cheio(ip, t) || validUser.MatchString(name) && a.limLoginNome.cheio(name, t) && a.limLoginPar.cheio(ip+"|"+name, t)
}

// senhaErrada conta o erro; nome fora do formato não existe nem vira chave: o mapa não guarda texto arbitrário de quem tenta.
func (a *app) senhaErrada(ip, name string, t time.Time) {
	a.limLoginIP.somar(ip, t)
	if validUser.MatchString(name) {
		a.limLoginNome.somar(name, t)
		a.limLoginPar.somar(ip+"|"+name, t)
	}
}

func (a *app) startSession(w http.ResponseWriter, r *http.Request, uid int64) error {
	token, now := rand.Text(), time.Now()
	if _, err := a.db.Exec(`DELETE FROM sessions WHERE expires <= ?`, now.Unix()); err != nil {
		return err
	}
	if _, err := a.db.Exec(`INSERT INTO sessions (token, user_id, expires) VALUES (?, ?, ?)`,
		token, uid, now.Add(sessionTTL).Unix()); err != nil {
		return err
	}
	setSessionCookie(w, r, token, now.Add(sessionTTL))
	return nil
}

func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

// origem é o endereço do pad como quem pediu o vê, para links que a pessoa leva para fora.
func origem(r *http.Request) string {
	if isHTTPS(r) {
		return "https://" + r.Host
	}
	return "http://" + r.Host
}

// --- convites ---

func (a *app) invite(w http.ResponseWriter, r *http.Request, u user) {
	if !u.Admin {
		fail(w, http.StatusForbidden, "só administradores podem convidar")
		return
	}
	data := struct{ Me, Link string }{Me: u.Name}
	if r.Method == http.MethodPost {
		token := rand.Text()
		if _, err := a.db.Exec(`INSERT INTO invites (token, created_by, expires) VALUES (?, ?, ?)`,
			token, u.ID, time.Now().Add(inviteTTL).Unix()); err != nil {
			serverError(w, err)
			return
		}
		data.Link = origem(r) + "/_join/" + token
	}
	render(w, http.StatusOK, "invite", data)
}

type joinData struct{ Name, Err string }

func (a *app) join(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	const valid = `SELECT 1 FROM invites WHERE token = ? AND used = 0 AND expires > ?`
	err := a.db.QueryRow(valid, token, time.Now().Unix()).Scan(new(int))
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, http.StatusNotFound, "convite inválido, vencido ou já usado")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	if r.Method == http.MethodGet {
		render(w, http.StatusOK, "join", joinData{})
		return
	}
	name, pass := normUser(r.FormValue("name")), r.FormValue("pass")
	bad := func(msg string) { render(w, http.StatusBadRequest, "join", joinData{name, msg}) }
	if err := checkUserName(name); err != nil {
		bad(err.Error())
		return
	}
	if err := checkPassword(pass); err != nil {
		bad(err.Error())
		return
	}
	if pass != r.FormValue("pass2") {
		bad("as senhas não conferem")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(pass), bcrypt.DefaultCost)
	if err != nil {
		serverError(w, err)
		return
	}
	tx, err := a.db.Begin()
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback()
	// marca o convite antes de criar a conta: dois envios simultâneos não criam duas contas
	res, err := tx.Exec(`UPDATE invites SET used = 1 WHERE token = ? AND used = 0 AND expires > ?`, token, time.Now().Unix())
	if err != nil {
		serverError(w, err)
		return
	}
	if n, _ := res.RowsAffected(); n != 1 {
		fail(w, http.StatusNotFound, "convite inválido, vencido ou já usado")
		return
	}
	var taken bool
	if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM users WHERE name = ?)`, name).Scan(&taken); err != nil {
		serverError(w, err)
		return
	}
	if taken {
		bad("esse nome já existe, escolha outro")
		return
	}
	res, err = tx.Exec(`INSERT INTO users (name, hash) VALUES (?, ?)`, name, hash)
	if err != nil {
		serverError(w, err)
		return
	}
	uid, _ := res.LastInsertId()
	if err := tx.Commit(); err != nil {
		serverError(w, err)
		return
	}
	if err := a.startSession(w, r, uid); err != nil {
		serverError(w, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func normUser(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func checkPassword(p string) error {
	if len(p) < senhaMin || len(p) > senhaMax {
		return fmt.Errorf("a senha precisa ter de %d a %d caracteres", senhaMin, senhaMax)
	}
	return nil
}

func (a *app) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieSessao); err == nil {
		a.db.Exec(`DELETE FROM sessions WHERE token = ?`, c.Value)
	}
	apagarCookieSessao(w)
	http.Redirect(w, r, "/_login", http.StatusSeeOther)
}

// --- documentos ---

// acesso é o que alguém pode num documento; página, POST, WebSocket e MCP decidem todos por ele.
type acesso struct {
	Existe, Dono bool // fora da área aberta, onde não há linha em docs
	Publico      bool // o dono abriu para leitura sem login
	Aberto       bool // área /publico: sem dono, qualquer um lê e edita
	Ver, Editar  bool
	Criar        bool // não existe e fica no espaço de quem pede: a primeira gravação o cria
}

// acesso decide o que u pode em name; o user zero é o visitante sem login.
func (a *app) acesso(name string, u user) (acesso, error) {
	if isPublico(name) {
		return acesso{Aberto: true, Ver: true, Editar: true}, nil
	}
	var ac acesso
	var ownerID int64
	err := a.db.QueryRow(`SELECT owner_id, public, owner_id = ?2
		OR EXISTS (SELECT 1 FROM user_shares WHERE doc = ?1 AND user_id = ?2)
		OR EXISTS (SELECT 1 FROM group_shares g JOIN members m ON m.group_id = g.group_id
			WHERE g.doc = ?1 AND m.user_id = ?2)
		FROM docs WHERE name = ?1`, name, u.ID).Scan(&ownerID, &ac.Publico, &ac.Editar)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		ac.Criar = u.ID != 0 && strings.HasPrefix(name, u.Name+"/")
	case err != nil:
		return acesso{}, err
	default:
		ac.Existe, ac.Dono = true, ownerID == u.ID
	}
	ac.Editar = ac.Editar || ac.Criar
	ac.Ver = ac.Editar || ac.Publico
	return ac, nil
}

// limiteDoc é o tamanho máximo de name, em bytes UTF-8, e o erro de quem passa dele.
func limiteDoc(name string) (int, error) {
	if isPublico(name) {
		return maxPublicoDoc, msgErr{errTooBig, "documento da área pública passa de " + tamanho(maxPublicoDoc)}
	}
	return maxDoc, errTooBig
}

// normalizaQuebras faz o que o textarea faz com as quebras de linha: sem isso os comprimentos divergem nas abas.
func normalizaQuebras(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
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

// criarDoc (com writeMu) cria a linha do documento no espaço de uid; se já existe, nada muda.
func (a *app) criarDoc(name string, uid int64) error {
	_, err := a.db.Exec(`INSERT INTO docs (name, owner_id, updated) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`,
		name, uid, time.Now().Unix())
	return err
}

// persistir (com writeMu) grava o texto de name: na área pública, mantendo os totais dela; fora dela, só se a linha
// ainda existe, para um documento apagado enquanto alguém entrava não ganhar arquivo sem dono.
func (a *app) persistir(name string, body []byte) error {
	if isPublico(name) {
		return a.gravarPublico(name, body)
	}
	res, err := a.db.Exec(`UPDATE docs SET updated = ? WHERE name = ?`, time.Now().Unix(), name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil
	}
	return a.writeDoc(name, body)
}

// checkDocName é a regra do nome de documento, para a página e para o agente.
func checkDocName(name string) error {
	if len(name) > 200 || !validName.MatchString(name) {
		return falha("nome de documento inválido (use letras, números, - e _ separados por /)")
	}
	if strings.Count(name, "/") > maxDepth {
		return falha(fmt.Sprintf("no máximo %d níveis de pasta", maxDepth))
	}
	return nil
}

func docName(w http.ResponseWriter, r *http.Request) (string, bool) {
	name := r.PathValue("name")
	if err := checkDocName(name); err != nil {
		fail(w, http.StatusNotFound, err.Error())
		return "", false
	}
	return name, true
}

// Label é o nome sem "/eu/" na frente: na própria interface o usuário não precisa ver o próprio nome.
// Owner é o dono quando não é o próprio usuário.
type docRow struct {
	Name, Label, Owner string
	UpdatedAt          time.Time
}

// userDocs lista os documentos de u e os compartilhados com ele, do mais recente ao mais antigo.
func (a *app) userDocs(u user) ([]docRow, error) {
	rows, err := a.db.Query(`SELECT d.name, d.updated FROM docs d
		WHERE d.owner_id = ?1
		OR d.name IN (SELECT doc FROM user_shares WHERE user_id = ?1)
		OR d.name IN (SELECT g.doc FROM group_shares g JOIN members m ON m.group_id = g.group_id WHERE m.user_id = ?1)
		ORDER BY d.updated DESC`, u.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var docs []docRow
	for rows.Next() {
		var d docRow
		var t int64
		if err := rows.Scan(&d.Name, &t); err != nil {
			return nil, err
		}
		d.Label = strings.TrimPrefix(d.Name, u.Name+"/")
		d.UpdatedAt = time.Unix(t, 0)
		// só se cria documento dentro do próprio usuário: o dono é o primeiro trecho do nome
		if dono, _, _ := strings.Cut(d.Name, "/"); dono != u.Name {
			d.Owner = dono
		}
		docs = append(docs, d)
	}
	return docs, rows.Err()
}

// cartao é um bloco da entrada; Meta junta quando mudou e quem está nele agora.
type cartao struct{ Name, Label, Meta string }

// inicio é o endereço raiz: quem tem sessão vê os documentos; o visitante, a apresentação do pad.
func (a *app) inicio(w http.ResponseWriter, r *http.Request) {
	u, ok, err := a.currentUser(w, r)
	switch {
	case err != nil:
		serverError(w, err)
	case ok:
		a.home(w, r, u)
	default:
		render(w, http.StatusOK, "landing", nil)
	}
}

func (a *app) home(w http.ResponseWriter, r *http.Request, u user) {
	docs, err := a.userDocs(u)
	if err != nil {
		serverError(w, err)
		return
	}
	col := colunaDe(docs, u, "")
	agora := time.Now()
	bloco := func(d docRow, por bool) cartao {
		meta := []string{quando(d.UpdatedAt, agora)}
		if por {
			meta = append([]string{"por " + maiuscula(d.Owner)}, meta...)
		}
		// quem abre a entrada já sabe onde está: só os outros contam
		if q := quemEsta(slices.DeleteFunc(a.hub.editores(d.Name), func(n string) bool { return n == u.Name })); q != "" {
			meta = append(meta, q)
		}
		return cartao{d.Name, d.Label, strings.Join(meta, " · ")}
	}
	p := struct {
		Me, Name                 string
		Admin                    bool
		Coluna                   *colunaData
		Recentes, Compartilhados []cartao
	}{Me: u.Name, Admin: u.Admin, Coluna: col}
	// userDocs já vem do mais recente ao mais antigo
	for i, d := range docs {
		if i < 9 {
			p.Recentes = append(p.Recentes, bloco(d, false))
		}
		if d.Owner != "" {
			p.Compartilhados = append(p.Compartilhados, bloco(d, true))
		}
	}
	render(w, http.StatusOK, "home", p)
}

// noArvore é uma pasta ou um documento da coluna; Caminho é o nome completo.
type noArvore struct {
	Nome, Caminho string
	Pasta         bool
	Filhos        []noArvore
}

// arvore monta as pastas de me a partir dos nomes (me/a/b vira a pasta a com b),
// pastas antes de documentos e cada grupo em ordem alfabética. Os de outros donos ficam de fora.
func arvore(docs []docRow, me string) []noArvore {
	var raiz []noArvore
	for _, d := range docs {
		if resto, ok := strings.CutPrefix(d.Name, me+"/"); ok {
			raiz = inserir(raiz, strings.Split(resto, "/"), me)
		}
	}
	ordenar(raiz)
	return raiz
}

// um nome pode ser documento e pasta ao mesmo tempo (me/p e me/p/x): viram dois nós
func inserir(nos []noArvore, partes []string, pai string) []noArvore {
	caminho := pai + "/" + partes[0]
	if len(partes) == 1 {
		return append(nos, noArvore{Nome: partes[0], Caminho: caminho})
	}
	i := slices.IndexFunc(nos, func(n noArvore) bool { return n.Pasta && n.Nome == partes[0] })
	if i < 0 {
		nos = append(nos, noArvore{Nome: partes[0], Caminho: caminho, Pasta: true})
		i = len(nos) - 1
	}
	nos[i].Filhos = inserir(nos[i].Filhos, partes[1:], caminho)
	return nos
}

func ordenar(nos []noArvore) {
	slices.SortFunc(nos, func(a, b noArvore) int {
		if a.Pasta != b.Pasta {
			if a.Pasta {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Nome, b.Nome)
	})
	for i := range nos {
		ordenar(nos[i].Filhos)
	}
}

// colunaData é a coluna de documentos de quem está logado; Atual é o documento aberto.
type colunaData struct {
	Arvore         []noArvore
	Compartilhados []docRow
	Atual          string
}

// colunaDe monta a coluna a partir dos documentos já lidos (a entrada usa os mesmos para os blocos).
func colunaDe(docs []docRow, u user, atual string) *colunaData {
	c := &colunaData{Arvore: arvore(docs, u.Name), Atual: atual}
	for _, d := range docs {
		if d.Owner != "" {
			c.Compartilhados = append(c.Compartilhados, d)
		}
	}
	slices.SortFunc(c.Compartilhados, func(a, b docRow) int { return strings.Compare(a.Name, b.Name) })
	return c
}

// ramo leva o documento atual para dentro do template recursivo da árvore.
func ramo(atual string, nos []noArvore) any {
	return struct {
		Atual string
		Nos   []noArvore
	}{atual, nos}
}

// corta separa as pastas do último trecho: "a/b/c" dá "a/b/" e "c".
func corta(s string) [2]string {
	i := strings.LastIndex(s, "/") + 1
	return [2]string{s[:i], s[i:]}
}

type docPage struct {
	Name, Label, Body, Me string
	Admin, Moderar        bool // Moderar: admin vendo documento de /publico
	Aberto                bool // documento de /publico: qualquer pessoa lê e edita
	Exists, Owner, Public bool
	NotFound              bool
	Users, Groups         []string
	Coluna                *colunaData // nil para visitante
}

func (a *app) view(w http.ResponseWriter, r *http.Request) {
	u, logged, err := a.currentUser(w, r)
	if err != nil {
		serverError(w, err)
		return
	}
	name, ok := docName(w, r)
	if !ok {
		return
	}
	ac, err := a.acesso(name, u)
	if err != nil {
		serverError(w, err)
		return
	}
	// mesma resposta para privado e inexistente: ninguém descobre os nomes dos outros
	if !ac.Ver && !logged {
		toLogin(w, r)
		return
	}
	if !ac.Ver {
		render(w, http.StatusNotFound, "msg", msgPage{"documento não encontrado ou sem acesso. Seus documentos ficam em /" + u.Name + "/...", u.Name, name})
		return
	}
	p := docPage{Name: name, Label: strings.TrimPrefix(name, u.Name+"/"), Me: u.Name, Admin: u.Admin,
		Moderar: ac.Aberto && u.Admin, Aberto: ac.Aberto, Exists: ac.Existe, Owner: ac.Dono, Public: ac.Publico,
		NotFound: r.URL.Query().Has("erro")}
	if p.Body, err = a.readDoc(name); err != nil {
		serverError(w, err)
		return
	}
	if ac.Aberto {
		p.Label, p.Exists = name, p.Body != ""
	}
	// a coluna só depois de decidido o acesso: o caminho do 404 não consulta nada a mais
	if logged {
		docs, err := a.userDocs(u)
		if err != nil {
			serverError(w, err)
			return
		}
		p.Coluna = colunaDe(docs, u, name)
	}
	if !ac.Editar {
		render(w, http.StatusOK, "public", p)
		return
	}
	if ac.Dono {
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

func (a *app) save(w http.ResponseWriter, r *http.Request) {
	name, ok := docName(w, r)
	if !ok {
		return
	}
	if isPublico(name) && !a.limPublico.permite(clientIP(r), time.Now()) {
		http.Error(w, "gravações demais, tente de novo mais tarde", http.StatusTooManyRequests)
		return
	}
	// confere antes de ler o corpo: sem isso qualquer um faz o servidor guardar 2 MB por pedido
	if _, _, ok := a.podeGravar(w, r, name); !ok {
		return
	}
	limite, grande := limiteDoc(name)
	// aqui e não no servidor: um prazo global cortaria as esperas longas do MCP
	http.NewResponseController(w).SetReadDeadline(time.Now().Add(30 * time.Second))
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, int64(limite)))
	if err != nil {
		var demais *http.MaxBytesError
		if errors.As(err, &demais) {
			http.Error(w, grande.Error(), http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "o documento demorou demais para chegar", http.StatusRequestTimeout)
		}
		return
	}
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	// de novo: a sessão ou o acesso podem ter mudado enquanto o corpo chegava
	u, ac, ok := a.podeGravar(w, r, name)
	if !ok {
		return
	}
	if ac.Criar {
		if err := a.criarDoc(name, u.ID); err != nil {
			serverError(w, err)
			return
		}
	}
	// a aba aberta gravaria o texto dela por cima deste
	if a.hub.connected(name) {
		http.Error(w, "documento aberto na edição ao vivo: recarregue a página", http.StatusConflict)
		return
	}
	if err := a.persistir(name, body); err != nil {
		serverError(w, err)
		return
	}
	a.hub.forget(name)
	w.WriteHeader(http.StatusNoContent)
}

// podeGravar responde 401 ou 403 a quem não edita name.
func (a *app) podeGravar(w http.ResponseWriter, r *http.Request, name string) (user, acesso, bool) {
	u, logged, err := a.currentUser(w, r)
	var ac acesso
	if err == nil {
		ac, err = a.acesso(name, u)
	}
	switch {
	case err != nil:
		serverError(w, err)
	case ac.Editar:
		return u, ac, true
	case !logged:
		http.Error(w, "sessão expirada, entre de novo", http.StatusUnauthorized)
	default:
		http.Error(w, "sem acesso", http.StatusForbidden)
	}
	return u, ac, false
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
	a.hub.recheck(name)
	http.Redirect(w, r, "/"+name, http.StatusSeeOther)
}

func (a *app) del(w http.ResponseWriter, r *http.Request, u user) {
	name, ok := docName(w, r)
	if !ok {
		return
	}
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	if isPublico(name) {
		a.apagarPublico(w, r, name, u)
		return
	}
	if !a.mustOwn(w, name, u) {
		return
	}
	a.hub.kill(name, "documento apagado")
	if _, err := a.db.Exec(`DELETE FROM docs WHERE name = ?`, name); err != nil {
		serverError(w, err)
		return
	}
	if err := os.Remove(a.docPath(name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Print(err)
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// --- área /publico: sem dono, sem login, qualquer um lê e grava ---

const publico = "publico"

// tetos de /publico, em itens (documentos + pastas) e em bytes; var para o teste poder baixar
var (
	maxPublicoItems       = 50000
	maxPublicoBytes int64 = 1 << 30
)

func isPublico(name string) bool { return name == publico || strings.HasPrefix(name, publico+"/") }

// checkUserName barra também "publico", que é a área aberta e não pode virar espaço de alguém.
func checkUserName(s string) error {
	if !validUser.MatchString(s) || s == publico {
		return fmt.Errorf("nome: de %d a %d letras minúsculas, números, - ou _, sem acento nem espaço (e não pode ser \"publico\")", minUser, maxUser)
	}
	return nil
}

// gravarPublico (com writeMu) abre espaço, grava e mantém os totais em dia: percorrer a pasta a cada documento novo
// deixaria todo mundo esperando o writeMu.
func (a *app) gravarPublico(name string, body []byte) error {
	a.medirPublico()
	if err := a.roomPublico(name, int64(len(body))); err != nil {
		return err
	}
	path := a.docPath(name)
	antes, existia := tamanhoDe(path)
	pastas := pastasFaltando(filepath.Dir(path), filepath.Join(a.docsDir, publico))
	if err := a.writeDoc(name, body); err != nil {
		a.pubMedido = false // as pastas podem ter ficado: a próxima gravação mede de novo
		return err
	}
	if !existia {
		a.pubItens += 1 + pastas
	}
	a.pubBytes += int64(len(body)) - antes
	return nil
}

// medirPublico (com writeMu) conta a área na primeira vez; depois os totais andam a cada gravação e remoção.
func (a *app) medirPublico() {
	if a.pubMedido {
		return
	}
	a.pubItens, a.pubBytes = countEntries(filepath.Join(a.docsDir, publico))
	// o documento /publico mora ao lado da pasta, e não dentro dela
	if size, ok := tamanhoDe(a.docPath(publico)); ok {
		a.pubItens++
		a.pubBytes += size
	}
	a.pubMedido = true
}

func tamanhoDe(path string) (int64, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	return info.Size(), true
}

// pastasFaltando conta as pastas que o MkdirAll vai criar entre root (que não conta) e dir.
func pastasFaltando(dir, root string) (n int) {
	for ; strings.HasPrefix(dir, root+string(filepath.Separator)); dir = filepath.Dir(dir) {
		if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
			return n
		}
		n++
	}
	return n
}

// roomPublico (com writeMu) abre espaço antes de gravar size bytes em name: passando de um
// teto, apaga de uma vez os editados há mais tempo, até 1% abaixo dele, para a próxima gravação
// não percorrer a pasta de novo. Abertos na edição ao vivo e o próprio name ficam.
func (a *app) roomPublico(name string, size int64) error {
	path := a.docPath(name)
	antes, existia := tamanhoDe(path)
	estoura := func() bool {
		return !existia && a.pubItens >= maxPublicoItems || a.pubBytes-antes+size > maxPublicoBytes
	}
	if !estoura() {
		return nil
	}
	type candidato struct {
		path string
		em   time.Time
		size int64
	}
	var cs []candidato
	root := filepath.Join(a.docsDir, publico)
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || p == path {
			return nil
		}
		if info, err := d.Info(); err == nil && !a.hub.connected(docOf(a.docsDir, p)) {
			cs = append(cs, candidato{p, info.ModTime(), info.Size()})
		}
		return nil
	})
	slices.SortFunc(cs, func(x, y candidato) int { return x.em.Compare(y.em) })
	alvoItens, alvoBytes := maxPublicoItems-maxPublicoItems/100, maxPublicoBytes-maxPublicoBytes/100
	for _, c := range cs {
		if !estoura() && a.pubItens < alvoItens && a.pubBytes-antes+size <= alvoBytes {
			break
		}
		if err := a.removerPublico(c.path, c.size); err != nil {
			return err
		}
	}
	if estoura() {
		return errors.New("área pública cheia sem nenhum documento para apagar")
	}
	return nil
}

// removerPublico (com writeMu) apaga o arquivo e as pastas que ficarem vazias, e tira dos totais.
func (a *app) removerPublico(path string, size int64) error {
	if err := os.Remove(path); err != nil {
		return err
	}
	a.pubItens--
	a.pubBytes -= size
	a.hub.forget(docOf(a.docsDir, path))
	root := filepath.Join(a.docsDir, publico)
	for dir := filepath.Dir(path); strings.HasPrefix(dir, root+string(filepath.Separator)); dir = filepath.Dir(dir) {
		if os.Remove(dir) != nil {
			break // pasta ainda tem conteúdo
		}
		a.pubItens--
	}
	return nil
}

// docOf é o nome do documento guardado no arquivo path (o inverso de docPath).
func docOf(docsDir, path string) string {
	rel, _ := filepath.Rel(docsDir, path)
	return filepath.ToSlash(strings.TrimSuffix(rel, ".txt"))
}

// countEntries devolve os itens abaixo de dir e os bytes dos arquivos.
func countEntries(dir string) (n int, bytes int64) {
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && p != dir {
			n++
			if info, err := d.Info(); err == nil && !d.IsDir() {
				bytes += info.Size()
			}
		}
		return nil
	})
	return n, bytes
}

func (a *app) setPublic(w http.ResponseWriter, r *http.Request, u user) {
	name, ok := docName(w, r)
	if !ok {
		return
	}
	if !a.mustOwn(w, name, u) {
		return
	}
	if _, err := a.db.Exec(`UPDATE docs SET public = ? WHERE name = ?`, r.FormValue("public") == "1", name); err != nil {
		serverError(w, err)
		return
	}
	a.hub.recheck(name)
	http.Redirect(w, r, "/"+name, http.StatusSeeOther)
}

// apagarPublico (com writeMu) é a moderação de /publico: a área não tem dono, então quem apaga é o admin.
func (a *app) apagarPublico(w http.ResponseWriter, r *http.Request, name string, u user) {
	if !u.Admin {
		fail(w, http.StatusForbidden, "só administradores apagam documentos da área pública")
		return
	}
	a.hub.kill(name, "documento apagado")
	path := a.docPath(name)
	if size, existe := tamanhoDe(path); existe {
		a.medirPublico()
		if err := a.removerPublico(path, size); err != nil {
			serverError(w, err)
			return
		}
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *app) mustOwn(w http.ResponseWriter, name string, u user) bool {
	ac, err := a.acesso(name, u)
	if err != nil {
		serverError(w, err)
		return false
	}
	if !ac.Dono {
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
	out := []string{} // lista vazia sai [] no JSON
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// falha é um erro que a pessoa (ou o agente) pode ler; os outros vão para o log e chegam como "erro interno".
type falha string

func (f falha) Error() string { return string(f) }

func ehFalha(err error) bool {
	var f falha
	return errors.As(err, &f)
}

func render(w http.ResponseWriter, status int, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Print(err)
	}
}

// msgPage é a página de aviso; Aba, se houver, sai das abas de Me (documento apagado ou sem acesso).
type msgPage struct{ Msg, Me, Aba string }

func fail(w http.ResponseWriter, status int, msg string) { render(w, status, "msg", msgPage{Msg: msg}) }

func serverError(w http.ResponseWriter, err error) {
	log.Print(err)
	fail(w, http.StatusInternalServerError, "erro interno")
}
