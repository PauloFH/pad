# Conversas entre agentes pelo pad (MCP) — plano de implementação

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** o pad passa a ser um servidor MCP (Streamable HTTP em `POST /_mcp`, chave por máquina) com conversas em formato de chat numa área própria (`/_mcp`), mais ferramentas para os agentes lerem e escreverem documentos do pad.

**Architecture:** MCP implementado à mão dentro do binário (JSON-RPC sobre HTTP, só respostas `application/json`, sem SSE), com sessões em memória (`mcpState`). Conversas, mensagens, máquinas e "lido" ficam em tabelas novas no SQLite. O `esperar` e o chat do navegador fazem long-poll sobre um sinal em memória que fecha a cada mensagem nova. A escrita de documento pelo agente vira uma operação OT aplicada pelo servidor no `liveDoc`.

**Tech Stack:** Go 1.25, `net/http`, `modernc.org/sqlite`, `html/template`, JS puro em `static/` com `node --test`.

**Spec:** `docs/superpowers/specs/2026-11-04-mcp-conversas-design.md`

## Global Constraints

- **Nenhuma dependência nova.** O MCP é feito à mão, sem SDK. Decisão do plano: o subconjunto necessário é pequeno, e o projeto tem três dependências.
- **Versões do protocolo aceitas:** `2025-11-25`, `2025-06-18`, `2025-03-26`. Se o cliente pedir uma dessas, o servidor ecoa a versão pedida. Se pedir outra, responde `2025-06-18`.
- **Ordem de locks:** `a.writeMu` → `hub.mu` → `liveDoc.mu`, nunca `hub.mu` antes de `a.writeMu` (`hub.go`).
- **Teto do `esperar`:** 50 s (`var esperaMax = 50 * time.Second`, que os testes baixam).
- **Janela de "ativo":** 2 min (`const ativoJanela = 2 * time.Minute`).
- **Nomes de conversa, apelido e máquina:** `strings.ToLower(strings.TrimSpace(s))` validado com `validCurto = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,59}$`)`. O apelido também não pode ser igual a um login existente.
- **Mensagem:** no máximo 64 KiB (`const maxMensagem = 64 << 10`).
- **Rótulo do agente:** `<apelido> · <tipo> · <máquina>`. A pessoa aparece só pelo login.
- **Tipo do cliente:** se o `clientInfo.name` em minúsculas contém `claude`, o tipo é `claude`. Se contém `codex`, é `codex`. Senão, é o próprio nome, cortado em 20 caracteres.
- **Hora mostrada:** `t.In(time.Local).Format("02/01 15:04")`.
- **Erros de ferramenta** vão em `result.isError: true` com o texto em português. Erro de JSON-RPC só para protocolo (método desconhecido, JSON inválido).
- **Identificadores e textos em português,** como o resto do código. Comentários só para o porquê.
- **Testes:** `go test ./...` e `node --test static/*.test.js` passam ao fim de cada task.
- **Commits sem linha de coautor.**

## Review Focus

1. **Servidor reiniciado com o agente conectado:** o cliente manda um `Mcp-Session-Id` que não existe mais. Esperado: 404, o cliente refaz o `initialize`, e as ferramentas pedem `apresentar` de novo. Teste na Task 3 (`TestMCPSessaoDesconhecida`).
2. **Máquina revogada no meio de um `esperar`:** a chamada volta com erro em menos de 1 s, não em 50 s. Teste na Task 2/3 (`TestMCPRevogaDuranteEsperar`).
3. **Mensagem com HTML ou script no chat:** aparece como texto literal, nunca é interpretada. Teste na Task 6 (`partes` em `static/chat.test.js`, e o DOM só usa `textContent`).
4. **Agente escreve num trecho que a pessoa selecionou no navegador:** recusa com "trecho travado por ana", sem mexer no texto. Teste na Task 4 (`TestEscreverTrechoTravado`).
5. **Desligamento do servidor (SIGTERM) com agentes esperando:** os `esperar` voltam na hora com "nada ainda", e o desligamento não fica preso 50 s. Teste na Task 3 (`TestMCPEncerrarAcordaEspera`).

---

## Arquivos

| arquivo | responsabilidade |
|---|---|
| `maquina.go` (novo) | máquinas: criar chave, autenticar pela chave, listar, revogar |
| `conversa.go` (novo) | conversas e mensagens no SQLite, regra de "lido", sinal de novidade |
| `mcp.go` (novo) | protocolo MCP: sessões, `initialize`, `tools/*`, `ping`, as ferramentas de conversa |
| `mcpdoc.go` (novo) | `ler_documento`, `escrever_documento`, `resources/list`, `resources/read` |
| `mcpweb.go` (novo) | páginas e rotas do navegador em `/_mcp` |
| `live.go` / `hub.go` | aplicar operação do servidor no `liveDoc` (sem aba) |
| `main.go` | schema, rotas, `app.mcp`, `RegisterOnShutdown` |
| `templates.html` | `mcp` (painel), `mcp-conversa` (chat), item **mcp** no menu da conta |
| `static/chat.js`, `static/chat.test.js` (novos) | chat e atualização do painel; `partes(texto)` testável |
| `static/componentes.css` | estilos do painel e dos balões |
| `README.md` | seção "Agentes (MCP)" |

---

### Task 1: Máquinas e chaves

**Files:**
- Create: `maquina.go`, `maquina_test.go`
- Modify: `main.go` (constante `schema`)

**Interfaces:**
- Produces:
  - `type maquina struct { ID int64; Nome string; Criada, Vista time.Time }`
  - `var validCurto = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,59}$`)` e `func curto(s string) (string, bool)`, que normaliza (minúsculas e trim) e valida
  - `var errMaquinaExiste = errors.New("já existe uma máquina com esse nome")`
  - `func (a *app) criarMaquina(uid int64, nome string) (chave string, err error)`
  - `func (a *app) maquinaDaChave(chave string) (m maquina, u user, ok bool, err error)`
  - `func (a *app) listarMaquinas(uid int64) ([]maquina, error)`, em ordem de nome
  - `func (a *app) revogarMaquina(uid, id int64) error`. Só apaga se a máquina for de `uid`; a Task 3 acrescenta derrubar as sessões.
  - `func (a *app) marcarVista(id int64, now time.Time) error`

- [ ] **Step 1: Escrever os testes que falham**

```go
func TestMaquinas(t *testing.T) {
	a := appComUsuarios(t, "ana", "bia") // helper novo neste arquivo: openApp(t.TempDir()) + useradd
	chave, err := a.criarMaquina(1, " Notebook ")
	// err == nil; strings.HasPrefix(chave, "pad_"); len(chave) > 20
	_, err = a.criarMaquina(1, "notebook")      // errors.Is(err, errMaquinaExiste)
	_, err = a.criarMaquina(2, "notebook")      // nil: o nome é por usuário
	_, err = a.criarMaquina(1, "com espaço")    // erro "nome inválido"
	m, u, ok, err := a.maquinaDaChave(chave)    // ok; m.Nome == "notebook"; u.Name == "ana"
	_, _, ok, _ = a.maquinaDaChave("pad_errada") // !ok
	// a chave não fica no banco: SELECT count(*) FROM mcp_maquinas WHERE hash = ? com []byte(chave) dá 0
	ms, _ := a.listarMaquinas(1)                // len 1
	a.revogarMaquina(2, ms[0].ID)               // não é dela: nada muda
	_, _, ok, _ = a.maquinaDaChave(chave)       // ainda ok
	a.revogarMaquina(1, ms[0].ID)
	_, _, ok, _ = a.maquinaDaChave(chave)       // !ok
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test -run TestMaquinas .`
Expected: FAIL (`criarMaquina` não definido).

- [ ] **Step 3: Implementar**

Acrescentar no `schema`:

```sql
CREATE TABLE IF NOT EXISTS mcp_maquinas (
	id INTEGER PRIMARY KEY,
	user_id INTEGER NOT NULL REFERENCES users ON DELETE CASCADE,
	nome TEXT NOT NULL,
	hash BLOB NOT NULL UNIQUE,
	criada INTEGER NOT NULL,
	vista INTEGER NOT NULL DEFAULT 0,
	UNIQUE (user_id, nome));
```

A chave é `"pad_" + rand.Text()`, e o banco guarda `sha256.Sum256(chave)`. O `maquinaDaChave` busca pelo hash com JOIN em `users`. O `marcarVista` só grava se a última gravação tiver mais de 1 min. O controle desse 1 min fica num campo em memória, para não gravar a cada chamada.

- [ ] **Step 4: Rodar e ver passar**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add maquina.go maquina_test.go main.go
git commit -m "feat: máquinas com chave para o MCP"
```

---

### Task 2: Conversas, mensagens e "lido"

**Files:**
- Create: `conversa.go`, `conversa_test.go`
- Modify: `main.go` (`schema`, campo `mcp *mcpState` no `app`, criado no `openApp`)

**Interfaces:**
- Consumes: `curto` (Task 1)
- Produces:
  - `type mensagem struct { ID int64; Autor, Rotulo string; Humano bool; Em time.Time; Texto string }`. `Autor` é o apelido ou o login, e é a chave do "outro autor". `Rotulo` é o que aparece.
  - `type conversaRow struct { Nome, UltimoRotulo string; UltimaEm time.Time; NaoLidas int }`
  - `var errConversaExiste`, `errSemConversa` ("conversa não encontrada"), `errMsgGrande` ("mensagem passa de 64 KB"), `errMsgVazia`
  - `func (a *app) criarConversa(uid int64, nome string, autor mensagem) error`. A primeira mensagem é `autor`, e o `Texto` vai nela.
  - `func (a *app) enviarMensagem(uid int64, conversa string, m mensagem) (int64, error)`. Chama `a.mcp.avisar()`.
  - `func (a *app) mensagens(uid int64, conversa string, depois int64) ([]mensagem, error)`, para o navegador, em ordem de ID
  - `func (a *app) lerConversa(uid int64, conversa, apelido string, tudo bool) ([]mensagem, error)`
  - `func (a *app) novidades(uid int64, conversas []string, apelido string) (map[string][]mensagem, error)`
  - `func (a *app) listarConversas(uid int64, apelido string) ([]conversaRow, error)`, da mais recente para a mais antiga
  - `func (a *app) apagarConversa(uid int64, nome string) error`
  - `type mcpState struct` com `mu sync.Mutex` e `novidade chan struct{}` (a Task 3 acrescenta as sessões)
  - `func (m *mcpState) sinal() <-chan struct{}` (pegar **antes** de consultar o banco) e `func (m *mcpState) avisar()` (fecha o canal e cria outro)

Regras, copiadas do spec:
- `lerConversa`: se não há linha em `mcp_lido` para (conversa, apelido), ou se `tudo` vem verdadeiro, devolve todas as mensagens, inclusive as próprias. Senão, devolve as de outros autores com `id > ate`. Nos dois casos, grava `ate = maior id da conversa`.
- `novidades`: para cada conversa (lista vazia = todas do `uid`), `desde` é o `ate` de `mcp_lido`. Sem linha, `desde` é o id da última mensagem do próprio apelido ali, ou 0 se ele nunca falou. Devolve as mensagens de outros autores com `id > desde`, só das conversas com alguma, e grava `ate`.
- `NaoLidas` em `listarConversas` usa o mesmo `desde` do `novidades`.

- [ ] **Step 1: Escrever os testes que falham**

```go
func TestConversaLido(t *testing.T) {
	a := appComUsuarios(t, "ana")
	pai := mensagem{Autor: "pai", Rotulo: "pai · claude · desktop", Texto: "faz o login"}
	a.criarConversa(1, "login", pai)                                 // nil
	// errors.Is(a.criarConversa(1, "login", pai), errConversaExiste)
	ms, _ := a.lerConversa(1, "login", "filho", false)               // len 1 (primeira leitura: tudo)
	ms, _ = a.lerConversa(1, "login", "filho", false)                // len 0
	a.enviarMensagem(1, "login", mensagem{Autor: "filho", Rotulo: "filho · codex · note", Texto: "dúvida"})
	ms, _ = a.lerConversa(1, "login", "filho", false)                // len 0: própria não volta
	nv, _ := a.novidades(1, nil, "pai")                               // nv["login"] tem 1, Texto "dúvida"
	nv, _ = a.novidades(1, nil, "pai")                                // len(nv) == 0
	a.enviarMensagem(1, "login", mensagem{Autor: "ana", Humano: true, Rotulo: "ana", Texto: "eu"})
	nv, _ = a.novidades(1, []string{"login"}, "pai")                  // 1 mensagem, Humano
	ms, _ = a.lerConversa(1, "login", "filho", true)                  // len 3 (tudo)
	// apelido novo que já falou: só o que veio depois da última fala dele
	a.enviarMensagem(1, "login", mensagem{Autor: "outro", Rotulo: "outro · claude · x", Texto: "oi"})
	a.enviarMensagem(1, "login", mensagem{Autor: "ana", Humano: true, Rotulo: "ana", Texto: "depois"})
	nv, _ = a.novidades(1, nil, "outro")                              // só "depois"
	// errors.Is(enviarMensagem(..., Texto: strings.Repeat("x", 64<<10+1)), errMsgGrande)
	// errors.Is(enviarMensagem(1, "nao-existe", ...), errSemConversa)
}

func TestSinalNovidade(t *testing.T) {
	a := appComUsuarios(t, "ana")
	s := a.mcp.sinal()
	a.criarConversa(1, "c", mensagem{Autor: "pai", Rotulo: "pai", Texto: "x"})
	// select { case <-s: ok; case <-time.After(time.Second): t.Fatal }
}
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test -run 'TestConversaLido|TestSinalNovidade' .`
Expected: FAIL

- [ ] **Step 3: Implementar**

```sql
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
CREATE TABLE IF NOT EXISTS mcp_lido (
	conversa_id INTEGER NOT NULL REFERENCES mcp_conversas ON DELETE CASCADE,
	apelido TEXT NOT NULL, ate INTEGER NOT NULL,
	PRIMARY KEY (conversa_id, apelido));
```

O `criarConversa` insere a conversa e a primeira mensagem numa transação. Texto vazio, ou só com espaços, dá `errMsgVazia`.

- [ ] **Step 4: Rodar e ver passar**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add conversa.go conversa_test.go main.go
git commit -m "feat: conversas e mensagens com estado de lido por apelido"
```

---

### Task 3: Protocolo MCP e ferramentas de conversa

**Files:**
- Create: `mcp.go`, `mcp_test.go`
- Modify: `main.go` (rotas `POST /_mcp` e `DELETE /_mcp`; `srv.RegisterOnShutdown(a.mcp.encerrar)` em `main`), `maquina.go` (o `revogarMaquina` derruba as sessões e chama `avisar`), `conversa.go` (o `mcpState` ganha as sessões)

**Interfaces:**
- Consumes: Tasks 1 e 2
- Produces:
  - `type sessao struct { id string; uid int64; login string; maquinaID int64; maquina, tipo, apelido string; ultima time.Time; esperando int; conversas map[string]time.Time; leituras map[string]leitura }` (`leitura` vem na Task 4; aqui pode ser declarada vazia)
  - `func (s *sessao) rotulo() string`, que devolve `apelido + " · " + tipo + " · " + maquina`
  - `func tipoDoCliente(nome string) string`
  - `func (m *mcpState) conectados(uid int64, now time.Time) []agenteView`, com `type agenteView struct { Rotulo, Apelido, Maquina, Tipo string; Conversas []string; Ultima time.Time }`. Ativo é `ultima` nos últimos `ativoJanela`, ou `esperando > 0`. A lista vem em ordem de máquina e depois de apelido.
  - `func (m *mcpState) agentesNa(uid int64, conversa string, now time.Time) []string`: os rótulos ativos naquela conversa
  - `func (m *mcpState) derrubar(maquinaID int64)` e `func (m *mcpState) encerrar()`
  - `func (a *app) mcpRPC(w http.ResponseWriter, r *http.Request)` e `func (a *app) mcpFim(w, r)` (DELETE)
  - `const instrucoes` (texto abaixo)

Comportamento HTTP:
- **Origin:** se o header `Origin` existe e o host dele é diferente de `r.Host`, responde 403.
- **Autenticação:** `Authorization: Bearer <chave>`, via `maquinaDaChave`. Faltando ou inválida: 401 com `WWW-Authenticate: Bearer`. A cada chamada aceita, chama `marcarVista`.
- **Corpo:** um objeto JSON-RPC (até 1 MiB). Array ou JSON inválido: erro `-32700`/`-32600`.
- **Notificação** (sem `id`): 202 sem corpo.
- **`initialize`:** cria a sessão (id `rand.Text()`) e devolve o header `Mcp-Session-Id`. O resultado é `{protocolVersion, capabilities:{tools:{}, resources:{}}, serverInfo:{name:"pad", version:"1"}, instructions}`. O `tipo` vem de `params.clientInfo.name`. Cada máquina tem no máximo 20 sessões; ao passar disso, cai a de `ultima` mais antiga.
- **Demais métodos:** exigem `Mcp-Session-Id` de uma sessão que existe e é da mesma máquina. Se não for, 404.
- **`ping`** → `{}`. **`tools/list`** → as ferramentas com `inputSchema`. **`tools/call`** → `{content:[{type:"text", text}], isError}`. Método desconhecido → `-32601`.
- **`DELETE /_mcp`** com sessão válida: apaga a sessão e responde 204.
- Toda chamada atualiza `s.ultima`. As ferramentas que citam uma conversa atualizam `s.conversas[nome]`.

Ferramentas desta task (as de documento vêm na Task 4):

| nome | argumentos (`inputSchema`) | resposta |
|---|---|---|
| `apresentar` | `apelido` string, obrigatório | `"você é <rótulo>"` |
| `conversas` | — | uma linha por conversa: `nome · última: <rótulo> 04/11 14:32 · 2 não lidas` |
| `criar_conversa` | `nome`, `mensagem` | `"conversa <nome> criada"` |
| `enviar` | `conversa`, `texto` | `"enviada (#<id>)"` |
| `ler` | `conversa`, `tudo` boolean opcional | `formatarMensagens`, ou `"nada novo"` |
| `esperar` | `conversas` array de string opcional, `segundos` integer opcional (padrão e teto 50) | as novidades agrupadas por conversa, ou `"nada ainda"` |

- Sem apelido, todas menos `apresentar` respondem `isError` com "apresente-se com `apresentar` primeiro".
- O `apresentar` recusa nestes casos: apelido inválido; apelido igual a um login (`SELECT 1 FROM users WHERE name = ?`); outra sessão **ativa** do mesmo `uid` já com esse apelido ("apelido em uso por <tipo> · <máquina>"). Se a sessão que tinha o apelido estiver inativa, ela perde o apelido.
- `func formatarMensagens(ms []mensagem) string`: um bloco por mensagem, separados por linha em branco, cada um assim:

```
#12 · 04/11 14:32 · filho · codex · note
texto da mensagem
```

  No `esperar` com várias conversas, cada grupo começa com `== <conversa> ==`.
- O `esperar` pega `sinal()`, consulta `novidades` e, se não há nada, espera o primeiro destes eventos: o sinal, o fim do prazo, `r.Context().Done()` ou o `encerrar`. Depois de acordar, confere se a sessão ainda existe. Se a máquina foi revogada, responde `isError` com "máquina revogada". Enquanto espera, `s.esperando` fica com 1 a mais.

Texto exato de `instrucoes`:

```
pad: canal de mensagens entre agentes (Claude Code, Codex) desta conta.
Antes de tudo, chame `apresentar` com seu apelido: o agente central usa "pai"; um filho usa o nome da conversa da task dele.
Pai: crie uma conversa por task com `criar_conversa` (a task vai na primeira mensagem), ouça todas com `esperar` sem lista de conversas e responda com `enviar`.
Filho: use `ler` na sua conversa para pegar a task e mande dúvidas e resultados com `enviar`.
Se uma dúvida trava o trabalho, chame `esperar` com a sua conversa em laço até chegar resposta: cada chamada volta em até 50 s, com "nada ainda" se ninguém respondeu. Se não trava, siga trabalhando e confira com `ler` entre um passo e outro.
`@pad /caminho` numa mensagem é um documento do pad: abra com `ler_documento`. Para mudar um documento, use `escrever_documento` com a versão que você leu.
Se perder o contexto, chame `ler` com tudo=true.
```

- [ ] **Step 1: Escrever os testes que falham**

Helper em `mcp_test.go`: `mcpCli(t, srv, chave, clientName) *mcpCliente`. Ele faz o `initialize` e guarda o `Mcp-Session-Id`. Tem `call(method string, params any) (result json.RawMessage, status int)` e `tool(nome string, args any) (texto string, isError bool)`.

```go
func TestMCPInitialize(t *testing.T) // protocolVersion ecoa "2025-06-18"; pedido "1999-01-01" → "2025-06-18";
                                     // header Mcp-Session-Id presente; instructions contém "apresentar"
func TestMCPAuth(t *testing.T)       // sem Authorization → 401 com WWW-Authenticate; chave errada → 401;
                                     // Origin "https://mal.example" → 403
func TestMCPSessaoDesconhecida(t *testing.T) // tools/list com Mcp-Session-Id "x" → 404
func TestMCPNotificacao(t *testing.T)        // {"jsonrpc":"2.0","method":"notifications/initialized"} → 202
func TestMCPApresentar(t *testing.T)
	// sem apresentar: tool("conversas") → isError, texto contém "apresentar"
	// tool("apresentar", {apelido:"Ana"}) com login ana existente → isError
	// "pai" no cliente "claude-code" da máquina "desktop" → "você é pai · claude · desktop"
	// segundo cliente "codex-mcp-client" pedindo "pai" → isError contém "apelido em uso por claude · desktop"
	// depois de 2 min sem atividade do primeiro (mexer em s.ultima pelo a.mcp), o segundo consegue
func TestMCPConversaPaiFilho(t *testing.T)
	// pai cria "login" com "faz o login"; filho (codex, outra máquina) apresentar "login", ler "login" → contém "faz o login"
	// pai esperar {segundos:5} em goroutine; filho enviar "dúvida"; pai volta em < 2 s com "== login ==" e "dúvida"
	// conectados(uid) lista 2 agentes, um Tipo "claude" e um "codex"
func TestMCPEsperarPrazo(t *testing.T)       // esperaMax = 200ms; esperar → "nada ainda" em < 1 s
func TestMCPRevogaDuranteEsperar(t *testing.T) // esperar em goroutine; revogarMaquina; volta isError "máquina revogada" em < 1 s;
                                               // próxima chamada → 401
func TestMCPEncerrarAcordaEspera(t *testing.T) // esperar {segundos:50}; a.mcp.encerrar(); volta "nada ainda" em < 1 s
func TestTipoDoCliente(t *testing.T)         // "claude-code"→"claude", "Codex-MCP-Client"→"codex", "cursor"→"cursor",
                                             // 30 letras → 20
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test -run 'TestMCP|TestTipoDoCliente' .`
Expected: FAIL

- [ ] **Step 3: Implementar `mcp.go` e as mudanças em `maquina.go`, `conversa.go` e `main.go`**

Rotas: `mux.HandleFunc("POST /_mcp", a.mcpRPC)` e `mux.HandleFunc("DELETE /_mcp", a.mcpFim)`. O `GET /_mcp` é a página da Task 5. Se esse `GET` chegar com `Authorization`, responde 405 (é o cliente MCP tentando abrir SSE).

- [ ] **Step 4: Rodar e ver passar**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add mcp.go mcp_test.go main.go maquina.go conversa.go
git commit -m "feat: servidor MCP com sessões e ferramentas de conversa"
```

---

### Task 4: Documentos pelo MCP (`ler_documento`, `escrever_documento`, resources)

**Files:**
- Create: `mcpdoc.go`, `mcpdoc_test.go`
- Modify: `live.go` (extrair de `submit` o trecho comum de aplicar uma op), `mcp.go` (registrar as ferramentas e os métodos `resources/list` e `resources/read`; `capabilities.resources` já vem anunciado da Task 3)

**Interfaces:**
- Consumes: `sessao` (Task 3), `hub.open`/`hub.leave`/`hub.armFlush`, `Transform`, `Op.Apply`
- Produces:
  - `type leitura struct { doc *liveDoc; rev int; texto []uint16; versao string }`, guardada em `s.leituras[nome]` (só a última leitura de cada documento)
  - `func caminhoDoc(s string) (string, bool)`. Aceita `/ana/notas`, `ana/notas`, `@pad /ana/notas`, `https://host/ana/notas` e `pad://ana/notas`. Devolve `ana/notas` se passar em `validName` e em `maxDepth`.
  - `func diffOp(de, para []uint16) Op`: retain do prefixo comum, delete do miolo antigo, insert do miolo novo e retain do sufixo comum. O corte nunca separa um par substituto (UTF-16).
  - `func (d *liveDoc) aplicar(from *peer, rev int, op Op) (Op, error)`: transforma por `since(rev)`, confere as travas, aplica, confere o tamanho, sobe `rev`, grava no `log`, marca `dirty` e move os cursores. É o miolo do `submit`, que passa a chamar este. Com `from == nil`, o nome na trava é `q.login` ou `q.visitor`.
  - `func (a *app) lerDocumento(s *sessao, caminho string) (texto, versao string, err error)`
  - `func (a *app) escreverDocumento(s *sessao, caminho, texto, versao string) error`

Regras:
- **Ler** é permitido em: área `/publico`; `access(...).allowed`; ou documento com `public`. Senão: "documento não encontrado ou sem acesso". Documento inexistente responde "documento não existe; para criar, use escrever_documento com versao vazia".
- **Escrever** é permitido em: área `/publico` ou `allowed`. Também pode criar, com `versao == ""`, dentro de `s.login + "/"` ou no `/publico`, igual ao `save` e ao `liveAccess`.
- A versão é `fmt.Sprintf("%d-%x", rev, sha256(texto)[:6])`.
- `escreverDocumento`:
  1. Pega a leitura: `s.leituras[nome]` com a mesma `versao`. Com `versao == ""`, a base é o texto vazio e `doc` é nil. Sem leitura que bata, responde "documento mudou desde a sua leitura, leia de novo".
  2. Monta `op := diffOp(base.texto, novo)`.
  3. Abre o documento com `hub.open(nome)`. Se for criar, faz o `INSERT ... ON CONFLICT DO NOTHING` sob `writeMu` depois do `open`, como no `ws.go`.
  4. Sob `d.mu`: se `base.doc == d` e `base.rev >= d.rev-len(d.log)`, chama `d.aplicar(nil, base.rev, op)`. Senão, se `d.text` é igual a `base.texto`, chama `d.aplicar(nil, d.rev, op)`. Senão, recusa com a mesma mensagem do passo 1.
  5. Se deu certo, manda a op às abas como o `ws.go` faz (`{"t":"op","rev":d.rev,"op":op,"by":""}`), chama `armFlush` e, ao sair, `hub.leave(d, nil)`. O `leave` com peer nil é seguro: `delete` de chave nil não faz nada.
  6. Atualiza `s.leituras[nome]` com o texto e a revisão novos. Assim o agente pode escrever de novo sem reler.
- Erros do `aplicar` vão para o agente: `errLocked` ("trecho travado por ana") e `errTooBig`.
- **`resources/list`:** os documentos que o usuário acessa (`userDocs(u)` do `main.go`), cada um como `{uri:"pad://"+nome, name:"/"+nome, mimeType:"text/plain"}`. Sem paginação. *shortcut: lista até 1000; paginar se alguém passar disso.*
- **`resources/read`** `{uri}` → `{contents:[{uri, mimeType:"text/plain", text}]}`, com as mesmas regras de leitura. Não registra leitura em `s.leituras`.
- Ferramentas: `ler_documento {caminho}` devolve `"versao: <v>\n\n<texto>"`. `escrever_documento {caminho, texto, versao}` devolve `"gravado (versao: <v>)"`.

- [ ] **Step 1: Escrever os testes que falham**

```go
func TestCaminhoDoc(t *testing.T)   // os 5 formatos → "ana/notas"; "../x", "/ana/" e 7 níveis → false
func TestDiffOp(t *testing.T)       // "abc"→"abXc" = [retain 2, insert "X", retain 1]; ""→"x"; "x"→"";
                                    // "a😀b"→"a😃b" não corta o par; Apply(diffOp(a,b), a) == b em 200 pares aleatórios
func TestLerEscreverDocumento(t *testing.T)
	// ana cria /ana/notas "um" via POST; agente ler_documento "@pad /ana/notas" → contém "um" e "versao: "
	// escrever_documento com a versão lida, texto "um dois" → ok; GET /ana/notas mostra "um dois" (depois do flush)
	// escrever_documento de novo com a versão VELHA → "leia de novo"
	// /bia/x sem compartilhamento → "sem acesso"; criar "/ana/novo" com versao "" → ok e o doc tem dono ana
	// criar "/bia/novo" → recusa
func TestEscreverJuntaComQuemDigita(t *testing.T)
	// ana abre /ana/notas "linha1\nlinha2" no WS (dialWS); agente ler_documento
	// ana digita "X" no começo (tc.edit) e recebe o ack
	// agente escreve "linha1\nlinha2\nlinha3" com a versão antiga → ok
	// ana recebe a op; tc.text == "Xlinha1\nlinha2\nlinha3"
func TestEscreverTrechoTravado(t *testing.T)
	// ana no WS seleciona "linha2" (sel a/h); agente escreve trocando linha2 → isError "trecho travado por ana"; texto igual
func TestResources(t *testing.T)    // resources/list contém uri "pad://ana/notas"; resources/read devolve o texto
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test -run 'TestCaminhoDoc|TestDiffOp|TestLerEscrever|TestEscrever|TestResources' .`
Expected: FAIL

- [ ] **Step 3: Implementar**

Antes de mexer no `submit`, rodar `go test -run 'TestWS|TestLive' .` e confirmar que passa. A extração do `aplicar` não pode mudar o comportamento.

- [ ] **Step 4: Rodar e ver passar**

Run: `go test ./... && node --test static/*.test.js`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add mcpdoc.go mcpdoc_test.go live.go mcp.go
git commit -m "feat: agentes leem e escrevem documentos pelo MCP"
```

---

### Task 5: Painel `/_mcp` no navegador: máquinas e conectados

**Files:**
- Create: `mcpweb.go`, `mcpweb_test.go`, `static/chat.js` (só a parte do painel nesta task)
- Modify: `main.go` (rotas), `templates.html` (define `mcp`; item **mcp** no menu `conta`, entre "documentos" e "convidar", para todo usuário), `static/componentes.css`

**Interfaces:**
- Consumes: Tasks 1–3
- Produces:
  - `type painelPage struct { Me string; Admin bool; Conversas []conversaRow; Agentes map[string][]string; Maquinas []maquina; Conectados []agenteView; Resumo string; Nova *novaChave; Err string }`
  - `type novaChave struct { Nome, Chave, Claude, Codex string }`
  - `func resumo(ags []agenteView) string`: `"nenhum agente conectado"`, `"1 agente: 1 Claude"` ou `"5 agentes: 3 Claude, 2 Codex"`. Os tipos aparecem em ordem alfabética, com maiúscula. Outros tipos aparecem pelo próprio nome.

Rotas (todas com `a.auth`):

| rota | faz |
|---|---|
| `GET /_mcp` | painel. Se a requisição tem `Authorization`, responde 405 (ver Task 3) |
| `POST /_mcp/maquinas` | form `nome`. Cria a máquina e **renderiza** o painel com `Nova` preenchido (sem redirect: a chave aparece uma vez). Erro vira `Err`. |
| `POST /_mcp/maquinas/{id}/revogar` | revoga e redireciona 303 para `/_mcp` |
| `GET /_mcp/conectados` | JSON `{"resumo": string, "agentes": [{"rotulo","maquina","tipo","conversas":[...],"ultima": "04/11 14:32"}]}` |

Comandos mostrados em `Nova`. A base é `scheme://r.Host`, com o `scheme` vindo de `isHTTPS(r)`:

```
claude mcp add --scope user --transport http pad <base>/_mcp --header "Authorization: Bearer <chave>"
```

```
# ~/.codex/config.toml
[mcp_servers.pad]
url = "<base>/_mcp"
bearer_token_env_var = "PAD_MCP_CHAVE"
# e no shell: export PAD_MCP_CHAVE=<chave>
```

Conferir a sintaxe do Codex na documentação atual (context7 ou a doc do Codex) antes de fixar o texto. Se o nome do campo for outro, o teste abaixo muda junto.

Tela, no visual da moldura (`body.ed`, `header.topo` com `marca` e o template `conta`):
- **Conversas:** os `cartoes` com nome, última mensagem e os agentes ativos de `Agentes[nome]`. Um form com nome e primeira mensagem faz `POST /_mcp/c` (Task 6).
- **Máquinas:** tabela com nome, cadastrada e vista por último (`quando` de `entrada.go`), mais um botão de revogar. Embaixo, o form de cadastro.
- **Agora conectados:** o `Resumo` e uma tabela com apelido, máquina, tipo, conversas e última atividade. O `chat.js` refaz essa tabela a cada 5 s com `GET /_mcp/conectados`, montando com `textContent`.
- **`Nova`:** a chave num `input readonly` e os dois blocos de comando em `<pre>`, com o mesmo botão Copiar do convite. O aviso diz: "Guarde agora: a chave não aparece de novo."

- [ ] **Step 1: Escrever os testes que falham**

```go
func TestPainelMCP(t *testing.T)
	// GET /_mcp sem login → redireciona ao login
	// ana: POST /_mcp/maquinas nome=notebook → 200, corpo contém "pad_", "claude mcp add", "bearer_token_env_var",
	//      "Guarde agora"
	// GET /_mcp depois → contém "notebook" e NÃO contém a chave
	// bia: GET /_mcp não contém "notebook"; POST /_mcp/maquinas/<id da ana>/revogar → a máquina continua
	// com um agente conectado (mcpCli da Task 3): GET /_mcp/conectados → resumo "1 agente: 1 Claude"
	// POST revogar da ana → 303; maquinaDaChave → !ok
	// GET /_mcp com header Authorization → 405
	// página de documento traz o link href="/_mcp" no menu da conta
func TestResumo(t *testing.T) // 0, 1 claude, 3 claude + 2 codex, 1 cursor → as frases acima ("1 agente: 1 cursor")
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test -run 'TestPainelMCP|TestResumo' .`
Expected: FAIL

- [ ] **Step 3: Implementar**

- [ ] **Step 4: Rodar e ver passar; abrir no navegador**

Run: `go test ./... && node --test static/*.test.js`
Expected: PASS

Depois, `PAD_DATA=./data go run .` e abrir `http://localhost:8080/_mcp` logado: cadastrar uma máquina e ver a chave e os comandos. Conferir em 360 px de largura e em dois temas (Papel e Grafite).

- [ ] **Step 5: Commit**

```bash
git add mcpweb.go mcpweb_test.go main.go templates.html static/chat.js static/componentes.css
git commit -m "feat: painel MCP com máquinas e agentes conectados"
```

---

### Task 6: Chat de cada conversa

**Files:**
- Modify: `mcpweb.go`, `mcpweb_test.go`, `templates.html` (define `mcp-conversa`), `static/chat.js`, `static/componentes.css`, `main_test.go` (`TestPaginaCarregaScripts`, se ela listar os scripts por página)
- Create: `static/chat.test.js`

**Interfaces:**
- Consumes: Task 2 (`mensagens`, `enviarMensagem`, `criarConversa`, `apagarConversa`), Task 3 (`agentesNa`, `sinal`)
- Produces (JS, exportado como no `shell.js`/`presence.js` para o `node --test`):
  - `partes(texto) → [{t:'texto'|'codigo'|'link', v: string, href?: string}]`. Trechos entre ```` ``` ```` viram `codigo`, sem a linha da linguagem. Fora do código, viram `link`: `@pad /x/y`, caminhos `/usuario/doc` (a regra de `validUser` seguida de um ou mais `/trecho`) e `http(s)://...`. O `href` de `@pad /x/y` é `/x/y`.
  - `quando(unix) → "04/11 14:32"`, no fuso do navegador

Rotas (com `a.auth`):

| rota | faz |
|---|---|
| `POST /_mcp/c` | form `nome`, `mensagem`. Cria com autor `{Autor: u.Name, Rotulo: u.Name, Humano: true}` e redireciona 303 para `/_mcp/c/<nome>`. Erro: painel com `Err`. |
| `GET /_mcp/c/{nome}` | página do chat. 404 com `msg` se a conversa não existe para o usuário |
| `GET /_mcp/c/{nome}/msgs?depois=<id>&esperar=1` | JSON `{"mensagens":[{"id","rotulo","humano","em","texto"}], "agentes":[rótulos]}`. Com `esperar=1` e nada novo, segura até 25 s (`var esperaWeb = 25 * time.Second`) no `sinal()` ou até o cliente sair. |
| `POST /_mcp/c/{nome}/msgs` | corpo JSON `{"texto"}`. Grava como o usuário e responde 204. Mensagem grande demais: 413 com o texto do erro. Vazia: 400. |
| `POST /_mcp/c/{nome}/apagar` | apaga e redireciona 303 para `/_mcp` |

Tela:
- Topo com `← mcp`, o nome da conversa e os agentes ativos (atualizados a cada resposta de `msgs`). Botão "apagar conversa" num `<form>` de confirmação em duas etapas: o primeiro clique troca o texto para "confirmar apagar". Não usar `confirm()`.
- Lista de balões: `humano` à direita e agente à esquerda, com rótulo e hora acima do texto. O texto é montado só com `textContent` a partir de `partes`. `codigo` vai em `<pre>` com `var(--mono)`, e `link` em `<a>`. `white-space: pre-wrap`.
- Rodapé com `<textarea>` e botão Enviar. Enter envia e Shift+Enter quebra a linha. Enquanto envia, o botão fica desabilitado. Se der erro, o texto continua na caixa e o erro aparece num `role="status"`.
- Ao abrir, chama `msgs?depois=0` e rola até o fim. Depois fica em laço com `esperar=1`. Se der erro de rede, tenta de novo em 3 s. Só rola sozinho se a pessoa já estava no fim.

- [ ] **Step 1: Escrever os testes que falham**

```js
// static/chat.test.js
test('partes: texto, código e links', () => {
  assert.deepEqual(partes('oi\n```go\nx := 1\n```\nfim'),
    [{t:'texto', v:'oi\n'}, {t:'codigo', v:'x := 1\n'}, {t:'texto', v:'\nfim'}])
  assert.deepEqual(partes('veja @pad /ana/notas ok'),
    [{t:'texto', v:'veja '}, {t:'link', v:'@pad /ana/notas', href:'/ana/notas'}, {t:'texto', v:' ok'}])
  assert.deepEqual(partes('https://x.dev/a'), [{t:'link', v:'https://x.dev/a', href:'https://x.dev/a'}])
})
test('partes: HTML fica texto', () => {
  assert.deepEqual(partes('<script>alert(1)</script>'), [{t:'texto', v:'<script>alert(1)</script>'}])
})
test('partes: crases sem fechar ficam texto', () => {
  assert.deepEqual(partes('a ```b'), [{t:'texto', v:'a ```b'}])
})
```

```go
func TestChatMCP(t *testing.T)
	// ana: POST /_mcp/c nome=login mensagem=oi → 303 /_mcp/c/login
	// GET /_mcp/c/login/msgs?depois=0 → 1 mensagem, humano true, rotulo "ana"
	// bia: GET /_mcp/c/login → 404; GET .../msgs → 404
	// long-poll: GET ...msgs?depois=<id>&esperar=1 em goroutine; agente (mcpCli) enviar "resposta"; volta em < 2 s com ela
	//   e agentes contém o rótulo do agente
	// esperaWeb = 200ms: long-poll sem novidade volta 200 com mensagens [] em < 1 s
	// POST .../msgs {"texto":""} → 400; texto de 64 KiB + 1 → 413
	// agente: esperar {conversas:["login"]} recebe a mensagem que ana mandou pelo navegador
	// POST .../apagar → 303; GET /_mcp/c/login → 404
```

- [ ] **Step 2: Rodar e ver falhar**

Run: `go test -run TestChatMCP . ; node --test static/chat.test.js`
Expected: FAIL

- [ ] **Step 3: Implementar**

- [ ] **Step 4: Rodar e ver passar; abrir no navegador**

Run: `go test ./... && node --test static/*.test.js`
Expected: PASS

Depois, com `go run .`, abrir a mesma conversa em duas abas e mandar mensagens das duas. A mensagem tem que aparecer na outra aba em ~1 s. Conferir em 360 px e nos temas Papel e Grafite.

- [ ] **Step 5: Commit**

```bash
git add mcpweb.go mcpweb_test.go templates.html static/chat.js static/chat.test.js static/componentes.css main_test.go
git commit -m "feat: chat das conversas MCP no navegador"
```

---

### Task 7: README e teste com os clientes de verdade

**Files:**
- Modify: `README.md`, e o que o teste manual revelar (por exemplo, o mapeamento em `tipoDoCliente` ou o texto do comando do Codex)

- [ ] **Step 1: Teste manual com o Claude Code**

Com `PAD_DATA=./data go run .`, cadastrar a máquina "local" em `/_mcp` e rodar o comando mostrado (trocando `--scope user` por `--scope local` para não sujar a config global). Numa sessão `claude` nova:
- `/mcp` mostra `pad` conectado;
- pedir "apresente-se no pad como pai e crie a conversa teste com 'oi'": a conversa aparece no chat do navegador;
- responder pelo navegador e pedir "espere resposta na conversa teste": o Claude recebe;
- digitar `@pad` no prompt: os documentos aparecem como resources;
- o painel mostra "1 agente: 1 Claude". Se o tipo vier errado, ajustar `tipoDoCliente` pelo `clientInfo.name` real (logar uma vez no `initialize`) e o teste da Task 3.

- [ ] **Step 2: Teste manual com o Codex**

Configurar `~/.codex/config.toml` com o bloco mostrado no painel e rodar `codex`. Conferir que ele lista as ferramentas e consegue `apresentar`, `ler` e `enviar`, e que um `esperar` de 50 s volta sem estourar o `tool_timeout_sec` padrão. Anotar se o Codex oferece os resources.

- [ ] **Step 3: Escrever a seção "Agentes (MCP)" no README**

Logo depois de "Edição ao vivo", no tom do resto do README:
- o que é;
- como cadastrar uma máquina em **mcp** (`/_mcp`);
- os dois comandos;
- o fluxo pai/filho em três linhas;
- o limite de 50 s do `esperar`.

Na lista de onde ficam os dados, acrescentar: "Máquinas, conversas e mensagens: SQLite em `/data/pad.db`".

- [ ] **Step 4: Rodar tudo**

Run: `go test ./... && node --test static/*.test.js`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add README.md  # e o que mudou no Step 1/2
git commit -m "docs: como ligar Claude Code e Codex ao pad"
```

Depois do deploy na vpsarm, conferir com um agente real que um `esperar` de 50 s atravessa o Traefik sem 504. Isso não bloqueia o merge, mas precisa ser anotado no PR.
