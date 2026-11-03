# Edição ao vivo — Plano de implementação

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Várias pessoas editam o mesmo documento do pad ao mesmo tempo, sem sobrescrever, vendo presença, cursores coloridos, travas por seleção e podendo acompanhar alguém.

**Architecture:** Para cada documento aberto, o servidor Go mantém na memória o texto (em unidades UTF-16), a revisão, um log curto de operações e os participantes. Os clientes trocam operações OT ([retain, "insert", -delete], como no ot.js) por WebSocket. O servidor transforma, ordena e repassa as operações e decide as travas. No navegador, três scripts sem build: `ot.js` (operações puras), `sync.js` (máquina de estados do cliente e pilha de desfazer, puras) e `editor.js` (DOM: `textarea`, camada de cursores, presença).

**Tech Stack:** Go 1.25 (stdlib `net/http`, `html/template`, `embed`), `github.com/coder/websocket`, SQLite (`modernc.org/sqlite`), JavaScript puro testado com `node --test` (Node 24, sem dependências).

**Spec:** `docs/superpowers/specs/2026-11-03-edicao-ao-vivo-design.md`

## Global Constraints

- Um binário Go só, sem build de front-end. Os JS ficam em `static/` (embutidos com `embed`) e são servidos em `GET /_static/`.
- A única dependência Go nova é `github.com/coder/websocket`.
- Toda posição no protocolo é em unidades UTF-16 (a unidade de `textarea.selectionStart`).
- Limites: 2 MiB por documento (`maxDoc`), 256 KB no `/publico` (`maxPublicoDoc`), contados em bytes UTF-8 do texto final.
- Trava solta após `lockIdle = 30 * time.Second` sem atividade da aba dona.
- Gravação: `flushDelay = 2 * time.Second` após a última operação, quando o último participante sai e no SIGTERM.
- Reconferência de sessão e acesso: `recheckEvery = time.Minute`.
- O texto carregado do disco é normalizado: `\r\n` e `\r` viram `\n` (o `textarea` faz isso no navegador, e sem normalizar os comprimentos divergem).
- Textos de interface em PT-BR. O 409 do `POST` diz exatamente: `documento aberto na edição ao vivo: recarregue a página`.
- Fechamento por perda de acesso: código de close WebSocket `4000`, e o motivo é o texto mostrado ao usuário. O cliente não reconecta depois de um `4000`.
- Comentários e mensagens de commit em PT-BR, no estilo do repositório (`feat: ...`). `gofmt`, `go vet` e `go test -race ./...` verdes a cada task.

## Protocolo (fixado aqui; Go e JS dependem dele)

Conexão: `GET /_ws/{name...}?v=<token de visitante opcional>`.

Cliente → servidor:
| mensagem | quando |
|---|---|
| `{"t":"op","rev":R,"op":[...],"a":A,"h":H}` | operação local feita sobre a revisão `R`. `a`/`h` são âncora e cabeça da seleção *depois* da operação |
| `{"t":"sel","rev":R,"a":A,"h":H}` | cursor ou seleção mudou, enviada só quando o cliente não tem operação pendente |

Servidor → cliente:
| mensagem | conteúdo |
|---|---|
| `{"t":"init","rev":R,"text":"...","me":"<peerId>","canEdit":bool,"visitor":"<token>"}` | ao conectar, e de novo logo após cada `reject`. `visitor` só vem quando um token novo foi emitido |
| `{"t":"ack","rev":R}` | a operação do próprio cliente virou a revisão `R` |
| `{"t":"op","rev":R,"op":[...],"by":"<peerId>"}` | operação de outra aba |
| `{"t":"peers","peers":[{"id","key","name","color","a","h","lock","reading"}]}` | presença completa, montada para cada destinatário, no máximo a cada 50 ms. Inclui a própria aba. `key` agrupa as abas da mesma identidade em uma bolinha. As posições valem na última revisão enviada a esse cliente |
| `{"t":"reject","reason":"..."}` | operação recusada. Vem sempre seguido de um `init`, e o cliente descarta o pendente. Um `init` sem `reject` antes (reconexão ou revisão velha demais) faz o cliente reaplicar o pendente com `rebase` |

O servidor transforma as posições `a`/`h` recebidas pelas operações do log posteriores a `R`, como faz com a operação.

## Review Focus

1. **Arquivo existente com `\r\n`** (colado do Windows): o documento abre, o comprimento bate com o `textarea` e a edição não dessincroniza. O teste fica na Task 4 (`TestLiveDocNormalizaQuebras`).
2. **Colar texto grande perto do limite** (2 MiB, ou 256 KB no `/publico`): a operação é recusada com o motivo, o cliente se ressincroniza e ninguém mais vê texto parcial. Testes na Task 4 (`TestSubmitLimite`) e na Task 6 (`TestWSRejeitaAcimaDoLimite`).
3. **Mesma pessoa em duas abas**: aparece uma bolinha (mesmo `key` e cor) com dois cursores, e a trava da aba A bloqueia a aba B. Teste na Task 4 (`TestTravaEDaAba`).
4. **Servidor reinicia (deploy) com alguém digitando**: o cliente reconecta, recebe um `init` com outra revisão e reaplica o que estava pendente sem perder nada. Teste na Task 3 (`rebase preserva pendente após reinício`).
5. **Emoji e acentos no meio de uma edição concorrente**: nenhuma operação corta um par substituto, e o texto final é igual em todos. Testes nas Tasks 1 e 2 (vetores com `😀`) e na Task 6 (integração).

---

### Task 1: Núcleo OT em Go

**Files:**
- Create: `ot.go`, `ot_test.go`, `testdata/ot-vectors.json`
- Commit junto: `docs/superpowers/specs/2026-11-03-edicao-ao-vivo-design.md`, `docs/superpowers/plans/2026-11-03-edicao-ao-vivo.md`, `.gitignore` (já tem `.superpowers/`)

**Interfaces:**
- Produces:
  - `type Op []Comp` e `type Comp struct{ Retain, Delete int; Insert string }` (exatamente um campo preenchido). O JSON é o formato do ot.js: número positivo = retain, negativo = delete, string = insert. `UnmarshalJSON` recusa zero, componente vazio, insert com surrogate solto e qualquer outro tipo.
  - `func (o Op) BaseLen() int`, `func (o Op) TargetLen() int`: em unidades UTF-16.
  - `func (o Op) Apply(doc []uint16) ([]uint16, error)`: erro se `BaseLen() != len(doc)` ou se alguma fronteira de componente cair entre as duas metades de um par substituto.
  - `func Transform(a, b Op) (Op, Op, error)`: `a` e `b` partem do mesmo documento, e `apply(apply(d,a),b') == apply(apply(d,b),a')`. **Empate de inserção na mesma posição: o insert de `a` vem primeiro.** O servidor sempre chama com a operação do cliente como `a`.
  - `func TransformIndex(i int, o Op, stickRight bool) int`: posição depois de `o`. Com insert exatamente em `i`, anda para a direita só se `stickRight`.
  - `func TouchesRange(o Op, from, to int) bool`: verdadeiro se `o` apaga alguma unidade em `[from,to)` ou insere em `p` com `from < p < to`.
  - `func utf16Of(s string) []uint16` e `func stringOf(u []uint16) string`.

- [ ] **Step 1: Escrever `testdata/ot-vectors.json`**, compartilhado com a Task 2:

```json
{"apply":[
  {"doc":"abc","op":[1,"X",-1,1],"want":"aXc"},
  {"doc":"ação","op":[4,"!"],"want":"ação!"},
  {"doc":"a😀b","op":[1,-2,1],"want":"ab"}
 ],
 "applyErr":[
  {"doc":"a😀b","op":[2,-1,1]},
  {"doc":"abc","op":[2]}
 ],
 "transform":[
  {"doc":"abc","a":[1,"X",2],"b":[1,"Y",2],"want":"aXYbc"},
  {"doc":"abcdef","a":[1,-3,2],"b":[2,-3,1],"want":"af"},
  {"doc":"😀😀","a":[2,"é",2],"b":[-2,2],"want":"é😀"}
 ]}
```

- [ ] **Step 2: Escrever os testes que falham** em `ot_test.go`:
  - `TestOpVetores`: para cada `apply`, `stringOf(op.Apply(utf16Of(doc))) == want`. Para cada `applyErr`, `Apply` devolve erro. Para cada `transform`, os dois caminhos dão `want`.
  - `TestOpJSON`: `[3,"x",-2]` faz ida e volta idêntica. `[0]`, `[""]`, `[true]` e `["\ud800"]` dão erro no `Unmarshal`.
  - `TestTransformAleatorio`: 2000 pares aleatórios (semente fixa) sobre textos com `a`, `ç` e `😀`, gerados sem cortar pares substitutos. Os dois caminhos convergem.
  - `TestTransformIndex`: `TransformIndex(2, Op{{Retain:2},{Insert:"xy"},{Retain:1}}, true) == 4` e o mesmo com `false` `== 2`. Num delete que cobre o índice, o índice vai para o início do delete.
  - `TestTouchesRange`: com o intervalo `[5,10)` sobre um documento de 12, insert em 5 → false, insert em 7 → true, insert em 10 → false, apagar `[4,5)` → false, apagar `[9,11)` → true.

- [ ] **Step 3: Rodar e ver falhar:** `go test -run 'TestOp|TestTransform|TestTouches' ./...` → FAIL (símbolos indefinidos).
- [ ] **Step 4: Implementar `ot.go`.** O `Transform` percorre as duas operações componente a componente, como o `transform` do ot.js, quebrando retains e deletes no menor comprimento. As saídas são normalizadas (sem componente de tamanho zero e com componentes vizinhos do mesmo tipo fundidos).
- [ ] **Step 5: Rodar e ver passar:** `go test -race -run 'TestOp|TestTransform|TestTouches' ./...` → PASS. Depois `gofmt -l .` sem saída.
- [ ] **Step 6: Commit:** `git add ot.go ot_test.go testdata/ docs/superpowers .gitignore && git commit -m "feat: operações de texto concorrentes (OT) no servidor"`

---

### Task 2: Núcleo OT em JS e arquivos estáticos

**Files:**
- Create: `static/ot.js`, `static/ot.test.js`
- Modify: `main.go` (`//go:embed static/*.js` e a rota em `routes()`), `.github/workflows/ci.yml` (no job `test`, o passo `- run: node --test static/`)

**Interfaces:**
- Consumes: `testdata/ot-vectors.json` (Task 1), lido com `fs` no teste.
- Produces: o script clássico expõe `globalThis.PadOT` e, no Node, `module.exports`, com:
  - `apply(text, op) → string` (lança erro se o comprimento não bate)
  - `transform(a, b) → [a2, b2]` (mesmo desempate da Task 1: `a` primeiro)
  - `compose(a, b) → op` (`apply(apply(t,a),b) === apply(t, compose(a,b))`)
  - `invert(op, textBefore) → op`
  - `transformIndex(i, op, stickRight) → int`, `touchesRange(op, from, to) → bool`
  - `diff(oldText, newText, cursorAfter) → op`
  - `isNoop(op) → bool`
- Rota: `GET /_static/` serve `static/` com `Cache-Control: no-cache`.

- [ ] **Step 1: Escrever os testes que falham** em `static/ot.test.js` (`node:test` e `node:assert`):
  - Os vetores do arquivo compartilhado passam iguais aos do Go (`apply`, `applyErr` lança, `transform` converge).
  - `compose`: 1000 pares aleatórios satisfazem a igualdade acima.
  - `invert`: `apply(apply(t,op), invert(op,t)) === t`, para 1000 casos aleatórios.
  - `diff`: `diff("aa","aaa",1)` é `["a",2]`. `diff("aa","aaa",3)` é `[2,"a"]`. `diff("abc","abc",1)` é `[3]`. `diff("aXc","ac",1)` é `[1,-1,1]`.
  - `transformIndex` e `touchesRange` com os mesmos casos da Task 1.
- [ ] **Step 2: Rodar e ver falhar:** `node --test static/` → FAIL (arquivo ausente).
- [ ] **Step 3: Implementar `static/ot.js`.** O `diff` é o único algoritmo que a assinatura não determina. Primeiro ele calcula o maior sufixo comum `s`, limitado a `min(old.length, new.length)` e a `new.length - cursorAfter`. Depois calcula o maior prefixo comum `p`, limitado a `min(old.length, new.length) - s`. A operação resultante é `[p, -(old.length-p-s), new.slice(p, new.length-s), s]`, normalizada. Se `p` ou o fim do trecho trocado cair no meio de um par substituto, ele recua uma unidade.
- [ ] **Step 4: Servir `static/`** em `main.go` e acrescentar o passo do Node no CI.
- [ ] **Step 5: Rodar e ver passar:** `node --test static/` → PASS. Acrescente a `main_test.go` o `TestStatic`, em que `GET /_static/ot.js` dá 200 e `Content-Type` contém `javascript`. `go test -race ./...` → PASS.
- [ ] **Step 6: Commit:** `git commit -m "feat: operações de texto no navegador e arquivos estáticos"`

---

### Task 3: Cliente de sincronização e desfazer (JS puro)

**Files:**
- Create: `static/sync.js`, `static/sync.test.js`

**Interfaces:**
- Consumes: `PadOT` (Task 2).
- Produces: `globalThis.PadSync` / `module.exports` com:
  - `class SyncClient`:
    - `constructor(rev, text)`, com os campos `rev`, `text` (o local), `serverText`, `inflight` e `buffer`.
    - `local(op) → msg|null`. Aplica a `text`. Se não há `inflight`, devolve `{t:'op', rev, op}` e passa a operação para `inflight`. Se há, compõe no `buffer` e devolve `null`.
    - `ack(rev) → msg|null`. `serverText = apply(serverText, inflight)`. Se havia `buffer`, ele vira o novo `inflight` e é devolvido como mensagem.
    - `remote(rev, op) → op`. Transforma contra `inflight` e `buffer` (o cliente como `a`) e devolve a operação a aplicar no texto local.
    - `rebase(rev, serverText) → msg|null`. Usado num `init` depois de reconectar. Calcula `pend = compose(inflight, buffer)`, `d = diff(this.serverText, serverText, serverText.length)` e `[p2] = transform(pend, d)`. Depois `text = apply(serverText, p2)`, `inflight = p2` (ou `null` se for no-op) e `buffer = null`. Se houver `inflight`, devolve a mensagem.
    - `reset(rev, text)`. Usado no `init` que vem depois de um `reject`: descarta o pendente.
    - `pending() → bool`. `localIndex(i) → int` = `transformIndex(i, compose(inflight, buffer), true)`, para as posições dos outros.
  - `class UndoStack`:
    - `constructor(groupMs = 1000)`.
    - `record(op, textBefore, now)` empilha `invert(op, textBefore)`. Se `now - últimoRecord < groupMs`, compõe com o topo: `compose(novo, topo)`. Também limpa o refazer.
    - `transform(remoteOp)` transforma as duas pilhas do topo para a base, levando a operação remota adiante como no `UndoManager` do ot.js.
    - `undo(text) → op|null` e `redo(text) → op|null`. A operação devolvida já entra na pilha oposta como inversa. `undo` não remove a entrada se o chamador recusar: `peekUndo() → op|null` permite checar a trava antes.

- [ ] **Step 1: Escrever os testes que falham** em `static/sync.test.js`:
  - `dois clientes e um servidor simulado convergem`: um servidor de mentira (array de log + `transform`) e dois `SyncClient`. Cada um faz 200 operações locais aleatórias, entregues em ordem embaralhada mas FIFO por canal. No fim, `c1.text === c2.text === servidor`.
  - `rebase preserva pendente após reinício`: o cliente com `serverText = "abc"` digita `"X"` no fim (inflight) e `"Y"` (buffer). Faz `rebase(0, "abc!")`: o `text` contém `X`, `Y` e `!`, e a mensagem devolvida tem `rev: 0`.
  - `desfazer só o meu`: o texto `""` recebe a operação local `"oi"` e depois a remota `[2," ana"]`. `undo` devolve a operação que leva `"oi ana"` para `" ana"`.
  - `agrupa digitação`: três `record` a 100 ms um do outro, e um `undo` desfaz os três. Um quarto `record` 2 s depois fica separado.
  - `localIndex`: com o inflight `["ab",3]`, `localIndex(0) === 2`.
- [ ] **Step 2: Rodar e ver falhar:** `node --test static/` → FAIL.
- [ ] **Step 3: Implementar `static/sync.js`.**
- [ ] **Step 4: Rodar e ver passar:** `node --test static/` → PASS.
- [ ] **Step 5: Commit:** `git commit -m "feat: cliente de sincronização e desfazer por usuário"`

---

### Task 4: Documento ao vivo no servidor (sem rede)

**Files:**
- Create: `live.go`, `live_test.go`

**Interfaces:**
- Consumes: `Op`, `Transform`, `TransformIndex`, `TouchesRange`, `utf16Of`, `stringOf` (Task 1).
- Produces:
  - `type peer struct { id, key, login, visitor, color string; canEdit bool; a, h int; lock, idle bool; selSince, active time.Time; send chan []byte }`. O `key` vale `"u:"+uid` para um editor logado fora do `/publico` e `"v:"+visitorID` nos outros casos. `send` é preenchido na Task 6.
  - `type liveDoc struct` com `mu sync.Mutex`, `name`, `publico bool`, `max int`, `text []uint16`, `rev int`, `log []Op` (no máximo `logMax = 1000`), `peers map[*peer]bool`, `colors map[string]string` (de `key` para cor), `dirty bool`.
  - `func newLiveDoc(name, text string) *liveDoc`: normaliza as quebras de linha e define `max` (`maxPublicoDoc` se `isPublico(name)`, senão `maxDoc`).
  - `func (d *liveDoc) add(p *peer)` atribui a cor da paleta (12 cores fixas): reaproveita a do mesmo `key`, ou pega a primeira que nenhum presente usa. `func (d *liveDoc) remove(p *peer)`.
  - `func (d *liveDoc) submit(p *peer, rev int, op Op, a, h int, now time.Time) (Op, error)` faz, em ordem: checa `canEdit` (`errReadOnly`); checa `rev` dentro do log (`errStale`); transforma `op`, `a` e `h` por `log[rev:]`; checa trava alheia com `TouchesRange` (`errLocked`, com o nome do dono); checa o limite em bytes UTF-8 (`errTooBig`); aplica a operação (erro → `errInvalid`); faz `rev++`, guarda no log e marca `dirty`; transforma `a`/`h` dos outros peers com `stickRight=true` e o início e o fim das travas como na regra da trava; por fim atualiza a seleção e a atividade de `p`. As mensagens de erro são em PT-BR: `trecho travado por <nome>`, `documento passa de 2 MB` e `documento da área pública passa de 256 KB`.
  - `func (d *liveDoc) setSel(p *peer, rev, a, h int, now time.Time) error`.
  - `func (d *liveDoc) expireLocks(now time.Time) bool`.
  - `func (d *liveDoc) views(to *peer) []peerView`, com `type peerView struct{ ID, Key, Name, Color string; A, H int; Lock, Reading bool }` e JSON nas chaves minúsculas do protocolo. O `Key` sai como um apelido opaco por documento (`"k1"`, `"k2"`…), nunca o `key` interno.
- Regras de trava, aplicadas por `recomputeLocks` a cada mudança de seleção:
  - Só `canEdit`, com seleção não vazia e sem `idle`.
  - Por ordem de `selSince`, ganha a trava quem não se sobrepõe a uma trava já concedida.
  - `idle` é marcado quando `now - active > lockIdle` e limpo na próxima mensagem da aba.
- Regra do nome em `views` (o destinatário é `to`): no `/publico`, todo mundo aparece pelo `visitor`. Fora dele, `login` só quando `to.canEdit && p.canEdit && p.login != ""`; senão, `visitor`. `Reading = !p.canEdit`.

- [ ] **Step 1: Escrever os testes que falham** em `live_test.go`:
  - `TestLiveDocNormalizaQuebras`: `newLiveDoc("ana/x", "a\r\nb\rc")` dá o texto `"a\nb\nc"` e `len(text) == 5`.
  - `TestSubmitConcorrente`: A e B mandam operações sobre a rev 0. A segunda é transformada, e o texto final sai igual ao do `TestTransform` equivalente.
  - `TestSubmitStale`: com `logMax` reduzido no teste para 2, uma operação sobre a rev 0 depois de 3 revisões dá `errStale`.
  - `TestTravaBloqueiaOutros`: B seleciona `[5,10)`. A insere em 7 → `errLocked` com a mensagem `trecho travado por bruno`. A insere em 5 → ok. A apaga `[4,5)` → ok. B insere em 7 → ok.
  - `TestTravaPrimeiroGanha`: B seleciona `[5,10)` e depois C seleciona `[8,12)`. Só B fica com `lock`. Quando B solta, C ganha a trava no próximo `recomputeLocks`.
  - `TestTravaEDaAba`: dois peers com o mesmo `key`. A trava do primeiro bloqueia o segundo. Os dois recebem a mesma cor.
  - `TestTravaExpira`: `expireLocks(t0+31s)` solta a trava e devolve `true`. Um novo `setSel` restaura a trava.
  - `TestLeitorNaoEdita`: um peer `canEdit=false` → `errReadOnly`, e uma seleção dele nunca trava.
  - `TestNomesPorDestinatario`: num documento não `/publico`, um editor vê o login de outro editor. Um leitor vê o editor pelo `visitor`. No `/publico`, o editor logado aparece pelo `visitor` para todos.
  - `TestSubmitLimite`: com o `/publico` e o texto em 256 KB − 1 byte, inserir `"é"` (2 bytes) dá `errTooBig`, e o texto e a rev não mudam.
  - `TestCursorAndaComInsercao`: o peer C tem cursor em 3. O peer A insere `"xx"` em 1, e C passa a 5.
- [ ] **Step 2: Rodar e ver falhar:** `go test -run 'TestLiveDoc|TestSubmit|TestTrava|TestLeitor|TestNomes|TestCursor' ./...` → FAIL.
- [ ] **Step 3: Implementar `live.go`.** O tempo entra sempre por parâmetro (`now`), nunca por `time.Now()` lá dentro, para os testes controlarem o relógio.
- [ ] **Step 4: Rodar e ver passar:** o mesmo comando com `-race` → PASS.
- [ ] **Step 5: Commit:** `git commit -m "feat: documento ao vivo com travas por seleção no servidor"`

---

### Task 5: Identidade de visitante

**Files:**
- Create: `visitor.go`, `visitor_test.go`
- Modify: `main.go` (o `schema` ganha `CREATE TABLE IF NOT EXISTS secrets (k TEXT PRIMARY KEY, v BLOB NOT NULL);`, e `app` ganha `visitorKey []byte`, carregado ou criado com 32 bytes de `crypto/rand` em `openApp`)

**Interfaces:**
- Produces:
  - `func (a *app) newVisitor() (id, name, token string)`. O `name` sai de duas listas PT-BR (pelo menos 30 bichos brasileiros × 30 adjetivos, com concordância de gênero), como `Capivara Anônima` ou `Tatu Curioso`. O `token` é `base64url(id + "|" + name) + "." + base64url(HMAC-SHA256(visitorKey, id|name))`.
  - `func (a *app) parseVisitor(token string) (id, name string, ok bool)`: compara o HMAC em tempo constante (`hmac.Equal`).

- [ ] **Step 1: Escrever os testes que falham:**
  - `TestVisitorIdaEVolta`: o token de `newVisitor` volta no `parseVisitor` com o mesmo `id` e `name`.
  - `TestVisitorAdulterado`: trocar o nome dentro do token dá `ok=false`. Um token de outro `app` (outra chave) também dá `ok=false`.
  - `TestVisitorChavePersiste`: reabrir o `openApp` no mesmo diretório aceita o token antigo.
  - `TestNomesConcordam`: todo nome gerado é uma combinação válida das listas, sem string vazia. As listas não têm duplicatas.
- [ ] **Step 2: Rodar e ver falhar:** `go test -run TestVisitor ./...` → FAIL.
- [ ] **Step 3: Implementar.**
- [ ] **Step 4: Rodar e ver passar:** `go test -race ./...` → PASS.
- [ ] **Step 5: Commit:** `git commit -m "feat: nomes de visitante assinados pelo servidor"`

---

### Task 6: Hub, endpoint WebSocket e gravação

**Files:**
- Create: `hub.go`, `ws.go`, `ws_test.go`
- Modify: `main.go` (o `app` ganha `hub *hub`; a rota `GET /_ws/{name...}`; `go.mod`/`go.sum` com `go get github.com/coder/websocket@latest`)

**Interfaces:**
- Consumes: `liveDoc`, `peer`, `peerView` (Task 4); `newVisitor`/`parseVisitor` (Task 5); `a.access`, `a.currentUser`, `docName`, `a.readDoc`, `a.writeDoc`, `a.writeMu`, `isPublico`, `countEntries`, `a.evictOldest` (já existem).
- Produces:
  - `type hub struct { a *app; mu sync.Mutex; docs map[string]*liveDoc }`.
  - `func (h *hub) open(name string) (*liveDoc, error)` carrega do disco se o documento não estiver na memória. `func (h *hub) leave(d *liveDoc, p *peer)` grava na hora quando sai o último e tira o documento da memória.
  - `func (h *hub) connected(name string) bool`: se há algum peer no documento (usado na Task 7).
  - `func (h *hub) flush(d *liveDoc) error` grava `stringOf(text)` com `writeDoc` sob `a.writeMu` e atualiza `docs.updated`. No `/publico`, se o arquivo ainda não existe, roda a mesma limpeza de `savePublico` antes de gravar.
  - `func (h *hub) flushAll()`.
  - `func (a *app) liveWS(w http.ResponseWriter, r *http.Request)`, nesta ordem:
    1. Nome válido (`docName`).
    2. Acesso. No `/publico`, editor. Se `allowed`, ou se é o próprio prefixo e o documento não existe, editor com login. Se `public`, leitor. Senão, 404 (ou 401 sem login), **antes** do upgrade.
    3. Visitante: o `?v=` válido é reaproveitado; senão, o servidor emite um novo e manda em `init.visitor`.
    4. `websocket.Accept` com a checagem padrão de origem, e `SetReadLimit(int64(maxDoc) + 64<<10)`.
    5. `init` e depois `peers` para todos.
    6. Laço de leitura: `op`, que chama `submit` e responde `ack` para quem mandou, `op` para os outros e `peers` para todos; e `sel`. Antes do primeiro `submit` de um editor sobre um documento inexistente fora do `/publico`, faz o mesmo `INSERT INTO docs ... ON CONFLICT DO NOTHING` do `save`.
    7. Escrita por uma goroutine que esvazia `p.send` (canal com buffer de 256). Se o canal enche, a conexão cai com `StatusPolicyViolation`.
    8. Ping a cada 10 s com timeout de 5 s.
  - O temporizador de gravação (`flushDelay`, declarado como `var` para o teste reduzir) é armado em cada `submit` bem-sucedido. Um tique de 1 s por documento aberto chama `expireLocks` e, se algo mudou, manda `peers`.
  - Os `peers` são acumulados e enviados no máximo a cada 50 ms por documento.
  - Erro de `submit` vira `reject` com a mensagem e depois um `init` novo para aquele cliente. A exceção é o `errStale`, que manda só o `init`, sem `reject`, para o cliente fazer `rebase` e não perder o pendente.

- [ ] **Step 1: Escrever os testes que falham** em `ws_test.go`. Use `httptest.NewServer(a.routes())`, o login de `main_test.go` e `websocket.Dial` com `HTTPClient` (o cookie jar segue junto). Um `testClient` de apoio guarda `rev`, `text` e uma única operação pendente, transformando-a com `Transform` contra cada `op` que chega. Ele manda a próxima só depois do `ack`.
  - `TestWSConvergencia`: ana, mais bia com acesso compartilhado, mais um terceiro cliente da ana fazem 100 inserções e deleções aleatórias cada um, com emoji. No fim, os três `text` são iguais. Depois que todos fecham, o arquivo em disco tem o mesmo conteúdo.
  - `TestWSSemAcesso`: caio, sem compartilhamento, recebe 404 no handshake. Um anônimo num documento privado recebe 401.
  - `TestWSOrigemOutroSite`: o handshake com `Origin: https://evil.example` é recusado.
  - `TestWSLeitorPublico`: num documento com `public=1`, o anônimo conecta, recebe `init` com `canEdit:false` e, numa `op`, recebe `reject`. Na lista `peers` que ele recebe não aparece `"ana"`.
  - `TestWSPublicoVisitante`: no `/publico`, ana logada aparece com um nome de visitante nos `peers`. Reconectar com o mesmo `?v=` mantém o nome.
  - `TestWSRejeitaAcimaDoLimite`: no `/publico`, uma `op` que passa de 256 KB recebe `reject` com `documento da área pública passa de 256 KB`, seguido de um `init` com o texto anterior.
  - `TestWSCriaDocumento`: a primeira `op` da ana em `/ana/novo` cria a linha em `docs` com `owner_id` da ana.
- [ ] **Step 2: Rodar e ver falhar:** `go test -run TestWS ./...` → FAIL.
- [ ] **Step 3: Implementar `hub.go` e `ws.go`.**
- [ ] **Step 4: Rodar e ver passar:** `go test -race -count=3 -run TestWS ./...` → PASS sem data race. `go vet ./...` limpo.
- [ ] **Step 5: Commit:** `git commit -m "feat: edição ao vivo por WebSocket"`

---

### Task 7: Mudanças de acesso, POST, limites e desligamento

**Files:**
- Modify: `ws.go`, `hub.go`, `main.go` (`save`, `share`, `setPublic`, `del`, `evictOldest` e `main`), `ws_test.go`, `main_test.go`

**Interfaces:**
- Consumes: `hub` (Task 6).
- Produces:
  - `func (h *hub) recheck(name string)`: refaz o acesso de cada peer. Quem perdeu acesso, ou passou de editor para leitor ou o contrário, é fechado com `4000` e o motivo `seu acesso a este documento mudou: recarregue a página`.
  - `func (h *hub) kill(name, reason string)`: fecha todos com `4000` e descarta o documento **sem gravar**.
  - `share` (`/_unshare`) e `setPublic` chamam `recheck`. `del` chama `kill(name, "documento apagado")` antes de remover o arquivo.
  - O `save` devolve `409` com `documento aberto na edição ao vivo: recarregue a página` quando `a.hub.connected(name)`.
  - O `evictOldest` pula os arquivos de documentos com `connected` verdadeiro.
  - Uma goroutine por conexão, a cada `recheckEvery` (`var`), confere a sessão (se havia cookie) e o acesso. Se mudou, fecha como em `recheck`.
  - Limites: `maxConnsPerIP = 20` (o IP vem de `X-Real-Ip` se presente, senão de `RemoteAddr`) e `maxPeersPerDoc = 50`. Acima deles, 429 antes do upgrade. Comente o atalho: `// shortcut: confia no X-Real-Ip do Traefik; se o serviço ficar exposto sem proxy, ler só o RemoteAddr`.
  - No `main`, `signal.NotifyContext(ctx, SIGTERM, os.Interrupt)`, depois `srv.Shutdown`, depois `a.hub.flushAll()` e só então a saída.

- [ ] **Step 1: Escrever os testes que falham:**
  - `TestWSPerdeAcesso`: bia conectada, a ana faz `/_unshare` da bia, e a conexão da bia fecha com o código 4000 em até 1 s.
  - `TestWSApagado`: com a ana conectada, ela mesma apaga pelo `/_delete`. A conexão fecha com 4000 `documento apagado`, e o arquivo não volta a existir depois de `flushDelay`.
  - `TestWSSessaoExpira`: com `recheckEvery` reduzido a 50 ms, apagar a sessão da bia no banco fecha a conexão dela.
  - `TestPOSTComConexao`: o `POST` com alguém conectado dá 409 e a mensagem exata. Sem ninguém conectado, dá 204 como antes. Os testes atuais de `main_test.go` continuam verdes sem mudança.
  - `TestEvictPulaAberto`: com `maxPublicoItems` baixo, o documento público mais antigo, mas conectado, não é apagado, e o seguinte é.
  - `TestWSLimitePorIP`: a 21ª conexão do mesmo IP recebe 429.
- [ ] **Step 2: Rodar e ver falhar:** `go test -run 'TestWSPerde|TestWSApagado|TestWSSessao|TestPOST|TestEvict|TestWSLimite' ./...` → FAIL.
- [ ] **Step 3: Implementar.**
- [ ] **Step 4: Rodar e ver passar:** `go test -race ./...` → PASS (o pacote inteiro).
- [ ] **Step 5: Commit:** `git commit -m "feat: edição ao vivo respeita mudanças de acesso e encerra gravando"`

---

### Task 8: Editor sincronizado no navegador

**Files:**
- Create: `static/editor.js`
- Modify: `templates.html` (as definições `doc` e `public` passam a carregar `/_static/ot.js`, `/_static/sync.js` e `/_static/editor.js`, com `data-name` e `data-can-edit` no `<textarea>`. O `<script>` inline de `POST` sai. A página `public` vira o mesmo editor em modo leitura)

**Interfaces:**
- Consumes: o protocolo, `PadOT` (Task 2) e `PadSync` (Task 3).
- Produces: `editor.js` expõe `window.pad = { sync, undo, socket, peers, me }` para a Task 9 e despacha `CustomEvent('pad:peers')` e `CustomEvent('pad:text')` no `document`.
- Comportamento:
  - Ao conectar, o `textarea` fica `readOnly` com `#st` = `reconectando…` até chegar o `init`. Leitores ficam sempre `readOnly`.
  - `input` (fora de composição) calcula `diff(anterior, t.value, t.selectionEnd)`. Se a operação toca uma trava alheia (`touchesRange` com as posições locais dos `peers` com `lock`), o editor restaura o valor e a seleção anteriores e mostra o aviso `trecho travado por <nome>` por 2 s. Senão, chama `undo.record`, `sync.local` e envia com `a`/`h` da seleção.
  - Durante `compositionstart`…`compositionend`, nada é enviado e as operações remotas ficam numa fila. No `compositionend`, faz o mesmo que um `input` e depois aplica a fila.
  - Operação remota: `undo.transform` e `sync.remote`. O valor é trocado e a seleção própria é restaurada com `transformIndex(…, false)`.
  - `selectionchange` e `keyup`/`mouseup` mandam `sel`, com no máximo um envio a cada 50 ms e só quando `!sync.pending()`. Depois de um `ack` sem pendência, manda a seleção atual.
  - Ctrl+Z, Ctrl+Shift+Z, Ctrl+Y e `beforeinput` com `historyUndo`/`historyRedo` recebem `preventDefault`. Antes de usar a pilha própria, `peekUndo` é checado contra as travas.
  - `reject` mostra a razão em `#st` e marca que o próximo `init` chama `sync.reset`. Qualquer outro `init` (reconexão ou `errStale`) chama `sync.rebase`.
  - Na reconexão, a espera cresce de 0,5 s até 10 s. Em `close` com 4000, nada de reconectar: o motivo aparece num aviso fixo com o link `recarregar`.
  - O token de visitante fica em `localStorage['pad:visitor']`, dentro de try/catch.
  - `beforeunload` só avisa se `sync.pending()`.

- [ ] **Step 1: Escrever o teste de ponta a ponta que falha** em `ws_test.go`: `TestPaginaCarregaScripts`, em que `GET /ana/doc` contém `/_static/editor.js` e não contém `fetch(location.pathname`.
- [ ] **Step 2: Rodar e ver falhar:** `go test -run TestPaginaCarregaScripts ./...` → FAIL.
- [ ] **Step 3: Implementar `editor.js` e ajustar `templates.html`.**
- [ ] **Step 4: Verificar:** `go test -race ./... && node --test static/` → PASS. No navegador, com `PAD_DATA=./data go run .` e duas janelas (uma anônima no `/publico/teste`), digitar nas duas ao mesmo tempo, inclusive `ç`/`ã` por tecla morta e um emoji. O texto fica igual nas duas e nada se perde. Ctrl+Z numa janela não desfaz o que a outra digitou. Derrubar o servidor e subir de novo: as duas voltam sozinhas.
- [ ] **Step 5: Commit:** `git commit -m "feat: editor sincronizado ao vivo com desfazer por usuário"`

---

### Task 9: Cursores, presença e acompanhar

**Files:**
- Modify: `static/editor.js` (ou crie `static/presence.js` se `editor.js` passar de ~400 linhas) e `templates.html` (o CSS do editor e o cabeçalho com `<span id="peers">`, como no mockup A)

**Interfaces:**
- Consumes: `window.pad` e os eventos da Task 8.
- Comportamento, seguindo os mockups `.superpowers/brainstorm/169106-1793721576/content/editor-presenca.html` (opção A) e `acompanhar.html`:
  - **Camada de cursores.** Uma div espelho atrás do `textarea` (fundo transparente), com a mesma fonte, `padding`, largura, `white-space: pre-wrap`, `overflow-wrap` e rolagem sincronizada. É refeita no `pad:text`. As posições vêm de `Range.getClientRects()` sobre o nó de texto do espelho, recalculadas em `requestAnimationFrame` no `pad:peers`, na rolagem e no `resize`. Cada outra aba mostra uma barra de 2 px na cor dela com etiqueta do nome. A seleção ganha fundo na cor com ~20 % de opacidade e, se `lock`, o selo `🔒 nome`.
  - **Calha de linhas** à esquerda, com a altura de cada linha lógica medida no espelho (as linhas quebradas ocupam mais). Os números das linhas onde há alguém ficam em negrito na cor da pessoa.
  - **Bolinhas.** Uma por `key`, com a inicial e a cor, sobrepostas no cabeçalho. A própria aba também aparece.
  - **Menu** ao clicar: cada pessoa com cor, nome e estado. `digitando na linha N` se houve `op` dela nos últimos 3 s. `selecionou as linhas X–Y` se há seleção. `lendo` se `reading`. `linha N` nos outros casos. A própria aparece como `você`. Fecha com Esc ou clique fora. É navegável por teclado (`button`s e `aria-expanded`).
  - **Acompanhar.** Clicar em outra pessoa liga o modo: borda do editor na cor dela, faixa `acompanhando <nome>` com o botão `parar`, e a rolagem mantém o cursor dela visível a cada `pad:peers`. O modo para com `parar`, com `input`, com `wheel`/`keydown` de rolagem do próprio usuário ou quando a pessoa some dos `peers`.
- [ ] **Step 1: Verificar no navegador com três janelas** (ana, bia e um anônimo no `/publico/teste`): as barrinhas e as etiquetas estão na posição certa, inclusive em linha longa quebrada e depois de rolar. A seleção da bia mostra o cadeado, e a ana não consegue digitar dentro, só na borda. Depois de 30 s parada, o cadeado some. O menu mostra os estados certos, acompanhar segue e `parar` desliga. Num documento público só leitura, o anônimo aparece como `lendo` e vê tudo ao vivo. Tire capturas de tela das três janelas para o relatório da task.
- [ ] **Step 2: Rodar a suíte:** `go test -race ./... && node --test static/` → PASS.
- [ ] **Step 3: Commit:** `git commit -m "feat: cursores coloridos, presença e modo acompanhar"`

---

### Task 10: README e verificação final

**Files:**
- Modify: `README.md`

- [ ] **Step 1:** Acrescentar ao README, logo depois do primeiro parágrafo, um parágrafo sobre a edição ao vivo: presença, cores, trava por seleção (30 s), visitantes no `/publico` e o `POST` recusado com 409 enquanto há alguém conectado. Na seção "Desenvolver", acrescentar `node --test static/`.
- [ ] **Step 2: Verificação completa:** `test -z "$(gofmt -l .)" && go vet ./... && go test -race -count=1 ./... && node --test static/` → tudo PASS. `docker compose build` termina sem erro.
- [ ] **Step 3: Commit:** `git commit -m "docs: edição ao vivo no README"`
