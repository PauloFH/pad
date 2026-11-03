# Visual Oficina — Plano de implementação

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Todas as telas do pad no visual Oficina, com 8 temas, fontes embutidas, coluna de documentos recolhível, abas e a tela de entrada com recentes. A edição ao vivo continua intacta.

**Architecture:** Os tokens de tema viram variáveis CSS num arquivo estático (`static/pad.css`, que sai do `<style>` do template), com um bloco por tema em `:root[data-theme=…]`. Um script pequeno inline no `<head>` aplica o tema antes da pintura, e `static/shell.js` cuida do seletor de tema, da coluna, das abas e da gaveta. O servidor passa a fornecer a lista de documentos para a coluna em todas as páginas logadas e, na entrada, quem está em cada documento (lido do hub).

**Tech Stack:** Go 1.25 (`html/template`, `embed`), CSS puro (`color-mix`, custom properties), JS clássico sem build, fontes woff2 embutidas (Space Grotesk e JetBrains Mono, OFL), testes em Go e `node --test static/*.test.js`.

**Spec:** `docs/superpowers/specs/2026-11-03-visual-oficina-design.md`. Mockups aprovados em `docs/superpowers/specs/2026-11-03-visual-oficina/`.

## Global Constraints

- Sem framework, sem CDN e sem build: o CSS e o JS ficam em `static/`, servidos por `/_static/`. As fontes também ficam em `static/fonts/` (woff2, subconjunto latino, pesos Space Grotesk 400/500/600/700 e JetBrains Mono 400/600) e o arquivo `OFL.txt` vai junto.
- `//go:embed` precisa passar a incluir `static/fonts/*` e `static/*.css`, além de `static/*.js`.
- Os valores dos 8 temas vêm da tabela do spec, copiados exatamente. Os tokens extras `--field`, `--hov`, `--err` e `--ok` existem em cada tema.
- Contraste mínimo, verificado em teste:
  - 4,5:1 para `--fg` e `--mut` sobre `--bg`, `--bar` e `--field`;
  - 4,5:1 para `--on` sobre `--acc`;
  - 4,5:1 para `--err` sobre `--bg` e `--field`;
  - 3:1 para `--acc` sobre `--bg` e para `--ok` sobre `--bar`;
  - 3:1 para cada cor da paleta das pessoas sobre os 8 `--bg` e sob texto branco.
- Tema padrão: seguir o sistema (Papel no claro, Grafite no escuro). A escolha fica em `localStorage['pad:tema']` (`sistema` ou o id do tema) e é aplicada antes da primeira pintura.
- Ids dos temas: `grafite`, `madrugada`, `cafe`, `floresta`, `papel`, `ceu`, `areia` e `contraste`. Os nomes mostrados são Grafite, Madrugada, Café, Floresta, Papel, Céu, Areia e Alto contraste.
- O textarea, o `#mir` e a `.gut` continuam com **uma** regra de fonte só, agora JetBrains Mono. A camada de cursores redesenha depois de `document.fonts.ready`.
- Coluna e abas guardadas por usuário: `localStorage['pad:col:<usuario>']` (aberta ou fechada), `pad:pastas:<usuario>` (pastas abertas) e `pad:abas:<usuario>` (até 8 nomes). Todo acesso ao `localStorage` fica dentro de try/catch.
- Gaveta abaixo de `768px`. Os alvos de toque têm pelo menos 40px no celular. `prefers-reduced-motion` desliga as transições. O foco aparece como anel de 2px em `--acc`.
- Textos de interface em PT-BR e em frase normal (sem caixa alta), usando os textos dos mockups. Sem coautor nos commits.
- A edição ao vivo não muda de comportamento: os testes Go e JS de hoje continuam verdes.

## Review Focus

1. **Trocar de tema com o editor aberto:** os cursores e as seleções continuam no lugar e as cores das pessoas continuam legíveis. O teste é `TestPaletaContraste` nos 8 fundos (Task 1), mais a verificação no navegador da Task 3.
2. **Primeira visita sem `localStorage`** (aba anônima ou armazenamento bloqueado): o tema segue o sistema sem piscar e nada quebra. O teste fica na Task 1 (`theme.test.js`: escolha inválida ou `localStorage` que lança exceção resultam em `sistema`).
3. **Nomes de documento longos ou muito aninhados** (5 níveis) na coluna, nas abas e nos blocos: o texto é truncado com reticências e o caminho completo aparece no `title`. A coluna não estoura a largura em 360px. O teste é `TestArvore` com 5 níveis (Task 3), mais a verificação em 360px.
4. **Usuário sem nenhum documento:** a coluna mostra só "+ novo documento" e a área pública, e a entrada mostra o estado vazio. O teste é `TestEntradaVazia` (Task 4).
5. **Visitante anônimo no `/publico` e em documento público:** não aparece a coluna, o botão da coluna nem as abas, e nenhum nome de documento de ninguém. O teste é `TestVisitanteSemColuna` (Task 3).

---

### Task 1: Temas, fontes e teste de contraste

**Files:**
- Create: `static/pad.css` (só tokens e `@font-face` nesta task), `static/theme.js`, `static/theme.test.js`, `static/fonts/*.woff2`, `static/fonts/OFL.txt`, `theme_test.go`
- Modify: `main.go` (o embed passa a `static/*.js static/*.css static/fonts/*`, e o `/_static/` serve `.woff2` com `Content-Type: font/woff2`), `templates.html` (o `head` ganha `<link rel=stylesheet href=/_static/pad.css>`, os preloads das 2 fontes principais e o script inline de tema), `live.go` (paleta) e `live_test.go` (`TestPaletaContraste`)

**Interfaces:**
- Produces:
  - Em `static/pad.css`, um bloco por tema `:root[data-theme="<id>"]{--bg;--bar;--fg;--mut;--line;--acc;--on;--field;--hov;--err;--ok;color-scheme:light|dark}`. Os valores do spec são copiados exatamente.
  - `--ui` = `'Space Grotesk', system-ui, sans-serif` e `--mono` = `'JetBrains Mono', ui-monospace, monospace`.
  - Script inline no `head`, de uma linha: lê `localStorage['pad:tema']`, resolve `sistema` para `papel` ou `grafite` com `matchMedia('(prefers-color-scheme: dark)')`, e põe `document.documentElement.dataset.theme`.
  - `static/theme.js` expõe `PadTheme = { temas: [{id, nome, tipo}], resolver(escolha, escuroNoSistema) → id, aplicar(escolha), escolha() → string }` (também por `module.exports`). Ao mudar o tema do sistema com a escolha em `sistema`, o `aplicar` acontece de novo.
  - Em Go, `func themeTokens(css string) (map[string]map[string]string, error)` em `theme_test.go` (função auxiliar de teste) lê os blocos de tema de `static/pad.css`.

- [ ] **Step 1: Testes que falham.** Em `theme_test.go`:
  - `TestTemasCompletos`: os 8 ids existem e cada um define os 11 tokens.
  - `TestTemasContraste`: todas as razões das Global Constraints, com a mensagem `tema <id>: <token> sobre <token> = 4.21 < 4.5` quando falhar.
  - Em `live_test.go`, o `TestPaletaContraste` passa a checar contra os 8 `--bg` lidos do CSS, mais o branco.
  - Em `static/theme.test.js`: `resolver('sistema', true) === 'grafite'`; `resolver('sistema', false) === 'papel'`; `resolver('cafe', false) === 'cafe'`; `resolver('xyz', true) === 'grafite'`; com um `localStorage` falso que lança exceção, `escolha() === 'sistema'`.
- [ ] **Step 2: Rodar e ver falhar:** `go test -run 'TestTemas|TestPaleta' ./...` e `node --test static/*.test.js` → FAIL.
- [ ] **Step 3: Implementar.** As fontes são baixadas do pacote Fontsource fixado por versão (o woff2 latino de cada peso) e gravadas em `static/fonts/`. Troque as cores da paleta de `live.go` que falharem, mantendo 12 cores bem distintas.
- [ ] **Step 4: Rodar e ver passar:** `go test -race ./... && node --test static/*.test.js` → PASS. `TestStatic` ganha um caso: `GET /_static/fonts/<um arquivo>.woff2` responde 200 com `font/woff2`.
- [ ] **Step 5: Commit:** `feat: temas, fontes embutidas e teste de contraste` (junto vão o spec, o plano e os mockups em `docs/`).

---

### Task 2: Componentes Relevo e telas simples

**Files:**
- Modify: `static/pad.css` (componentes), `templates.html` (as definições `head`, `msg`, `login`, `join` e `invite`, e as regras do `<style>` antigo migradas para `pad.css`)

**Interfaces:**
- Consumes: os tokens da Task 1.
- Produces: estas classes de componente em `pad.css`, que as Tasks 3 a 5 usam:
  - `.btn` (secundário), `.btn.pri` (principal "aceso"), `.btn.perigo`, `.campo` (input e select) e `.seg` (alternância);
  - `.pop` (painel flutuante), `.menu`/`.menu-item`, `.selo` (pílula), `.cartao` (bloco da B2), `.aviso` (toast), `.chave` (interruptor), `.av` (avatar);
  - o layout `.tela-cartao` para login, criar conta, mensagem e convite.
  - Valores do Relevo: raio 9px nos botões e campos, 12px no `.pop`, 10px no `.aviso`. O `.btn.pri` usa degradê `--acc` → `color-mix(--acc 82%, #000)`, brilho `inset 0 1px 0 #ffffff40` e sombra `0 4px 12px color-mix(--acc 35%, transparent)`. O `.campo` tem sombra interna `inset 0 1px 3px #0004` e contorno `0 0 0 1px var(--line)`. No foco, o contorno passa a `0 0 0 2px var(--acc)`.

- [ ] **Step 1: Teste que falha** em `main_test.go`, `TestTelasSimplesUsamCartao`: `GET /_login`, `GET /_join/<token válido>`, a página de mensagem (um 404 de documento) e `GET /_convite` (como admin) contêm `class="tela-cartao"` e `/_static/pad.css`. O login contém `pad<span` e o texto exato `Blocos de notas compartilhados, ao vivo.`
- [ ] **Step 2: Rodar e ver falhar:** `go test -run TestTelasSimplesUsamCartao ./...` → FAIL.
- [ ] **Step 3: Implementar** segundo o mockup `inicio.html` (topo) para o login e `resto.html` para o convite. O criar conta e a mensagem usam o mesmo cartão. O convite ganha o botão "Copiar" (com `navigator.clipboard.writeText`; se não houver, seleciona o campo) e o botão "Gerar outro link".
- [ ] **Step 4: Verificar:** `go test -race ./...` → PASS. No navegador, com `PAD_DATA=<tmp> go run .`, as 4 telas nos temas Grafite, Papel e Alto contraste, em 1280px e 400px, batem com os mockups. Tire as capturas para o relatório.
- [ ] **Step 5: Commit:** `feat: componentes Relevo e telas de login, convite e mensagem`

---

### Task 3: Moldura do editor — coluna, abas e barra de status

**Files:**
- Create: `static/shell.js`, `static/shell.test.js`
- Modify: `main.go` (o helper de documentos do usuário e os dados da coluna nas páginas `doc` e `public`), `templates.html` (as definições `doc` e `public` na moldura nova, mais as definições novas `moldura-topo`, `coluna` e `status`), `static/pad.css`, `static/presence.js` (redesenhar após `document.fonts.ready`) e `static/editor.js` (estados "salvo"/"salvando…" no `#st`)

**Interfaces:**
- Consumes: os componentes da Task 2 e `PadTheme` da Task 1.
- Produces:
  - `func (a *app) userDocs(u user) ([]docRow, error)`, extraída do `home` atual, com o mesmo SQL e a mesma ordem. O `docRow` ganha os campos `Owner string` (dono, vazio se for do próprio usuário) e `UpdatedAt time.Time`.
  - `func arvore(docs []docRow, me string) []noArvore`, com `type noArvore struct{ Nome, Caminho string; Pasta bool; Filhos []noArvore }`. Só os documentos do próprio usuário entram na árvore, montada pelos segmentos depois de `me/`, com as pastas antes dos documentos e cada grupo em ordem alfabética. Os compartilhados vêm numa lista à parte.
  - `docPage` ganha `Coluna *colunaData` (nil para visitante), com `type colunaData struct{ Arvore []noArvore; Compartilhados []docRow; Atual string }`.
  - `static/shell.js` expõe `PadShell = { abas: {ler(user), abrir(user, nome), fechar(user, nome)}, coluna: {aberta(user), alternar(user)}, pastas: {...} }`, com a lógica pura testável. Cuida também da ligação no DOM: o botão `#col` alterna a coluna, a gaveta abaixo de 768px fecha com Esc, com clique fora ou ao escolher um documento, e o seletor de tema `#tema` abre um `.menu` com "Seguir o sistema" e os grupos escuros e claros.
  - Barra de status: `● N pessoas` (a partir dos `pad:peers`), `linha L, coluna C` (seleção do textarea, a partir de 1), o `#st` ("salvo" com a bolinha `--ok` quando `!sync.pending()`, "salvando…" quando há pendente, "reconectando…" e os motivos de recusa como hoje) e o botão de tema.

- [ ] **Step 1: Testes que falham:**
  - Go `TestArvore`: `[ana/a, ana/p/x, ana/p/q/r/s/t, bia/z]` com `me="ana"` dá a pasta `p` (com a pasta `q` e o documento `x`) e o documento `a`, nessa ordem, com 5 níveis e sem `bia/z`.
  - Go `TestVisitanteSemColuna`: um anônimo em `/publico/x` e num documento público não recebe `id="coluna"`, `id="col"` nem `id="abas"`, nem o nome de outros documentos.
  - Go `TestEditorComColuna`: a ana logada em `/ana/a` recebe `id="coluna"` contendo `ana/p/x` e o compartilhado `bia/z`, com `aria-current="page"` no atual.
  - JS `shell.test.js`:
    - `abrir` mantém no máximo 8 abas, tira duplicatas e põe a atual no começo;
    - `fechar` remove;
    - as listas de usuários diferentes não se misturam;
    - um `localStorage` que lança exceção devolve listas vazias sem lançar.
- [ ] **Step 2: Rodar e ver falhar:** `go test -run 'TestArvore|TestVisitanteSemColuna|TestEditorComColuna' ./...` e `node --test static/*.test.js` → FAIL.
- [ ] **Step 3: Implementar** segundo `editor-v2.html` (aberta e recolhida) e `resto.html` (seletor de tema). As abas são links (`<a href="/<nome>">`) com um × para fechar. Os nomes longos são truncados com reticências e levam o `title`. Preserve todos os ids que o `editor.js` e o `presence.js` usam: `#t`, `#st`, `#gut`, `#mir`, `#lay`, `#area`, `#peers`, `#quem`, `#quem-menu`, `#seguir`, `#trava` e `#fim`.
- [ ] **Step 4: Verificar:** `go test -race ./... && node --test static/*.test.js` → PASS. No navegador, com duas abas no `/publico/x` e a ana logada num documento dela, confira:
  - os cursores, as seleções e o cadeado no lugar com a coluna aberta e fechada, depois de trocar entre Grafite, Papel e Alto contraste, e em 400px com a gaveta;
  - a troca de tema pela barra;
  - as abas;
  - o "salvando…" seguido de "salvo".

  Tire as capturas.
- [ ] **Step 5: Commit:** `feat: moldura Oficina do editor com coluna, abas e barra de status`

---

### Task 4: Tela de entrada com recentes (B2)

**Files:**
- Modify: `main.go` (`home`), `hub.go` (consulta de quem está num documento), `templates.html` (a definição `home`) e `static/pad.css`
- Test: `main_test.go` e `ws_test.go`

**Interfaces:**
- Consumes: `userDocs` e `colunaData` da Task 3 e as classes `.cartao`/`.campo`/`.btn.pri` da Task 2.
- Produces:
  - `func (h *hub) editores(name string) []string`: os logins distintos de quem tem `canEdit` e login, entre os peers conectados agora, em ordem alfabética. Leitores e visitantes não entram, e no `/publico` a lista é sempre vazia.
  - `func quando(t, agora time.Time) string`. Os textos são exatamente estes:
    - `agora` (menos de 1 min);
    - `há N min` (menos de 60 min);
    - `há N h` (no mesmo dia);
    - `ontem`;
    - `D mmm` com o mês em minúsculas e abreviado em PT-BR, como `12 out`, no mesmo ano;
    - `D mmm AAAA` em anos anteriores.
  - `func quemEsta(nomes []string) string`, que dá `""`, `Ana está aqui`, `Ana e Bia estão aqui` ou `3 pessoas aqui`. Os nomes são logins, com a primeira letra maiúscula só na exibição.
  - O `home` renderiza a moldura com a coluna aberta e, no centro, o campo de criar, "Continuar de onde parou" (seus documentos e os compartilhados, até 9) e "Compartilhados com você" (só os de outros donos).

- [ ] **Step 1: Testes que falham:**
  - `TestQuando`, com tabela: 30 s → `agora`; 5 min → `há 5 min`; 3 h no mesmo dia → `há 3 h`; dia anterior → `ontem`; 12/10 do mesmo ano → `12 out`; 12/10/2025 → `12 out 2025`.
  - `TestQuemEsta`, com os 4 casos.
  - `TestHubEditores`: com a ana editora e um leitor anônimo num documento público, o resultado é `[ana]`; no `/publico` é `[]`.
  - `TestEntradaVazia`: um usuário sem documentos recebe o campo de criar e nenhum `.cartao`.
  - `TestEntradaRecentes`: a ana com 2 documentos e 1 compartilhado recebe 3 blocos em "Continuar de onde parou" e 1 em "Compartilhados com você".
- [ ] **Step 2: Rodar e ver falhar:** `go test -run 'TestQuando|TestQuemEsta|TestHubEditores|TestEntrada' ./...` → FAIL.
- [ ] **Step 3: Implementar** segundo `inicio-v2.html` (B2). O `editores` segura só `h.mu` e o `d.mu` de cada documento, na ordem de travas já documentada em `hub.go`.
- [ ] **Step 4: Verificar:** `go test -race ./...` → PASS. No navegador, a entrada em Grafite e Papel, em 1280px e 400px, e o "Ana está aqui" aparecendo depois de recarregar com a Ana num documento.
- [ ] **Step 5: Commit:** `feat: tela de entrada com recentes e quem está em cada documento`

---

### Task 5: Compartilhar e página pública

**Files:**
- Modify: `templates.html` (o painel de compartilhar da definição `doc` e a definição `public`), `static/pad.css` e `static/shell.js` (abrir e fechar o `.pop`)

**Interfaces:**
- Consumes: `.pop`, `.seg`, `.chave` e `.btn.perigo` da Task 2, e a moldura da Task 3.
- Produces:
  - O painel "Compartilhar <nome>", com a alternância usuário/grupo (o mesmo `select name=tipo`, só que estilizado como `.seg` com radios). A lista tem "remover".
  - A chave "Público, só leitura" envia o mesmo `POST /_public/{name}` de hoje, como um botão com `role="switch"` e `aria-checked`.
  - "Apagar documento" mantém o `confirm()`.
  - O painel fecha com Esc e com clique fora, e abre sozinho quando `.NotFound` é verdadeiro, com o erro `Usuário ou grupo não encontrado.`
  - Na página pública, o visitante vê o caminho com o dono apagado, o `.selo` "só leitura", o botão "Entrar" (`/_login?next=/<nome>`) e, na barra de status, `<Nome> está editando agora` quando há editor presente (pelos `pad:peers`).

- [ ] **Step 1: Teste que falha** `TestPainelCompartilhar`: o dono recebe `role="switch"` com `aria-checked="false"` num documento privado e `"true"` num público. Os formulários `/_share`, `/_unshare`, `/_public` e `/_delete` continuam com os mesmos `action` e `name`. Os testes atuais de compartilhamento continuam verdes sem mudança.
- [ ] **Step 2: Rodar e ver falhar:** `go test -run TestPainelCompartilhar ./...` → FAIL.
- [ ] **Step 3: Implementar** segundo `resto.html`.
- [ ] **Step 4: Verificar:** `go test -race ./... && node --test static/*.test.js` → PASS. No navegador, faça o ciclo completo: compartilhar, remover, tornar público, abrir anônimo e tornar privado. Isso em Grafite e Areia, em 400px.
- [ ] **Step 5: Commit:** `feat: painel de compartilhar e página pública no visual Oficina`

---

### Task 6: Passada final de acabamento

**Files:**
- Modify: o que a verificação apontar. Em `README.md`, uma linha sobre os temas na seção do pad.

- [ ] **Step 1:** Percorra todas as telas da lista "Como saber que está pronto" do spec nos 8 temas, em 1280px e 400px. Teste a navegação só pelo teclado (Tab, Esc, setas no menu de tema) e com `prefers-reduced-motion` ligado. Anote cada desvio em relação aos mockups e corrija.
- [ ] **Step 2:** `test -z "$(gofmt -l .)" && go vet ./... && go test -race -count=1 ./... && node --test static/*.test.js` → tudo PASS.
- [ ] **Step 3: Commit:** `fix: acabamento do visual Oficina`
