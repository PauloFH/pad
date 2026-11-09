#!/bin/sh
# Roda na VPS, em ~/pad, chamado pelo CI: compila, faz backup com o serviço parado e sobe a versão nova.
set -eu

DADOS=${PAD_DADOS:-/srv/pad}
BACKUPS=${PAD_BACKUPS:-$HOME/pad-backups}
MANTER=7

compose() { docker compose -f compose.yaml -f compose.traefik.yaml "$@"; }

umask 077 # o backup tem os documentos privados e os hashes de senha
mkdir -p "$BACKUPS"

# um deploy que perdeu o ssh segue rodando aqui: outro junto copiaria o banco em uso
exec 9>"$BACKUPS/.deploy.lock"
flock -n 9 || {
	echo "outro deploy em curso" >&2
	exit 1
}
rm -f "$BACKUPS"/pad-*.parcial # sobra de um deploy morto à força, com dados privados

# Antes de parar o serviço: se algo aqui falhar, a versão atual segue no ar.
# --pull: sem ele o build reaproveita a imagem do Go em cache, com as falhas já corrigidas nas versões novas.
compose build --pull
docker pull alpine:3

arquivo="$BACKUPS/pad-$(date +%Y%m%d-%H%M%S).tgz"
parcial="$arquivo.parcial" # fora do padrão da retenção: um arquivo pela metade nunca conta como backup

# Da parada até o fim, qualquer saída tenta devolver o serviço. Sem backup pronto, só a versão
# atual pode voltar: o build gerou a imagem nova, mas o contêiner parado ainda é o antigo.
volta=atual
devolver() {
	status=$?
	set +e
	trap '' HUP INT TERM # um segundo sinal não pode interromper a recuperação
	rm -f "$parcial"
	if [ "$volta" = nova ]; then
		compose up -d || compose start || echo "SERVIÇO FORA DO AR: nem a versão nova nem a atual subiram" >&2
	else
		compose start || echo "SERVIÇO FORA DO AR: a versão atual não voltou" >&2
	fi
	exit "$status"
}
trap 'exit 1' HUP INT TERM
trap devolver EXIT

# parado para o SQLite não ser copiado no meio de uma escrita; fora o /publico, que é descartável e chega a 1 GB
compose stop
docker run --rm --network none -v "$DADOS:/data:ro" alpine:3 tar czf - -C /data --exclude=./docs/publico --exclude=./docs/publico.txt . >"$parcial"
# um caminho errado monta uma pasta vazia e o tar sai com 0: sem o banco no arquivo, não é backup
gzip -t "$parcial"
tar tzf "$parcial" | grep -q '^\./pad\.db$' || {
	echo "backup sem o banco de dados: confira $DADOS" >&2
	exit 1
}
mv "$parcial" "$arquivo"

volta=nova
compose up -d
trap - EXIT HUP INT TERM

# o serviço novo já está no ar: falhar aqui não pode marcar o deploy como falho
ls -1t "$BACKUPS"/pad-*.tgz | tail -n +$((MANTER + 1)) | xargs -r rm -f || echo "aviso: a limpeza dos backups antigos falhou" >&2
