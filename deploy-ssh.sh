#!/bin/sh
# Comando forçado da chave de deploy na VPS (restrict,command="..." no authorized_keys).
# Instalado em ~/pad-deploy-ssh.sh, fora de ~/pad: um push não troca este arquivo.
# A chave só escolhe entre as ações abaixo, e o código vem sempre da main no GitHub:
# quem roubar a chave pode repetir um deploy, mas não mandar código para a VPS.
set -eu

REPO=$HOME/pad-repo
ORIGEM=https://github.com/PauloFH/pad.git

case "${SSH_ORIGINAL_COMMAND:-}" in
"deploy "*)
	sha=${SSH_ORIGINAL_COMMAND#deploy }
	# antes de mexer em ~/pad; arquivo próprio, porque o deploy.sh tem a trava dele
	exec 8>"$HOME/.pad-deploy-ssh.lock"
	flock -n 8 || {
		echo "outro deploy em curso" >&2
		exit 1
	}
	# init repetido é inofensivo e recupera um repositório interrompido no meio
	git init -q --bare "$REPO"
	git -C "$REPO" fetch -q "$ORIGEM" +refs/heads/main:refs/heads/main
	# só a ponta da main: o commit aprovado é o que sobe, e o argumento nunca escolhe outro código
	if [ "$sha" != "$(git -C "$REPO" rev-parse refs/heads/main)" ]; then
		echo "a main andou: aprove o deploy mais novo" >&2
		exit 3
	fi
	mkdir -p "$HOME/pad"
	git -C "$REPO" archive "$sha" | tar x -C "$HOME/pad"
	cd "$HOME/pad"
	# em arquivo: o deploy continua mesmo se o ssh cair
	rc=0
	sh deploy.sh >"$HOME/pad-deploy.log" 2>&1 || rc=$?
	tail -n 40 "$HOME/pad-deploy.log"
	exit "$rc"
	;;
conferir)
	for _ in 1 2 3 4 5 6 7 8 9 10; do
		code=$(curl -sk -o /dev/null -w '%{http_code}' --resolve pad.pauloholanda.com.br:443:127.0.0.1 https://pad.pauloholanda.com.br/_login) || code=000
		[ "$code" = 200 ] && exit 0
		sleep 3
	done
	echo "status $code"
	docker logs --tail 20 pad-pad-1
	exit 1
	;;
*)
	echo "comando não permitido" >&2
	exit 2
	;;
esac
