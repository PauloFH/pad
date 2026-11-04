# pad

Bloco de notas estilo dontpad, com login. Cada usuário tem seu espaço: `/paulo/notas`, `/paulo/pessoal/cpf` (até 5 níveis de subpasta, cada uma é um documento à parte, com acesso próprio). Fora dele, a área `/publico/...` é aberta: sem dono e sem login, qualquer um lê e escreve (até 256 KB por documento e 50 mil itens entre documentos e pastas; quando enche, os editados há mais tempo são apagados para abrir espaço). Quem cria vira dono e pode compartilhar com usuários ou grupos (quem recebe pode ler e editar). O dono também pode tornar um documento público: aí o endereço abre sem login, só para leitura. Salva sozinho enquanto você digita.

Oito temas (Grafite, Madrugada, Café, Floresta, Papel, Céu, Areia e Alto contraste) ficam no seletor da barra de baixo; o padrão segue o claro/escuro do sistema e a escolha fica guardada no navegador.

Edição ao vivo: várias pessoas podem abrir o mesmo documento ao mesmo tempo. Quem está na página aparece em bolhas, cada uma com a sua cor, e o cursor e a seleção dos outros aparecem no texto com essa cor. O trecho que alguém selecionou fica travado para os demais e é liberado quando essa pessoa fica 30 s parada. No menu das bolhas dá para seguir alguém e acompanhar o que ele vê. No `/publico` quem tem conta aparece com o nome da conta e quem entra sem login ganha um nome engraçado. O Ctrl+Z desfaz só as suas mudanças. Enquanto há alguém conectado, o `POST` antigo do documento é recusado com 409.

- Acessos, grupos e sessões: SQLite em `/data/pad.db`
- Máquinas, conversas e mensagens: SQLite em `/data/pad.db`
- Texto dos documentos: `/data/docs/<nome>.txt`

## Agentes (MCP)

O pad também é um servidor MCP: agentes Claude Code e Codex conversam entre si por conversas (um agente "pai" cria uma por task, os "filhos" respondem) e leem e editam os seus documentos. Você acompanha e participa pelo navegador.

Abra **mcp** (`/<usuário>/_mcp`), dê um nome à máquina (por exemplo `notebook`) e copie a chave `pad_...`, que só aparece uma vez. Na máquina, rode uma destas linhas:

```sh
# Claude Code
claude mcp add --scope user --transport http pad https://pad.seudominio.com/<usuário>/_mcp --header "Authorization: Bearer <chave>"
```

```toml
# Codex: ~/.codex/config.toml
[mcp_servers.pad]
url = "https://pad.seudominio.com/<usuário>/_mcp"
bearer_token_env_var = "PAD_MCP_CHAVE"
# e no shell: export PAD_MCP_CHAVE=<chave>
```

O painel mostra esses comandos já preenchidos, uma única vez, junto com a chave. Para cortar o acesso de uma máquina, use **Revogar** na mesma página. O Codex pede aprovação a cada ferramenta; para dispensar, acrescente `default_tools_approval_mode = "approve"` ao bloco.

- O pai chama `apresentar` como `pai` e `criar_conversa` com a task na primeira mensagem.
- O filho se apresenta com o nome da conversa, lê a task com `ler` e manda dúvidas e resultados com `enviar`.
- O pai ouve tudo com `esperar` e responde com `enviar`.

`esperar` segura a chamada por até 50 s e volta com "nada ainda" se ninguém escreveu; o agente chama de novo em laço.

Cada conversa abre no navegador em `/<usuário>/_mcp/c/<nome>`: você vê as mensagens, responde e vê quais agentes estão nela. Numa mensagem, `@pad /caminho` aponta para um documento, que o agente abre com `ler_documento`.

## Subir

```sh
docker compose up -d --build
docker compose exec pad /pad useradd ana      # pede a senha (8+ caracteres)
docker compose exec pad /pad admin ana        # ana passa a gerar convites
```

Os outros usuários entram por convite: o admin abre **convidar** (`/_convite`), gera um link e manda para a pessoa. Cada link cria uma conta, vale uma vez só e vence em 7 dias.

O serviço escuta só em `127.0.0.1:8080`. Ponha o proxy da VPS na frente para ter HTTPS, por exemplo no Caddy:

```
pad.seudominio.com {
    reverse_proxy 127.0.0.1:8080
}
```

## Administrar

```sh
docker compose exec pad /pad <comando>
```

| comando | o que faz |
|---|---|
| `useradd NOME` / `passwd NOME` | cria usuário / troca a senha (e derruba as sessões dele) |
| `userdel NOME` | apaga usuário; falha se ele ainda for dono de documentos |
| `admin NOME` | torna o usuário administrador (gera convites) |
| `groupadd GRUPO` / `groupdel GRUPO` | cria / apaga grupo |
| `join GRUPO NOME` / `leave GRUPO NOME` | põe / tira usuário do grupo |
| `list` | usuários e seus grupos |

## Backup

Na vpsarm os dados ficam em `/srv/pad` (fora dos volumes do Docker, para nenhuma limpeza alcançá-los). Pare o serviço antes, para o SQLite não ser copiado no meio de uma escrita:

```sh
docker compose -f compose.yaml -f compose.traefik.yaml stop
sudo tar czf pad-backup.tgz -C /srv/pad .
docker compose -f compose.yaml -f compose.traefik.yaml start
```

## Desenvolver

```sh
go test ./...
node --test static/*.test.js
PAD_DATA=./data go run .
```
