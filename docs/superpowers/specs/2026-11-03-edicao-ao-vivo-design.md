# Edição ao vivo no pad: presença, cursores coloridos e trava por seleção

Data: 2026-11-03 · Status: rascunho para aprovação

## Por que

Hoje o editor é um `textarea` que manda o documento inteiro num `POST` 700 ms depois da última tecla. Quando duas pessoas editam o mesmo documento, quem salva por último sobrescreve o outro, e ninguém vê a mudança alheia sem recarregar. O Paulo quer o pad funcionando como o Google Docs: todos veem o texto mudar na hora, sabem quem está na página e onde cada um está escrevendo, e ninguém perde o que digitou.

## O que a pessoa vê

A tela foi aprovada a partir dos mockups da opção **A** (presença no cabeçalho) e do modo acompanhar. Os arquivos estão em `.superpowers/brainstorm/169106-1793721576/content/editor-presenca.html` e `acompanhar.html`, fora do git.

- **Cabeçalho:** uma bolinha colorida com a inicial de cada pessoa na página, sobrepostas como no Google Docs. A sua também aparece.
- **Cor por pessoa:** cada pessoa tem uma cor que se repete na barrinha do cursor, no número da linha (em negrito na cor dela) e no fundo da seleção.
- **Cursor dos outros:** uma barrinha vertical de 2 px na cor da pessoa, com uma etiqueta com o nome logo acima.
- **Seleção dos outros:** o trecho fica com fundo claro na cor da pessoa e um selo com cadeado e o nome. Enquanto a seleção durar, o trecho está travado.
- **Menu das bolinhas:** clicar nas bolinhas abre um menu pequeno com cor, nome e o que cada um está fazendo ("digitando na linha 2", "selecionou as linhas 4–5", "lendo"). A sua linha também aparece, como "você".
- **Acompanhar:** clicar no nome de outra pessoa liga o modo acompanhar. A tela rola até ela e segue cada movimento, a área do editor ganha uma borda na cor dela e uma faixa mostra "acompanhando bruno" com um botão "parar".

## Como funciona

### Edição ao mesmo tempo

Cada um digita onde quiser, e as mudanças de todos aparecem para todos em menos de um segundo, numa rede normal. Duas pessoas digitando em pontos diferentes nunca apagam o trabalho uma da outra. O texto final é o mesmo para todos.

### Desfazer (Ctrl+Z / Ctrl+Shift+Z / Ctrl+Y)

Decidido pelo Paulo: **cada um desfaz só o que fez**, como no Google Docs. O Ctrl+Z nunca apaga nem altera o que outra pessoa digitou. O editor tem um histórico de desfazer próprio, feito de operações (as do próprio usuário, transformadas contra as que chegam dos outros), e o histórico nativo do `textarea` deixa de valer. Digitação seguida vira um passo só de desfazer, como no navegador. Um desfazer que cairia num trecho travado por outro é recusado com o mesmo aviso da trava. Precisa de testes próprios.

### Trava por seleção

- Quem seleciona um trecho (seleção com pelo menos 1 caractere) trava aquele trecho para os outros.
- Os outros não conseguem inserir no meio do trecho travado nem apagar qualquer caractere dentro dele. Inserir exatamente na borda e apagar o caractere logo do lado de fora são permitidos. Exemplo: com a trava em [5,10), o Backspace na posição 5 apaga o caractere 4. *Escolha minha.*
- A trava é da **aba**, não da pessoa. Outra aba da mesma pessoa é bloqueada como qualquer outra. *Escolha minha.* Quem tentar editar ali não consegue, e aparece um aviso curto: "trecho travado por bruno".
- O dono da trava edita o trecho normalmente. Se ele digita por cima da seleção, ela some e a trava acaba.
- A trava se solta quando a seleção acaba, quando a pessoa sai ou cai, ou depois de **30 s sem atividade** dela (sem tecla, sem mexer o cursor ou a seleção). Isso evita que alguém ausente segure um trecho. *Os 30 s são escolha minha.*
- Se a seleção de alguém encosta num trecho já travado por outro, a trava não sai: vale a de quem chegou primeiro. A seleção dessa pessoa aparece sem cadeado. *Escolha minha.*

### Quem aparece e com que nome

- **Documentos com login** (`/usuario/...`, inclusive os compartilhados): aparece o nome de login.
- **Área `/publico`:** todo mundo aparece com um nome de visitante aleatório e engraçado, de bicho brasileiro com adjetivo ("Capivara Anônima", "Tatu Curioso", "Jabuti Apressado"), no estilo do Google Docs. Isso vale até para quem fez login, porque o `/publico` já esconde o nome próprio. *Mostrar o visitante mesmo com login é escolha minha.* Quem dá o nome é o servidor, para ninguém se passar por outra pessoa. O navegador guarda o nome e o reapresenta ao reconectar ou recarregar, para a pessoa continuar a mesma capivara.
- **Documento público só leitura:** quem lê sem acesso de edição também aparece nas bolinhas, vê o texto mudar ao vivo e não pode digitar nem travar trechos. Pode selecionar e mover o cursor, que os outros veem sem cadeado, e pode ser acompanhado. No menu aparece como "lendo". *Escolha minha.*
- **Nomes num documento público** (`/usuario/...` com `public=1`): o nome de login só aparece entre quem tem acesso de edição. Quem só lê vê todo mundo como visitante, e quem só lê aparece como visitante para todos. Por isso o servidor monta a lista de presença para cada destinatário. *Escolha minha: hoje a página pública não revela quem tem acesso, e isso continua assim.*
- **Mais de uma aba:** a mesma pessoa em duas abas é **uma** bolinha e uma cor, mas cada aba tem o próprio cursor. *Escolha minha.*

### Fora do escopo

- Histórico ou autoria do texto depois que a pessoa sai: só vale o que acontece no momento.
- Chat, comentários, avatares com foto.
- Editar sem conexão. Quando a conexão cai, o editor fica só leitura com "reconectando…" até voltar.

### Anti-metas (falhou, mesmo que funcione)

- Perder qualquer coisa que alguém digitou.
- O texto ficar diferente entre duas pessoas olhando o mesmo documento.
- Expor o nome de login de alguém no `/publico`, ou deixar quem não tem acesso receber o texto ou a presença de um documento.
- Uma trava que nunca se solta.

## Restrições

- Continua um único binário Go servindo HTML com `html/template` e JavaScript embutido no `templates.html`, **sem build de front-end** e sem framework.
- Uma única instância (SQLite em `/data/pad.db`, textos em `/data/docs/<nome>.txt`). Estado em memória é aceitável.
- Atrás do Traefik da vpsarm (`compose.traefik.yaml`), que repassa WebSocket sem configuração extra.
- Valem as regras de acesso que já existem (`access`, a criação só dentro de `/<usuario>/`, os limites de 2 MiB por documento e de 256 KB no `/publico`, o teto de 50 mil itens e a limpeza dos mais antigos).
- A renovação deslizante da sessão (`currentUser`) continua valendo.

## Decisões técnicas (tomadas na conversa)

- **WebSocket com `github.com/coder/websocket`.** A biblioteca padrão do Go não tem WebSocket. É a única dependência nova.
- **O servidor manda (operational transformation centralizada).** Para cada documento aberto, o servidor guarda na memória o texto, um número de revisão e o log recente de operações. Cada cliente manda operações sobre a revisão que conhece ("na revisão 41: manter 120, inserir 'x', manter o resto"). O servidor transforma a operação contra as que chegaram antes, aplica, aumenta a revisão e repassa a todos. O cliente tem no máximo uma operação a caminho e acumula as seguintes até receber a confirmação, como no modelo do ot.js/Google Wave. A trava por seleção é decidida no servidor, que conhece a ordem real das operações.
- **Editor:** continua o `textarea`. Por trás dele vai uma camada (uma div espelho com a mesma fonte, quebra de linha e rolagem) que desenha cursores, seleções, etiquetas e a cor das linhas. A fonte é monoespaçada, mas a quebra de linha automática precisa ser respeitada. Os números de linha ficam numa calha à esquerda, como no mockup.
- **Gravação:** continua no mesmo `.txt`, com o `writeDoc` atômico. O servidor grava sozinho uns 2 s depois da última operação, quando a última pessoa sai e quando o processo encerra (SIGTERM do `docker compose stop`). A coluna `docs.updated` é atualizada na gravação, e a criação do registro em `docs` acontece na primeira operação de quem pode criar, como o `save` faz hoje.

## Casos de borda (escolhas minhas, para o construtor seguir)

- **Unidade das posições:** o protocolo usa a mesma unidade do `textarea` (unidades UTF-16 do JavaScript). O servidor converte ao gravar. É obrigatório um teste com emoji e acentos.
- **Operação recusada pelo servidor** (trava que o cliente ainda não conhecia, ou limite de tamanho): o servidor responde recusando, e o cliente descarta o estado local e se ressincroniza com o texto atual. O cliente já bloqueia localmente, com o que sabe das travas, para que isso seja raro.
- **Reconexão:** o cliente reconecta sozinho, com espera crescente. Ao voltar, informa a última revisão confirmada e reenvia a operação que estava a caminho. Se o servidor ainda tem o log desde aquela revisão, aplica normalmente. Se não tem, o cliente recarrega o texto e a operação pendente é reaplicada por cima com transformação, ou, se não der, a pessoa é avisada. Como o editor fica só leitura enquanto está desconectado, o máximo pendente é o que estava a caminho.
- **Acesso que muda durante a conexão:** quando o dono remove um compartilhamento, apaga o documento ou torna privado um documento público, o servidor desconecta na hora quem perdeu acesso. A tela mostra "você perdeu acesso a este documento" ou "documento apagado". As sessões abertas são reconferidas periodicamente (a cada 1 min), para pegar `passwd`, logout e expiração.
- **O `POST` de hoje** (`POST /{name...}`, usado pelos testes e por quem usa `curl`) continua existindo, mas é **recusado com 409** enquanto houver alguém conectado ao documento pelo WebSocket. Sem ninguém conectado, grava como hoje. Isso evita que um "substituir tudo" sem revisão apague o que está sendo digitado. No deploy, as abas abertas com o JavaScript antigo vão receber 409 e mostrar "erro ao salvar (409)". A mensagem 409 diz "recarregue a página", e o JavaScript antigo mostra só o código. *Escolha minha.*
- **Nome de visitante:** o servidor assina o nome que dá (HMAC com uma chave guardada no SQLite) para o navegador poder reapresentá-lo sem conseguir inventar outro.
- **Acesso reconferido** a cada 1 min junto com a sessão, e não só a sessão, porque grupos mudam pela linha de comando, em outro processo.
- **Desligamento ordenado:** o `main` passa a tratar SIGTERM, grava os documentos abertos e só então encerra. Hoje ele só chama `log.Fatal(ListenAndServe)`.
- **Visitantes:** limite de conexões por IP e por documento no `/publico`, para um script não lotar a memória.
- **Teclados:** acentos digitados em duas teclas e teclados de celular (`compositionstart`/`compositionend`) não podem perder nem duplicar texto. A operação sai quando a composição termina. O foco é o computador, mas no celular o editor tem de funcionar.
- **Limpeza do `/publico`:** um documento aberto no momento não é apagado pela limpeza dos mais antigos.
- **Limites:** uma operação que faria o documento passar do limite (2 MiB, ou 256 KB no `/publico`) é recusada, e o cliente mostra o motivo.
- **Origem:** o handshake do WebSocket só é aceito da mesma origem (checagem padrão do `coder/websocket`), para outro site não usar o cookie de quem está logado. Cada mensagem tem tamanho máximo.
- **Cores:** paleta fixa de umas 12 cores bem distintas e legíveis em fundo claro e escuro. O servidor sorteia, evitando repetir cor entre quem está presente. A mesma pessoa em duas abas recebe a mesma cor.
- **O modo acompanhar segue o cursor** da pessoa, não a rolagem dela. Não é preciso transmitir a rolagem.
- **O modo acompanhar para** quando você clica em "parar", quando digita ou rola a página por conta própria, ou quando a pessoa seguida sai.
- **Presença:** cursor e seleção são enviados no máximo a cada ~50 ms. Quem cai some das bolinhas em poucos segundos (o ping do WebSocket detecta a queda).

## Deixado para o construtor

- O formato exato das mensagens do protocolo.
- A estrutura interna do hub e dos documentos abertos, e quantos arquivos Go isso ocupa.
- A paleta de cores exata e a lista de bichos e adjetivos (em PT-BR, divertida, sem nada ofensivo).
- A técnica exata da camada que desenha os cursores sobre o `textarea`, desde que respeite a quebra de linha e a rolagem.
- O tamanho do log de operações guardado em memória.

## Como saber que está pronto

- Testes em Go para a transformação de operações (incluindo emoji e acentos), para as regras de trava (bordas, sobreposição, expiração por inatividade) e para o acesso pelo WebSocket (sem acesso não conecta, perder acesso derruba a conexão, `/publico` não expõe nome de login).
- Um teste de integração com dois ou três clientes WebSocket digitando ao mesmo tempo, que termina com o mesmo texto em todos e no disco.
- `go test ./...` passando, e os testes atuais do `POST` continuando verdes.
- Verificação manual no navegador com duas janelas: cursores, cores, travas, menu, acompanhar e reconexão.
