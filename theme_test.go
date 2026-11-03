package main

import (
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"
)

var temaIDs = []string{"grafite", "madrugada", "cafe", "floresta", "papel", "ceu", "areia", "contraste"}

var blocoTema = regexp.MustCompile(`:root\[data-theme="([a-z]+)"\]\s*\{([^}]*)\}`)

// themeTokens lê os blocos :root[data-theme="id"]{...} do CSS: id → propriedade → valor.
func themeTokens(css string) (map[string]map[string]string, error) {
	temas := map[string]map[string]string{}
	for _, m := range blocoTema.FindAllStringSubmatch(css, -1) {
		props := map[string]string{}
		for _, decl := range strings.Split(m[2], ";") {
			if k, v, ok := strings.Cut(decl, ":"); ok {
				props[strings.TrimSpace(k)] = strings.TrimSpace(v)
			}
		}
		temas[m[1]] = props
	}
	if len(temas) == 0 {
		return nil, errors.New("nenhum bloco de tema no CSS")
	}
	return temas, nil
}

func lerTemas(t *testing.T) map[string]map[string]string {
	t.Helper()
	css, err := os.ReadFile("static/pad.css")
	if err != nil {
		t.Fatal(err)
	}
	temas, err := themeTokens(string(css))
	if err != nil {
		t.Fatal(err)
	}
	return temas
}

func TestTemasCompletos(t *testing.T) {
	temas := lerTemas(t)
	if len(temas) != len(temaIDs) {
		t.Errorf("%d temas no CSS, quero %d", len(temas), len(temaIDs))
	}
	for _, id := range temaIDs {
		props, ok := temas[id]
		if !ok {
			t.Errorf("tema %s ausente", id)
			continue
		}
		for _, tk := range []string{"--bg", "--bar", "--fg", "--mut", "--line", "--acc", "--on", "--field", "--hov", "--err", "--ok"} {
			if props[tk] == "" {
				t.Errorf("tema %s: falta %s", id, tk)
			}
		}
		if cs := props["color-scheme"]; cs != "light" && cs != "dark" {
			t.Errorf("tema %s: color-scheme %q", id, cs)
		}
	}
}

func TestTemasContraste(t *testing.T) {
	regras := []struct {
		fg, bg string
		min    float64
	}{
		{"--fg", "--bg", 4.5}, {"--fg", "--bar", 4.5}, {"--fg", "--field", 4.5},
		{"--mut", "--bg", 4.5}, {"--mut", "--bar", 4.5}, {"--mut", "--field", 4.5},
		{"--fg", "--hov", 4.5}, {"--mut", "--hov", 4.5}, // selo, item de menu em hover
		{"--on", "--acc", 4.5},
		{"--err", "--bg", 4.5}, {"--err", "--field", 4.5},
		{"--acc", "--bg", 3}, {"--ok", "--bar", 3},
	}
	for id, props := range lerTemas(t) {
		for _, r := range regras {
			if c := contraste(t, props[r.fg], props[r.bg]); c < r.min {
				t.Errorf("tema %s: %s sobre %s = %.2f < %v", id, r.fg, r.bg, c, r.min)
			}
		}
	}
}
