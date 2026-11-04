# Conversas entre agentes pelo pad (MCP)

Data: 2026-11-04 · Status: rascunho para aprovação

## Por que

O Paulo roda vários Claude Code e Codex em PCs diferentes, cada um numa task. Quando eles precisam trocar informação, hoje é o Paulo quem faz de carteiro: copia o que um escreveu, cola num documento do pad, vai até o outro PC e cola no outro agente. Ele quer que os agentes façam isso sozinhos, pelo pad, logados na conta dele.

O modelo mental é o `SendMessage` dos subagentes do Claude Code: um agente manda uma mensagem e o outro devolve a resposta. A diferença é que aqui cada agente está numa máquina diferente, e o pad faz a ponte.

## Como é usado

- **Um agente "pai", central,** divide o trabalho e delega tasks. **Vários "filhos"** (Claude Code ou Codex), cada um em uma máquina, cada um com a sua task.
- **O pontapé é sempre do Paulo.** Ele abre cada agente e diz algo como "lê aí" ou "pega a conversa refatorar-login no pad". Ninguém precisa acordar sozinho.
- **O pai cria uma conversa por task** e escreve a task como primeira mensagem.
- **O filho trabalha e conversa só pela conversa dele.** Quando tem dúvida ou resultado, manda mensagem. Se a dúvida trava o trabalho, espera a resposta. Se não trava, segue com outras coisas e confere depois.
- **O pai fica de olho nas conversas dos filhos e responde.** Ele pode esperar em todas ao mesmo tempo e acorda na primeira que tiver novidade.
- **O Paulo acompanha e participa pelo navegador,** numa área própria do pad, em formato de chat.
- **Documentos do pad entram na conversa por `@pad`.** No terminal, o Paulo põe um documento no contexto do agente. Numa mensagem, qualquer um (o Paulo ou o pai) cita um documento, e o agente que recebe abre e lê. O agente também pode escrever em documentos.

## O que se constrói

### 1. Área MCP, separada dos documentos

Uma área própria **de cada usuário**, em `/<usuário>/_mcp` (por exemplo `/paulo/_mcp`), fora dos documentos normais (decisão do Paulo, durante a implementação). Cada usuário só abre a própria área. Na área de outro usuário, a resposta é o mesmo "não encontrado" dos documentos. O `/_mcp` sozinho redireciona para a área de quem está logado. As conversas não são documentos: não aparecem na coluna de documentos nem abrem no editor de texto. A área tem três partes:

- **Conversas:** lista com nome, última mensagem (autor e hora) e quem está nela agora. Botão para criar conversa.
- **Máquinas:** cadastro e revogação (seção 4).
- **Agora conectados:** quem está conectado agora (seção 4).

Link para a área na barra/menu onde já ficam **convidar** e **sair**, com o nome **mcp**. *O nome do link é escolha minha.*

### 2. Cada conversa é um chat

Página em `/<usuário>/_mcp/c/<nome>`. O nome segue as regras de nome de documento (`validName`), sem barras. *Escolha minha.*

- **Mensagens em balões,** com autor e hora. As do Paulo ficam de um lado e as dos agentes do outro. O texto é mostrado como texto puro, preservando quebras de linha e espaços, em fonte monoespaçada nos trechos entre crases triplas. Não há renderizador de Markdown. *Escolha minha.*
- **Caixa de envio no rodapé.** Enter envia e Shift+Enter quebra a linha. A mensagem do Paulo é uma mensagem como qualquer outra: os agentes recebem pelo `ler`/`esperar`.
- **Atualiza sozinho.** Mensagem nova aparece em ~1 s sem recarregar. O mecanismo (SSE, long-poll, o WebSocket que já existe) fica com quem constrói.
- **Quem está na conversa:** no topo, os agentes ativos nela (que mandaram, leram ou esperaram nos últimos ~2 min), com o nome `login · claude · notebook`. *Os 2 min são escolha minha.*
- **Links para documentos** dentro de mensagens (`@pad /paulo/notas`, `/paulo/notas` ou o endereço completo) viram links clicáveis.
- **Autoria é registrada:** cada mensagem guarda quem mandou (agente e máquina, ou o login do Paulo) e quando. Mensagem não é editada nem apagada, nem pelo navegador nem pelo MCP. Apagar a conversa inteira pode, pela lista. *Escolha minha.*
- Mensagens ficam no SQLite (`/data/pad.db`), com as tabelas que quem constrói definir.

### 3. Servidor MCP dentro do pad

- O próprio pad responde o MCP no transporte **Streamable HTTP**, em `https://pad.pauloholanda.com.br/<usuário>/_mcp`. É o mesmo caminho da área no navegador: o `POST` com `Authorization: Bearer` é o MCP, e o `GET` do navegador é a página. Uma chave só vale no endereço do próprio dono. Uma chave do paulo usada em `/ana/_mcp` é recusada como chave inválida. Nada para instalar nas máquinas: cada uma registra o endereço e a chave uma vez no Claude Code (`claude mcp add --transport http ...` com o header `Authorization: Bearer <chave>`) e no Codex (servidor HTTP com bearer token na config).
- **Autenticação:** cada máquina cadastrada tem uma chave, e a chave vale como a conta inteira do dono (decisão do Paulo). Ela dá acesso aos documentos que o Paulo acessa (os dele e os compartilhados com ele, pela mesma regra do `access`), e às conversas dele.
- **Quem é o agente: um apelido** (decisão do Paulo). Ao começar, cada agente se apresenta com a ferramenta `apresentar(apelido)`. O pai usa `pai`, e cada filho usa o nome da conversa dele (`login`). As instruções do servidor mandam fazer isso antes de tudo. Nas mensagens e telas, o agente aparece como `login · claude · notebook`: apelido, tipo e máquina. A máquina vem da chave. O tipo vem do `clientInfo.name` do `initialize`: Claude Code vira `claude`, Codex vira `codex`, e qualquer outro cliente aparece com o nome que mandou.
  - O apelido é a identidade dentro da conta. O "lido" e a regra de "outro autor" do `esperar` usam o apelido, e não a máquina nem o tipo. Por isso dois Claude no mesmo notebook (pai e filho) se ouvem normalmente.
  - O apelido sobrevive à reconexão. Se a sessão cai e volta, o agente se apresenta de novo com o mesmo apelido e continua de onde parou.
  - As outras ferramentas recusam com "apresente-se com `apresentar` primeiro" enquanto a sessão não tiver apelido.
  - Se outra sessão ativa (nos últimos ~2 min) já usa o mesmo apelido, o `apresentar` recusa com "apelido em uso por claude · desktop". Se a outra sessão estiver inativa, o apelido passa para a nova. *Escolha minha.*
  - O apelido segue as regras de nome de documento, sem barras, e não pode ser igual a um login do pad. *Escolha minha.*
- **Instruções do servidor:** o `initialize` devolve um texto curto explicando o fluxo pai/filho, quando usar cada ferramenta, o laço do `esperar` e que `@pad /caminho` numa mensagem é um documento a ler com `ler_documento`. Assim basta o Paulo dizer "lê aí".

### 4. Máquinas e conectados

- **Máquinas:** lista com nome, quando foi cadastrada e quando foi vista por último. Botão para cadastrar (pede só o nome) e botão para revogar.
- **Ao cadastrar,** a chave aparece **uma vez só**, junto com os comandos prontos para copiar e colar no Claude Code e no Codex. No banco fica só o hash.
- **Ao revogar,** os agentes daquela máquina caem na hora: a próxima chamada é recusada, e um `esperar` em curso volta com erro.
- **Agora conectados:** um resumo ("5 agentes: 3 Claude, 2 Codex") e uma tabela com uma linha por agente: apelido, máquina, tipo, conversa(s) em que está e última atividade. Dá para ver quantos Claude da máquina X e quantos Codex da máquina Y estão ativos ao mesmo tempo. Atualiza sozinha a cada poucos segundos. *Escolha minha.*
- Cada usuário vê e cadastra só as próprias máquinas. Qualquer usuário pode cadastrar, não só admin. *Escolha minha.*

### 5. Ferramentas de conversa

Nomes em português, como o resto do pad. *Escolha minha.*

| ferramenta | o que faz |
|---|---|
| `apresentar(apelido)` | dá o apelido desta sessão (seção 3). Precisa vir antes das outras. |
| `conversas` | lista as conversas da conta: nome, quem falou por último e quando, e se há mensagem que este agente ainda não leu |
| `criar_conversa(nome, mensagem)` | cria a conversa com a primeira mensagem. Erro se já existir. |
| `enviar(conversa, texto)` | acrescenta uma mensagem |
| `ler(conversa, tudo?)` | devolve as mensagens que este agente ainda não leu, com autor e hora, e marca como lidas. Se o agente nunca leu aquela conversa, devolve a conversa inteira (serve de contexto para quem chega). Com `tudo=true`, devolve a conversa inteira de novo, para quem perdeu o contexto (`/clear`, compactação). |
| `esperar(conversas, segundos)` | fica parado até haver mensagem não lida de **outro** autor em alguma das conversas (lista vazia = todas), ou até o tempo acabar. Se já houver não lidas, volta na hora. Devolve só as mensagens novas, agrupadas por conversa, e marca como lidas. Senão, devolve "nada ainda". |

- **"Lido" é guardado por apelido e conversa,** e não por conexão. Fica no SQLite e sobrevive à reconexão. Assim o pai que volta não recebe o histórico inteiro de todas as conversas, e as respostas que chegaram enquanto ele estava fora aparecem como não lidas. Numa conversa que aquela identidade nunca leu, o `esperar` só considera não lidas as mensagens dos outros posteriores à última mensagem dela ali. Se ela nunca falou ali, considera todas. *Escolha minha.* Limite aceito: subagentes do Claude Code usam a mesma conexão do pai, então o que um subagente lê conta como lido para o pai. O `tudo=true` cobre esse caso.
- **Tempo do `esperar`:** teto fixo de **50 s**. O Codex corta chamadas de ferramenta em 60 s por padrão (`tool_timeout_sec`), e os entrypoints do Traefik v3 também usam 60 s. A stack pathoplay não é mexida. As instruções dizem para o agente chamar de novo em laço se ainda precisar esperar. *Escolha minha.*
- **Esperar fazendo outras coisas** não precisa de ferramenta própria: o agente chama `ler` ou `conversas` entre um passo e outro do trabalho.

### 6. Documentos do pad: `@pad` e ferramentas

Decisão do Paulo: os dois jeitos de `@pad`, e o agente lê e escreve.

- **No terminal (`@pad`):** os documentos que a conta acessa são expostos como **resources** do MCP (`pad://paulo/notas`). O Claude Code já oferece resources no autocompletar do `@`: o Paulo digita `@pad`, escolhe o documento e o texto entra no contexto. No Codex, vale o que o cliente oferecer para resources, e a ferramenta abaixo cobre o resto.
- **Na mensagem (`@pad /paulo/notas`):** o texto vai para o agente como foi escrito, e o agente abre o documento com `ler_documento`. O pad não cola o conteúdo dentro da mensagem. *Escolha minha.*

| ferramenta | o que faz |
|---|---|
| `ler_documento(caminho)` | devolve o texto atual do documento e a **versão** lida. Aceita `/paulo/notas`, `paulo/notas`, `@pad /paulo/notas` ou o endereço completo. |
| `escrever_documento(caminho, texto, versao)` | troca o texto do documento pelo texto novo, partindo da versão que o agente leu. Para criar um documento que não existe, a versão vai vazia, e ele é criado no espaço do Paulo (dono = ele), como o `POST` do pad faria. |

- **A escrita nunca apaga o que outra pessoa digitou depois da leitura do agente.** A mudança do agente é a diferença entre o texto da versão que ele leu e o texto novo, e não "documento inteiro por cima". *Escolha minha.*
  - Se o documento está aberto na edição ao vivo e a versão ainda está no `log` do `liveDoc`, a diferença é transformada pelas operações posteriores (`since(rev)`), como o `submit` já faz. Quem está olhando vê o texto mudar.
  - Se a versão não dá mais para juntar (saiu do `log`, ou o documento foi gravado em disco depois da leitura e o texto atual é diferente do lido), a ferramenta recusa com "documento mudou desde a sua leitura, leia de novo". Nunca sobrescreve.
  - Se a mudança cair num trecho travado por seleção, recusa com "trecho travado por <nome>", e o agente tenta depois.
  - Com ninguém conectado e o texto igual ao lido, grava como o `save` faz hoje, respeitando o `writeMu` e a ordem de locks do `hub.go`.
  - O formato da versão (revisão do `liveDoc`, hash do texto ou os dois) fica com quem constrói.
- **Documento normal não registra quem escreveu,** como hoje. A autoria só existe nas conversas (decisão do Paulo). Enquanto escreve, o agente pode aparecer nas bolhas do documento por alguns segundos, como "claude · notebook", mas isso fica com quem constrói.
- Valem os mesmos limites de hoje (2 MiB, 256 KB no `/publico`, níveis de pasta).

## Fora do escopo

- Agente que acorda sozinho quando chega mensagem (push para a sessão, `/loop`). O pontapé é manual.
- Agente que interrompe a própria task porque chegou mensagem.
- Anexos, editar ou apagar mensagem.
- Conversa entre contas diferentes. Tudo acontece dentro da conta do Paulo.
- Histórico de autoria nos documentos normais.
- Ferramenta para listar documentos. No terminal, a lista de resources já cumpre isso.
- Renderizar Markdown no chat.

## Anti-metas (falhou, mesmo que funcione)

- Uma mensagem nunca chegar a quem está lendo ou esperando naquela conversa.
- O agente escrever num documento e apagar o que alguém digitava nele ao mesmo tempo.
- O agente ficar pendurado: um `esperar` que nunca volta, ou que segura a conexão depois da máquina ser revogada.
- A chave vazar por URL, log ou página: ela só aparece uma vez, na criação, e só vai em header.
- Precisar explicar o protocolo para o agente toda vez.

## Restrições

- Go, binário único, SQLite, como o pad já é. Rotas novas entram em `routes()` no `main.go`. `/<usuário>/_mcp` fica reservado: um documento de nome `_mcp` logo abaixo do usuário não abre pelo navegador.
- Respeitar a ordem de locks do `hub.go` (`a.writeMu` → `hub.mu` → `liveDoc.mu`).
- A tela da área MCP segue o visual Oficina e os temas das outras telas (`templates.html`, `static/moldura.css`).
- Dependência nova só se compensar. A SDK oficial de Go do MCP (`github.com/modelcontextprotocol/go-sdk`) é candidata. Implementar à mão só o necessário (`initialize`, `tools/*`, `resources/list`, `resources/read`, `ping`) também vale. **Fica com quem constrói**, desde que funcione com o Claude Code e o Codex atuais.
- Testes em `go test ./...` (e `node --test` se houver JS novo), como o resto. O README ganha uma seção curta de como cadastrar uma máquina.
- Deploy atrás do Traefik (`compose.traefik.yaml`), sem mexer na stack pathoplay. Conferir no plano que uma requisição de 50 s passa.
- Conferir no plano os `clientInfo.name` que o Claude Code e o Codex mandam hoje, e se o Codex atual expõe resources de MCP.

## Fica com quem constrói

- SDK ou implementação própria do MCP; formato do texto que as ferramentas devolvem ao agente.
- Esquema das tabelas novas (máquinas, conversas, mensagens, estado de leitura).
- Como o chat se atualiza ao vivo.
- Como a escrita do agente vira operação no `liveDoc` sem WebSocket.
