#!/bin/sh
# Cria (ou troca) a chave de deploy restrita. Rode na sua máquina, na raiz do repositório:
#   sh instalar-chave-deploy.sh <alias-ssh-da-vps> <endereço público da VPS>
# Gera uma chave nova, instala o comando forçado, ACRESCENTA a chave ao authorized_keys (com cópia antes e
# conferência), prova que ela funciona e é restrita, e só então grava os segredos no Environment production.
# Não remove chave nenhuma. A chave privada só existe na pasta temporária e no GitHub.
set -eu

ALVO=${1:?uso: sh instalar-chave-deploy.sh <alias-ssh-da-vps> <endereço público da VPS>}
HOST=${2:?falta o endereço público da VPS, o mesmo que o GitHub vai usar para conectar}
REPO=PauloFH/pad
[ -f deploy-ssh.sh ] || {
	echo "rode na raiz do repositório do pad" >&2
	exit 1
}

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
umask 077

echo "== 1/5 conferindo a VPS (nada é alterado aqui)"
ssh "$ALVO" 'for c in git curl flock ssh-keygen tail; do command -v "$c" >/dev/null || { echo "falta $c na VPS" >&2; exit 1; }; done
	[ -f "$HOME/.ssh/authorized_keys" ] || { echo "sem authorized_keys na VPS" >&2; exit 1; }
	git ls-remote -q https://github.com/PauloFH/pad.git HEAD >/dev/null || { echo "a VPS não alcança o GitHub" >&2; exit 1; }'
USUARIO=$(ssh "$ALVO" 'id -un')
ssh "$ALVO" 'cat /etc/ssh/ssh_host_*_key.pub' >"$TMP/hostkeys"
awk -v h="$HOST" '{print h, $1, $2}' "$TMP/hostkeys" >"$TMP/known_hosts"
[ -s "$TMP/known_hosts" ] || {
	echo "não consegui ler as chaves de host da VPS" >&2
	exit 1
}

echo "== 2/5 gerando a chave"
ssh-keygen -q -t ed25519 -N '' -C pad-ci-deploy-restrita -f "$TMP/chave"
PUB=$(cat "$TMP/chave.pub")

echo "== 3/5 instalando na VPS"
ssh "$ALVO" 'cat >"$HOME/pad-deploy-ssh.sh" && chmod 755 "$HOME/pad-deploy-ssh.sh"' <deploy-ssh.sh
# a conferência roda na própria VPS: se a conta não bater, a cópia volta antes de a sessão acabar
ssh "$ALVO" "PUB='$PUB' sh -s" <<'REMOTO'
set -eu
f=$HOME/.ssh/authorized_keys
copia=$f.antes-deploy-restrito-$(date +%Y%m%d-%H%M%S)
cp -p "$f" "$copia"
antes=$(ssh-keygen -lf "$f" | wc -l)
# sem quebra de linha no fim, o >> emendaria na última chave e a inutilizaria
[ -z "$(tail -c1 "$f")" ] || echo >>"$f"
printf 'restrict,command="%s/pad-deploy-ssh.sh" %s\n' "$HOME" "$PUB" >>"$f"
depois=$(ssh-keygen -lf "$f" | wc -l)
if [ "$depois" != "$((antes + 1))" ]; then
	cp -p "$copia" "$f"
	echo "authorized_keys não ficou como esperado ($antes -> $depois chaves): a cópia foi restaurada" >&2
	exit 1
fi
echo "cópia em $copia"
REMOTO

echo "== 4/5 provando a chave nova, pelo endereço público"
SSH_CI="ssh -F none -i $TMP/chave -o IdentitiesOnly=yes -o IdentityAgent=none -o BatchMode=yes -o UserKnownHostsFile=$TMP/known_hosts -o StrictHostKeyChecking=yes $USUARIO@$HOST"
$SSH_CI conferir || {
	echo "a chave nova não passou no conferir: nenhum segredo foi gravado" >&2
	exit 1
}
rc=0
saida=$($SSH_CI id 2>&1) || rc=$?
if [ "$rc" != 2 ] || ! printf '%s' "$saida" | grep -q 'comando não permitido'; then
	echo "a restrição da chave não se confirmou: nenhum segredo foi gravado (saída $rc: $saida)" >&2
	exit 1
fi
echo "ok: 'conferir' funciona e um comando qualquer é recusado"

echo "== 5/5 gravando os segredos no Environment production"
gh secret set VPS_SSH_KEY -R "$REPO" -e production <"$TMP/chave"
gh secret set VPS_KNOWN_HOSTS -R "$REPO" -e production <"$TMP/known_hosts"
printf '%s' "$HOST" | gh secret set VPS_HOST -R "$REPO" -e production
printf '%s' "$USUARIO" | gh secret set VPS_USER -R "$REPO" -e production

echo
echo "Pronto. Impressões digitais no authorized_keys, para identificar a chave antiga do CI:"
ssh "$ALVO" 'ssh-keygen -lf "$HOME/.ssh/authorized_keys"'
echo "Esta é a sua chave local para este alias; não a remova:"
ssh -G "$ALVO" | awk '$1=="identityfile"{print $2}' | while read -r k; do
	k=$(printf '%s' "$k" | sed "s|^~|$HOME|")
	if [ -f "$k.pub" ]; then ssh-keygen -lf "$k.pub"; fi
done
