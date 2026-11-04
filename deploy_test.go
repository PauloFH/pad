package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// deploy.sh com um docker falso: o que importa é a ordem e nunca deixar o serviço parado.
func TestDeployScript(t *testing.T) {
	const falso = `#!/bin/sh
echo "$*" >>"$LOG"
case "$*" in
*"build --pull"*) [ -z "${FALHA_BUILD:-}" ] || exit 1 ;;
*" stop") [ -z "${FALHA_STOP:-}" ] || exit 1 ;;
*" up -d") [ -z "${FALHA_UP:-}" ] || exit 1 ;;
*"run --rm"*)
	[ -z "${FALHA_BACKUP:-}" ] || exit 1
	[ -z "${SINAL:-}" ] || { kill -TERM "$PPID"; exit 0; }
	tar czf - -C "$DADOS_FALSOS" .
	;;
esac
`
	rodar := func(t *testing.T, env ...string) (log, backups string, err error) {
		t.Helper()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(falso), 0o755); err != nil {
			t.Fatal(err)
		}
		backups = filepath.Join(dir, "backups")
		dados, vazio := filepath.Join(dir, "dados"), filepath.Join(dir, "vazio")
		for _, d := range []string{backups, dados, vazio} {
			if err := os.Mkdir(d, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(dados, "pad.db"), []byte("banco"), 0o600); err != nil {
			t.Fatal(err)
		}
		for i := range 8 {
			p := filepath.Join(backups, fmt.Sprintf("pad-antigo-%d.tgz", i))
			if err := os.WriteFile(p, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			at := time.Now().Add(-time.Duration(i+1) * time.Hour) // o 0 é o mais novo dos antigos
			os.Chtimes(p, at, at)
		}
		if err := os.WriteFile(filepath.Join(backups, "pad-morto.tgz.parcial"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		for _, e := range env {
			if e == "TRAVADO=1" { // outro deploy segura a trava até o fim do teste
				f, err := os.Create(filepath.Join(backups, ".deploy.lock"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { f.Close() })
				if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
					t.Fatal(err)
				}
			}
		}
		logPath := filepath.Join(dir, "log")
		cmd := exec.Command("sh", "deploy.sh")
		cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "LOG="+logPath, "PAD_BACKUPS="+backups, "DADOS_FALSOS="+dados)
		for _, e := range env {
			if e == "BACKUP_VAZIO=1" {
				e = "DADOS_FALSOS=" + vazio
			}
			cmd.Env = append(cmd.Env, e)
		}
		err = cmd.Run()
		b, _ := os.ReadFile(logPath)
		return string(b), backups, err
	}
	ordem := func(t *testing.T, log string, passos ...string) {
		t.Helper()
		resto := log
		for _, p := range passos {
			i := strings.Index(resto, p)
			if i < 0 {
				t.Fatalf("faltou %q depois do passo anterior em:\n%s", p, log)
			}
			resto = resto[i+len(p):]
		}
	}
	// nada pela metade nem backup novo: só os 8 antigos ficam
	soAntigos := func(t *testing.T, backups string) {
		t.Helper()
		ms, _ := filepath.Glob(filepath.Join(backups, "pad-*"))
		if len(ms) != 8 {
			t.Fatalf("mexeu nos backups: %d, %v", len(ms), ms)
		}
	}

	t.Run("caminho normal", func(t *testing.T) {
		log, backups, err := rodar(t)
		if err != nil {
			t.Fatal(err, log)
		}
		ordem(t, log, "build --pull", "pull alpine:3", "stop", "run --rm --network none", "up -d")
		if strings.Contains(log, " start") {
			t.Fatalf("acionou a recuperação sem precisar:\n%s", log)
		}
		ms, _ := filepath.Glob(filepath.Join(backups, "pad-*.tgz"))
		if len(ms) != 7 {
			t.Fatalf("%d backups, queria 7: %v", len(ms), ms)
		}
		if _, err := os.Stat(filepath.Join(backups, "pad-antigo-7.tgz")); !os.IsNotExist(err) {
			t.Error("o mais antigo devia ter saído")
		}
		if _, err := os.Stat(filepath.Join(backups, "pad-antigo-0.tgz")); err != nil {
			t.Error("um backup recente saiu")
		}
	})

	t.Run("outro deploy em curso: não toca em nada", func(t *testing.T) {
		log, _, err := rodar(t, "TRAVADO=1")
		if err == nil {
			t.Fatal("o deploy devia recusar")
		}
		if log != "" {
			t.Fatalf("chamou o docker:\n%s", log)
		}
	})

	t.Run("build falha: nem para o serviço", func(t *testing.T) {
		log, _, err := rodar(t, "FALHA_BUILD=1")
		if err == nil {
			t.Fatal("o deploy devia falhar")
		}
		if strings.Contains(log, "stop") || strings.Contains(log, "up -d") || strings.Contains(log, " start") {
			t.Fatalf("mexeu no serviço:\n%s", log)
		}
	})

	// em todos estes, o serviço já pode estar parado: a versão atual tem de voltar, e a nova não sobe sem backup
	for nome, env := range map[string]string{
		"stop falha":             "FALHA_STOP=1",
		"backup falha":           "FALHA_BACKUP=1",
		"backup sem o banco":     "BACKUP_VAZIO=1",
		"sinal durante o backup": "SINAL=1",
	} {
		t.Run(nome+": a versão atual volta", func(t *testing.T) {
			log, backups, err := rodar(t, env)
			if err == nil {
				t.Fatal("o deploy devia falhar")
			}
			ordem(t, log, "stop", " start")
			if strings.Contains(log, "up -d") {
				t.Fatalf("subiu a versão nova sem backup:\n%s", log)
			}
			soAntigos(t, backups)
		})
	}

	t.Run("up falha: tenta de novo e cai para a versão atual", func(t *testing.T) {
		log, backups, err := rodar(t, "FALHA_UP=1")
		if err == nil {
			t.Fatal("o deploy devia falhar")
		}
		ordem(t, log, "stop", "run --rm", "up -d", "up -d", " start")
		ms, _ := filepath.Glob(filepath.Join(backups, "pad-*"))
		if len(ms) != 9 { // o backup desta tentativa fica, e a limpeza não rodou
			t.Fatalf("%d arquivos de backup, queria 9: %v", len(ms), ms)
		}
	})
}

// o comando forçado da chave de deploy: só as ações conhecidas, com o código vindo do repositório.
func TestDeploySSH(t *testing.T) {
	const ponta = "aaaabbbbccccdddd"
	falsos := map[string]string{
		"git": `#!/bin/sh
echo "git $*" >>"$LOG"
case "$*" in
*fetch*) [ -z "${FALHA_FETCH:-}" ] || exit 1 ;;
*rev-parse*) echo aaaabbbbccccdddd ;;
*archive*) tar cf - -C "$FONTE" . ;;
esac
`,
		"curl": `#!/bin/sh
n=$(cat "$HOME/curl-n" 2>/dev/null || echo 0)
echo $((n + 1)) >"$HOME/curl-n"
[ "$n" -gt 0 ] || [ -z "${CURL_ERRO_1:-}" ] || exit 7
echo 200
`,
	}
	rodar := func(t *testing.T, comando string, env ...string) (saida, log string, err error) {
		t.Helper()
		dir := t.TempDir()
		fonte := filepath.Join(dir, "fonte")
		if err := os.Mkdir(fonte, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(fonte, "deploy.sh"), []byte("echo rodou-deploy; echo \"$PWD\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		for nome, corpo := range falsos {
			if err := os.WriteFile(filepath.Join(dir, nome), []byte(corpo), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		logPath := filepath.Join(dir, "log")
		cmd := exec.Command("sh", "deploy-ssh.sh")
		cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "HOME="+dir, "LOG="+logPath, "FONTE="+fonte, "SSH_ORIGINAL_COMMAND="+comando)
		cmd.Env = append(cmd.Env, env...)
		b, err := cmd.CombinedOutput()
		l, _ := os.ReadFile(logPath)
		return string(b), string(l), err
	}
	for _, c := range []string{"", "id", "deploy", "deploy; id", "conferir && id", "conferir x", "sh -c deploy"} {
		saida, log, err := rodar(t, c)
		if err == nil || log != "" || !strings.Contains(saida, "não permitido") {
			t.Errorf("comando %q: err %v, saída %q, log %q", c, err, saida, log)
		}
	}
	// o argumento só é comparado com a ponta da main: nada diferente sobe, nem como injeção
	for _, c := range []string{"deploy outro", "deploy " + ponta + "; id", "deploy $(id)"} {
		saida, log, err := rodar(t, c)
		if err == nil || strings.Contains(saida, "rodou-deploy") || strings.Contains(log, "archive") {
			t.Errorf("comando %q: err %v, saída %q, log %q", c, err, saida, log)
		}
	}
	saida, log, err := rodar(t, "deploy "+ponta)
	if err != nil || !strings.Contains(saida, "rodou-deploy") || !strings.Contains(saida, "/pad") {
		t.Fatalf("deploy: %v, %q", err, saida)
	}
	for _, p := range []string{"init", "fetch", "archive " + ponta} {
		if !strings.Contains(log, p) {
			t.Errorf("faltou git %s em:\n%s", p, log)
		}
	}
	if saida, _, err := rodar(t, "deploy "+ponta, "FALHA_FETCH=1"); err == nil || strings.Contains(saida, "rodou-deploy") {
		t.Errorf("deploy rodou com o fetch falhando: %v, %q", err, saida)
	}
	if saida, _, err := rodar(t, "conferir"); err != nil {
		t.Fatalf("conferir: %v, %q", err, saida)
	}
	if saida, _, err := rodar(t, "conferir", "CURL_ERRO_1=1"); err != nil {
		t.Fatalf("conferir desistiu no primeiro erro do curl: %v, %q", err, saida)
	}
}

// o instalador de ponta a ponta: ssh e gh falsos, ssh-keygen e authorized_keys de verdade.
func TestInstalarChaveDeploy(t *testing.T) {
	const falsoSSH = `#!/bin/sh
echo "ssh $*" >>"$LOG"
case "$1" in
-F) # a chave do CI, pelo endereço público
	for ultimo; do :; done
	if [ "$ultimo" = conferir ]; then [ -z "${CONFERIR_FALHA:-}" ]; exit; fi
	[ -z "${SEM_RESTRICAO:-}" ] || exit 0
	echo "comando não permitido" >&2
	exit 2
	;;
-G) echo "identityfile ~/nada"; exit 0 ;;
esac
shift
case "$*" in
*ssh_host_*) [ -z "${SEM_CHAVES_HOST:-}" ] || exit 0; echo "ssh-ed25519 CHAVEDOHOST root@vps"; exit 0 ;;
esac
exec sh -c "$*"
`
	const falsoGH = "#!/bin/sh\ncat >/dev/null\necho \"gh $*\" >>\"$LOG\"\n"
	const falsoGit = "#!/bin/sh\nexit 0\n"

	rodar := func(t *testing.T, semQuebraNoFim bool, env ...string) (saida, chaves, log string, err error) {
		t.Helper()
		dir := t.TempDir()
		for nome, corpo := range map[string]string{"ssh": falsoSSH, "gh": falsoGH, "git": falsoGit} {
			if err := os.WriteFile(filepath.Join(dir, nome), []byte(corpo), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		home := filepath.Join(dir, "home")
		if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", filepath.Join(dir, "minha")).CombinedOutput(); err != nil {
			t.Fatal(err, string(out))
		}
		pub, err := os.ReadFile(filepath.Join(dir, "minha.pub"))
		if err != nil {
			t.Fatal(err)
		}
		// sem comentário: é o caso em que emendar na linha inutilizaria a chave
		campos := strings.Fields(string(pub))
		minha := campos[0] + " " + campos[1]
		conteudo := minha + "\n"
		if semQuebraNoFim {
			conteudo = minha
		}
		ak := filepath.Join(home, ".ssh", "authorized_keys")
		if err := os.WriteFile(ak, []byte(conteudo), 0o600); err != nil {
			t.Fatal(err)
		}
		logPath := filepath.Join(dir, "log")
		cmd := exec.Command("sh", "instalar-chave-deploy.sh", "vps", "exemplo.invalid")
		cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "HOME="+home, "LOG="+logPath)
		cmd.Env = append(cmd.Env, env...)
		b, err := cmd.CombinedOutput()
		k, _ := os.ReadFile(ak)
		l, _ := os.ReadFile(logPath)
		if !strings.HasPrefix(string(k), minha+"\n") && !(string(k) == minha) {
			t.Fatalf("a chave existente mudou:\n%s", k)
		}
		return string(b), string(k), string(l), err
	}

	for _, semQuebra := range []bool{false, true} {
		t.Run(fmt.Sprintf("caminho normal, sem quebra no fim: %v", semQuebra), func(t *testing.T) {
			saida, chaves, log, err := rodar(t, semQuebra)
			if err != nil {
				t.Fatal(err, saida)
			}
			linhas := strings.Split(strings.TrimSpace(chaves), "\n")
			if len(linhas) != 2 || !strings.HasPrefix(linhas[1], `restrict,command="`) || !strings.Contains(linhas[1], `/pad-deploy-ssh.sh" ssh-ed25519 `) {
				t.Fatalf("authorized_keys:\n%s", chaves)
			}
			for _, s := range []string{"VPS_SSH_KEY", "VPS_KNOWN_HOSTS", "VPS_HOST", "VPS_USER"} {
				if !strings.Contains(log, "gh secret set "+s+" -R PauloFH/pad -e production") {
					t.Errorf("faltou gravar %s:\n%s", s, log)
				}
			}
			if !strings.Contains(log, "@exemplo.invalid") {
				t.Errorf("não testou pelo endereço público:\n%s", log)
			}
		})
	}
	// sem provar a chave, nenhum segredo é gravado
	for nome, env := range map[string]string{
		"chave nova não conecta": "CONFERIR_FALHA=1",
		"chave sem restrição":    "SEM_RESTRICAO=1",
		"sem chaves de host":     "SEM_CHAVES_HOST=1",
	} {
		t.Run(nome, func(t *testing.T) {
			saida, _, log, err := rodar(t, false, env)
			if err == nil {
				t.Fatal("devia falhar:", saida)
			}
			if strings.Contains(log, "gh secret") {
				t.Fatalf("gravou segredo sem provar a chave:\n%s", log)
			}
		})
	}
}
