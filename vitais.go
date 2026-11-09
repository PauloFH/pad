package main

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"
)

// vitais recebe as Web Vitals que o navegador mediu (vitais.js) e as grava no log pelo tipo da página.
// Sem login, como as páginas: o limite por IP e a proteção de origem seguram quem quisesse encher o log.
func (a *app) vitais(w http.ResponseWriter, r *http.Request) {
	if !a.limVitais.permite(clientIP(r), time.Now()) {
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	var v struct {
		Pagina                   string
		LCP, INP, CLS, FCP, TTFB float64
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&v); err != nil {
		http.Error(w, "corpo inválido", http.StatusBadRequest)
		return
	}
	lim := func(x float64) float64 { return max(0, min(x, 1e6)) } // um 1e308 viraria 300 dígitos no log
	log.Printf("vitais: %s lcp=%.0fms inp=%.0fms cls=%.3f fcp=%.0fms ttfb=%.0fms", tipoDePagina(v.Pagina),
		lim(v.LCP), lim(v.INP), lim(v.CLS), lim(v.FCP), lim(v.TTFB))
	w.WriteHeader(http.StatusNoContent)
}

// tipoDePagina reduz o caminho a um rótulo fixo: o log não guarda nome de documento nem token, e o texto que vem
// de fora nunca chega a ele.
func tipoDePagina(p string) string {
	seg := strings.Split(strings.TrimPrefix(p, "/"), "/")
	switch {
	case p == "/":
		return "/"
	case seg[0] == "_login", seg[0] == "_convite":
		return "/" + seg[0]
	case seg[0] == "_join":
		return "/_join/{token}"
	case seg[0] == "publico":
		return "/publico/{doc}"
	case !validUser.MatchString(seg[0]) || len(seg) < 2:
		return "outra"
	case seg[1] == "_mcp" && len(seg) > 2:
		return "/{usuario}/_mcp/c/{nome}"
	case seg[1] == "_mcp":
		return "/{usuario}/_mcp"
	}
	return "/{usuario}/{doc}"
}
