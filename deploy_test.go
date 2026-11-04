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
