# Visual novo do pad: Oficina, com temas

Data: 2026-11-03 · Status: rascunho para aprovação

## Por que

O pad funciona, mas as telas usam o estilo padrão do navegador (botões e campos crus, fonte do sistema, pouco espaçamento). O Paulo quer um visual moderno e bonito, com vários temas, mantendo o pad leve e sem build de front-end.

Ele escolheu cada parte vendo mockups. Os arquivos aprovados estão em `docs/superpowers/specs/2026-11-03-visual-oficina/`. Eles são a referência visual; quando este texto e um mockup discordarem, vale o texto.

## O que foi decidido (pelo Paulo, nos mockups)

- **Direção: Oficina** (`direcao.html`, opção B) — cara de editor de código moderno: barra fina em cima, numeração de linhas, barra de status embaixo.
- **Temas: os 8** (`temas.html`), todos na base Oficina, com estes valores:

  | tema | tipo | fundo | barras | texto | apagado | linhas | destaque | texto sobre o destaque |
  |---|---|---|---|---|---|---|---|---|
  | Grafite | escuro | `#16181f` | `#1d2029` | `#d7dae4` | `#858a9c` | `#262a35` | `#7758f5` | `#fff` |
  | Madrugada | escuro | `#0e1726` | `#142035` | `#d6e2f3` | `#7f93b3` | `#1d2a42` | `#38bdf8` | `#06121f` |
  | Café | escuro | `#1b1613` | `#241d19` | `#ece2d8` | `#a08e80` | `#30271f` | `#f59e0b` | `#1b1613` |
  | Floresta | escuro | `#111a15` | `#17231c` | `#d9eadf` | `#7d9a88` | `#1f2f26` | `#34d399` | `#0b1510` |
  | Papel | claro | `#f3f4f7` | `#e7e9ef` | `#222634` | `#606677` | `#d9dce5` | `#7758f5` | `#fff` |
  | Céu | claro | `#f2f6fc` | `#e3ebf7` | `#1a2638` | `#596982` | `#d3deee` | `#2563eb` | `#fff` |
  | Areia | claro | `#f5f2ec` | `#ebe5da` | `#2b241c` | `#6e6253` | `#ddd4c5` | `#c2410c` | `#fff` |
  | Alto contraste | escuro | `#000` | `#000` | `#fff` | `#d0d0d0` | `#fff` | `#ffd400` | `#000` |

  Os tons de "apagado" e o violeta foram ajustados levemente em relação ao mockup para o texto passar 4,5:1: "apagado" contra o fundo e contra as barras, e o texto sobre o destaque (botão principal) contra o destaque. *Ajuste meu.* Um teste em Go deve calcular esses contrastes para os 8 temas e falhar abaixo de 4,5:1, para que ajustes futuros não quebrem a leitura. O destaque usado como cor de detalhe (anel de foco, aba ativa) precisa de 3:1 contra o fundo.
- **Fontes** (`fontes.html`, opção B): **Space Grotesk** na interface, **JetBrains Mono** no texto do editor (e nos nomes de documento, que são caminhos).
- **Componentes: Relevo** (`componentes.html`, opção B): cantos de 9px; botão principal com degradê leve na cor de destaque, brilho fino em cima e sombra colorida; botões secundários com fundo de campo e contorno de 1px; campos com sombra interna; menus e avisos flutuantes com sombra mais funda e cantos de 12px/10px. O foco do teclado aparece como anel de 2px na cor de destaque.
- **Editor** (`editor-v2.html`): coluna de documentos à esquerda, **recolhível** por um botão no canto esquerdo da barra de cima; recolhida, a tela fica como a opção "Foco" (só barra em cima, editor, barra de status). Documentos abertos aparecem em **abas** acima do texto. Barra de status embaixo: quantas pessoas estão no documento, sua linha e coluna, "salvo", e o seletor de tema.
- **Login** (`inicio.html`, topo): cartão centralizado sobre um degradê suave da cor de destaque, nome `pad_` em JetBrains Mono com o `_` na cor de destaque, frase "Blocos de notas compartilhados, ao vivo.", campos, botão Entrar e o atalho para a área pública.
- **Depois de entrar** (`inicio-v2.html`, opção B2): a mesma moldura do editor, com a coluna aberta; no centro, o campo para criar documento e os **recentes em blocos** ("Continuar de onde parou" e "Compartilhados com você"), cada bloco com o nome, quando mudou e quem está nele agora.
- **Compartilhar, tema, convite e página pública** (`resto.html`), aprovados como estão: painel flutuante de compartilhar (alternância usuário/grupo, lista com "remover", chave "Público, só leitura", "Apagar documento" em vermelho); seletor de tema subindo da barra de status ("Seguir o sistema" primeiro, depois escuros e claros com amostra); página de convite com o link, Copiar e Gerar outro; documento público para visitante com selo "só leitura", botão Entrar e a frase de quem está editando.

## Como funciona

### Temas

- Padrão: **seguir o sistema** — Papel quando o sistema está claro, Grafite quando escuro. *Escolha minha.*
- A escolha de cada pessoa fica no navegador dela (`localStorage`), vale em todas as telas e é aplicada antes da primeira pintura, para não piscar o tema errado.
- Todas as telas usam os mesmos tokens de cor; nenhuma cor fixa fora da tabela, exceto as cores das pessoas.
- **Cores das pessoas** (cursores, seleções, bolinhas): a paleta de 12 cores atual tem de continuar legível nos 8 fundos. O teste de contraste que já existe (`TestPaletaContraste`) passa a checar contra os 8 fundos; se alguma cor não passar 3:1 em algum fundo, troque a cor da paleta (uma paleta só, para todos os temas).
- `color-scheme` acompanha o tipo do tema (claro/escuro), para barras de rolagem e controles nativos.
- **Tokens além da tabela** (*escolha minha*): cada tema também define `--field` (fundo de campo, botão secundário, blocos da B2), `--hov` (hover e fundo do avatar e do selo "só leitura"), `--err` (vermelho de apagar e erros) e `--ok` (bolinha verde do "salvo"). O construtor escolhe os valores fiéis aos mockups, e o teste de contraste cobre também: `--mut` e `--fg` sobre `--field` ≥ 4,5:1, `--err` sobre `--bg` e `--field` ≥ 4,5:1, `--ok` sobre `--bar` ≥ 3:1.
- A paleta das pessoas hoje tem `#16a34a`, que fica abaixo de 3:1 no Areia; essa cor tem de ser trocada.

### Coluna de documentos

- Mostra suas pastas e documentos em árvore (a partir dos nomes, ex.: `projetos/ideias` vira a pasta `projetos` com `ideias`), depois "compartilhados" (documentos de outros a que você tem acesso), depois o link para a área pública, e "+ novo documento" no fim.
- O documento aberto fica destacado. Pastas abrem e fecham; o estado aberto/fechado de cada pasta e da coluna inteira fica no navegador. *Escolha minha.*
- No celular (largura < 768px) a coluna começa fechada e abre por cima do editor, como uma gaveta, fechando ao escolher um documento ou tocar fora.
- Quem não está logado (visitante no `/publico` ou num documento público) não vê a coluna nem o botão.
- O dado da coluna é a mesma consulta que a lista de documentos de hoje já faz (`home`).

### Barra de status: "salvo"

*Escolha minha, sem mexer no protocolo da edição ao vivo:* "salvo" (com a bolinha `--ok`) quer dizer que o servidor já confirmou todas as suas alterações (nada pendente no cliente); o servidor grava em disco até 2 s depois. Enquanto há alteração pendente aparece "salvando…"; desconectado, "reconectando…"; numa recusa, o motivo (como hoje).

### Abas

- *Escolha minha, para não multiplicar conexões e editores na mesma página:* as abas são a lista dos documentos que você abriu recentemente neste navegador (até 8, guardada no navegador). Clicar numa aba navega para aquele documento (carrega a página dele); o × tira da lista. Não há vários editores vivos ao mesmo tempo na mesma página.
- A aba do documento atual fica destacada e entra na lista ao abrir.

### Tela depois de entrar (B2)

- Campo "nome do novo documento" + botão Criar (mesmo comportamento do formulário de hoje).
- "Continuar de onde parou": seus documentos e os compartilhados, por última alteração, em blocos (até 9). Cada bloco: nome, quando mudou ("há 2 min", "ontem", "12 out") e, se houver gente no documento agora, quem está ("Ana está aqui", "Ana e Bia estão aqui", "3 pessoas aqui").
- "Compartilhados com você": os de outros donos, mesmo formato.
- Sem documento nenhum: o centro mostra só o campo de criar e uma frase curta.
- Quem está no documento vem da memória do servidor (o hub da edição ao vivo), lido **no carregamento da página** (não se atualiza sozinho). *Escolha minha.* Só aparecem nomes de login de quem tem acesso de edição; leitores e visitantes não aparecem. *Escolha minha, seguindo a regra de nomes da edição ao vivo.*

### Telas que não foram desenhadas

- **Criar conta** (`/_join/...`) e **mensagens/erros** usam o mesmo cartão centralizado do login. *Escolha minha.*
- **Área pública** (`/publico`): editor na moldura Oficina sem a coluna para visitante; com a coluna para quem está logado.

## Fora do escopo

- Mudar o comportamento da edição ao vivo, das travas, do compartilhamento ou do login — só a aparência e a navegação (coluna, abas, tela de entrada).
- Editor rico (negrito, títulos formatados); o texto continua texto puro em fonte monoespaçada. O `# ` colorido nos mockups é só ilustração; realce de sintaxe não entra.
- Escolher tema por documento ou guardar a escolha no servidor.

## Anti-metas

- Ficar mais lento ou pesado para abrir: nada de framework, CDN ou build. As fontes vêm do próprio pad.
- Quebrar a edição ao vivo: cursores fora do lugar por causa da fonte nova, da coluna ou das abas.
- Texto ilegível em algum tema (contraste abaixo de 4,5:1 em texto, ou 3:1 nas cores das pessoas).
- Piscar o tema errado ao carregar.

## Restrições

- Continua um binário Go com `html/template` e JS/CSS sem build; arquivos estáticos em `static/` servidos por `/_static/`.
- Fontes **embutidas** (woff2, licença OFL) em `static/`, sem Google Fonts. Só os pesos usados (Space Grotesk 400/500/600/700, JetBrains Mono 400/600) e subconjunto latino.
- A camada de cursores da edição ao vivo mede o texto com a fonte do editor: ela tem de recalcular quando a fonte embutida terminar de carregar (`document.fonts.ready`), e textarea, espelho e calha continuam usando uma regra de fonte só.
- Acessibilidade: foco visível por teclado em tudo; menus e coluna operáveis por teclado; `prefers-reduced-motion` desliga animações da gaveta e dos menus; alvos de toque ≥ 40px no celular.
- Funciona a partir de ~360px de largura.

## Deixado para o construtor

- Como organizar o CSS (um arquivo em `static/` ou no template) e os nomes das variáveis.
- O desenho exato de ícones (usar caracteres ou SVG inline pequeno; nada de biblioteca de ícones).
- Espaçamentos finos, desde que fiéis aos mockups.
- Como montar a árvore de pastas no servidor ou no navegador.
- Onde ficam "convidar" e "sair" (sugestão: num menu do avatar), o tamanho da fonte e a altura de linha do editor, o seletor de tema nas telas sem barra de status.
- Abas e pastas abertas guardadas no navegador separadas por usuário, para quem usa o mesmo navegador não ver os nomes de documentos de outra pessoa.

## Como saber que está pronto

- Todas as telas (login, criar conta, entrada B2, editor com coluna aberta e fechada, abas, compartilhar, seletor de tema, convite, documento público, área pública, mensagens) batem com os mockups nos 8 temas, em desktop e em ~400px.
- `TestPaletaContraste` cobre os 8 fundos; os testes atuais (Go e JS) continuam verdes.
- Verificação no navegador: cursores e seleções da edição ao vivo continuam no lugar com a fonte nova, com a coluna aberta e fechada, e depois de trocar de tema.
