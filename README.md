# pad

Bloco de notas estilo dontpad, com login. `/qualquer/nome` abre o documento; quem cria vira dono e pode compartilhar com usuários ou grupos (quem recebe pode ler e editar). O dono também pode tornar um documento público: aí o endereço abre sem login, só para leitura. Salva sozinho enquanto você digita.

- Acessos, grupos e sessões: SQLite em `/data/pad.db`
- Texto dos documentos: `/data/docs/<nome>.txt`

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

Pare o serviço antes, para o SQLite não ser copiado no meio de uma escrita:

```sh
docker compose stop
docker run --rm -v pad_pad-data:/data -v "$PWD":/out alpine tar czf /out/pad-backup.tgz -C /data .
docker compose start
```

## Desenvolver

```sh
go test ./...
PAD_DATA=./data go run .
```
