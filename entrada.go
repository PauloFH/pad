package main

import (
	"fmt"
	"strings"
	"time"
)

var meses = [...]string{"jan", "fev", "mar", "abr", "mai", "jun", "jul", "ago", "set", "out", "nov", "dez"}

// quando descreve t em relação a agora, no fuso local, para a lista de recentes.
func quando(t, agora time.Time) string {
	t, agora = t.In(time.Local), agora.In(time.Local)
	d := agora.Sub(t)
	switch {
	case d < time.Minute:
		return "agora"
	case d < time.Hour:
		return fmt.Sprintf("há %d min", int(d/time.Minute))
	}
	dia := func(x time.Time) time.Time { return time.Date(x.Year(), x.Month(), x.Day(), 0, 0, 0, 0, time.Local) }
	switch {
	case dia(t).Equal(dia(agora)):
		return fmt.Sprintf("há %d h", int(d/time.Hour))
	case dia(t).Equal(dia(agora).AddDate(0, 0, -1)):
		return "ontem"
	case t.Year() == agora.Year():
		return fmt.Sprintf("%d %s", t.Day(), meses[t.Month()-1])
	}
	return fmt.Sprintf("%d %s %d", t.Day(), meses[t.Month()-1], t.Year())
}

// maiuscula põe a inicial de um login em maiúscula, só para exibir.
func maiuscula(s string) string { return strings.ToUpper(s[:1]) + s[1:] }

// quemEsta resume quem edita um documento.
func quemEsta(nomes []string) string {
	switch len(nomes) {
	case 0:
		return ""
	case 1:
		return maiuscula(nomes[0]) + " está aqui"
	case 2:
		return maiuscula(nomes[0]) + " e " + maiuscula(nomes[1]) + " estão aqui"
	}
	return fmt.Sprintf("%d pessoas aqui", len(nomes))
}
