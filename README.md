# pad

Bloco de notas estilo dontpad, com login. Cada usuário tem seu espaço: `/paulo/notas`, `/paulo/pessoal/cpf` (até 5 níveis de subpasta, cada uma é um documento à parte, com acesso próprio). Fora dele, a área `/publico/...` é aberta: sem dono e sem login, qualquer um lê e escreve (até 256 KB por documento, 50 mil itens entre documentos e pastas e 1 GB no total; quando enche, os editados há mais tempo são apagados para abrir espaço; um administrador também apaga qualquer documento da área pela própria página). Quem cria vira dono e pode compartilhar com usuários ou grupos (quem recebe pode ler e editar). O dono também pode tornar um documento público: aí o endereço abre sem login, só para leitura. Salva sozinho enquanto você digita.

Quem abre `/` sem sessão vê a apresentação do pad; com sessão, a lista de documentos.

Oito temas (Grafite, Madrugada, Café, Floresta, Papel, Céu, Areia e Alto contraste) ficam no seletor da barra de baixo; o padrão segue o claro/escuro do sistema e a escolha fica guardada no navegador.

Edição ao vivo: várias pessoas podem abrir o mesmo documento ao mesmo tempo. Quem está na página aparece em bolhas, cada uma com a sua cor, e o cursor e a seleção dos outros aparecem no texto com essa cor. O trecho que alguém selecionou fica travado para os demais e é liberado quando essa pessoa fica 30 s parada. No menu das bolhas dá para seguir alguém e acompanhar o que ele vê. No `/publico` quem tem conta aparece com o nome da conta e quem entra sem login ganha um nome engraçado. O Ctrl+Z desfaz só as suas mudanças. Enquanto há alguém conectado, o `POST` antigo do documento é recusado com 409.

- Acessos, grupos e sessões: SQLite em `/data/pad.db`
- Máquinas, conversas e mensagens: SQLite em `/data/pad.db`
- Texto dos documentos: `/data/docs/<nome>.txt`

## Agentes (MCP)

O pad também é um servidor MCP: agentes Claude Code e Codex conversam entre si por conversas (um agente "pai" cria uma por task, os "filhos" respondem) e leem e editam os seus documentos, fora da área `/publico`. Você acompanha e participa pelo navegador.

Abra **mcp** (`/<usuário>/_mcp`), dê um nome à máquina (por exemplo `notebook`) e copie a chave `pad_...`, que só aparece uma vez. Na máquina, use uma destas configurações:

```sh
# Claude Code
claude mcp add --scope user --transport http pad https://pad.seudominio.com/<usuário>/_mcp --header "Authorization: Bearer <chave>"
```

```toml
# Codex: ~/.codex/config.toml
[mcp_servers.pad]
url = "https://pad.seudominio.com/<usuário>/_mcp"
bearer_token_env_var = "PAD_MCP_CHAVE"
default_tools_approval_mode = "approve"
# e no shell: export PAD_MCP_CHAVE=<chave>
```

O painel mostra esses comandos já preenchidos, uma única vez, junto com a chave. Para cortar o acesso de uma máquina, use **Revogar** na mesma página; `pad passwd` revoga todas as chaves da conta. A linha `default_tools_approval_mode = "approve"` deixa o agente usar as ferramentas do pad sem pedir aprovação; sem ela, o Codex pergunta a cada chamada.

O `export PAD_MCP_CHAVE=<chave>` é obrigatório, no shell em que o `codex` roda: sem ele o Codex não envia a chave.

**Injeção de prompt:** os agentes não acessam `/publico`, nem para ler nem para escrever, porque qualquer anônimo escreve ali. Ainda assim, documentos de outras pessoas, e os seus quando compartilhados, chegam ao agente como texto. O pad marca esse texto como dado, mas quem decide o que fazer com ele é o agente, então não mande um agente com aprovação automática ler documentos em que você não confia.

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
    reverse_proxy 127.0.0.1:8080 {
        header_up X-Real-Ip {remote_host}
    }
}
```

O proxy precisa definir o `X-Real-Ip` com o IP real do cliente, sobrescrevendo o que vier de fora: os limites por IP (login, conexões e gravações em `/publico`) confiam nesse cabeçalho. O Traefik já faz isso por padrão, desde que o entrypoint não confie em cabeçalhos de qualquer origem (`forwardedHeaders.insecure`).

Limites contra abuso: 10 senhas erradas por IP em 5 minutos e 20 por conta em 15 minutos (a conta cheia só barra os IPs que já erraram 3 vezes nela, para ninguém deixar o dono de fora), 60 conexões novas por minuto e 20 simultâneas por IP, 120 gravações por minuto em `/publico` e 30 envios de Web Vitals por minuto. O `compose.yaml` limita o contêiner a 1 GB de memória; ajuste `mem_limit` ao tamanho da sua VPS.

## Desempenho

O HTML, o CSS, o JS e o JSON saem em gzip, e o JS e o CSS vêm com `?v=<hash>` na URL e ficam um ano no cache do navegador (um deploy muda a URL). Dois registros no log (`docker compose logs pad`) mostram como o pad está para quem usa:

- `lenta: GET /{name...} 412ms`: requisição acima de 300 ms, pelo padrão da rota (sem nome de documento nem token). O WebSocket, o chat esperando mensagem e as chamadas MCP ficam de fora, porque demoram de propósito.
- `vitais: /{usuario}/{doc} lcp=812ms inp=40ms cls=0.012 ...`: as Web Vitals medidas no navegador de cada visita, enviadas ao sair da página. O valor `0` quer dizer que o navegador não chegou a medir.

## Administrar

```sh
docker compose exec pad /pad <comando>
```

| comando | o que faz |
|---|---|
| `useradd NOME` / `passwd NOME` | cria usuário / troca a senha (e derruba as sessões e as chaves de máquina dele) |
| `userdel NOME` | apaga usuário; falha se ele ainda for dono de documentos |
| `admin NOME` | torna o usuário administrador (gera convites) |
| `groupadd GRUPO` / `groupdel GRUPO` | cria / apaga grupo |
| `join GRUPO NOME` / `leave GRUPO NOME` | põe / tira usuário do grupo |
| `list` | usuários e seus grupos |

## Deploy

Push na `main` roda o CI e, com ele verde, o job `deploy` fica esperando aprovação no Environment `production` (**Actions** > a execução > **Review deployments**). O ruleset `proteger-main` impede force-push e apagar a `main`.

A chave SSH do CI fica no Environment e é restrita no `authorized_keys` da VPS (`restrict,command=".../pad-deploy-ssh.sh"`): ela só pede `deploy <commit>` ou `conferir` e não executa mais nada. O código vem sempre da `main` no GitHub, e o deploy recusa um commit que não seja a ponta dela: se a `main` andou, aprove a execução mais nova.

O `~/pad-deploy-ssh.sh` da VPS é uma cópia do `deploy-ssh.sh` e não é atualizado pelo deploy, de propósito: ao mudar o arquivo aqui, copie-o de novo à mão. Para criar ou trocar a chave, use o `instalar-chave-deploy.sh`, que não remove a anterior.

## Backup

Na vpsarm os dados ficam em `/srv/pad` (fora dos volumes do Docker, para nenhuma limpeza alcançá-los). Cada deploy (`deploy.sh`) para o serviço, grava `~/pad-backups/pad-<data>.tgz` sem a área `/publico` e guarda os 7 últimos; a saída fica em `~/pad-deploy.log`. Se o backup falhar ou vier sem o banco, a versão atual volta ao ar e o deploy para. Os arquivos têm os documentos privados e os hashes de senha: guarde uma cópia fora da VPS, com o mesmo cuidado.

Para um backup fora do deploy, pare o serviço antes, para o SQLite não ser copiado no meio de uma escrita:

```sh
docker compose -f compose.yaml -f compose.traefik.yaml stop
sudo tar czf pad-backup.tgz -C /srv/pad .
docker compose -f compose.yaml -f compose.traefik.yaml start
```

Para restaurar, com o serviço parado, extraia numa pasta vazia: extrair por cima deixaria documentos criados depois do backup e um `pad.db-wal` que não combina com o banco. O backup não traz o `/publico`; devolva a pasta antiga se quiser mantê-lo.

```sh
sudo mv /srv/pad /srv/pad.antes
sudo mkdir /srv/pad
sudo tar xzf ~/pad-backups/pad-<data>.tgz -C /srv/pad
sudo mv /srv/pad.antes/docs/publico /srv/pad/docs/ # opcional
```

## Desenvolver

```sh
go test ./...
node --test static/*.test.js
PAD_DATA=./data go run .
```
