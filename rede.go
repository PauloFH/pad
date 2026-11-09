package main

import (
	"compress/gzip"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// os compressores do flate são grandes: reaproveitá-los poupa uma alocação por resposta.
// Nível 5: no texto real comprime quase como o 6 (padrão) e custa até metade da CPU em texto repetitivo.
var gzips = sync.Pool{New: func() any { w, _ := gzip.NewWriterLevel(nil, 5); return w }}

var limiteLenta = 300 * time.Millisecond

// lentas registra no log as requisições acima de limiteLenta pelo padrão da rota ("GET /{name...}"): sem nome de
// documento nem token no log. O WebSocket e as esperas longas (o chat com ?esperar=1 e as chamadas MCP, entre elas
// o esperar de até 50 s) demoram de propósito e ficam de fora.
// shortcut: as chamadas MCP saem todas, inclusive as rápidas que ficarem lentas; separar pelo nome da ferramenta se precisar
func lentas(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ini := time.Now()
		h.ServeHTTP(w, r)
		d := time.Since(ini)
		if d < limiteLenta || r.Header.Get("Upgrade") != "" || r.URL.Query().Get("esperar") == "1" || r.Pattern == "POST /{usuario}/_mcp" {
			return
		}
		rota := r.Pattern // o ServeMux grava no próprio request
		if rota == "" {
			rota = r.Method + " (sem rota)"
		}
		log.Printf("lenta: %s %v", rota, d.Round(time.Millisecond))
	})
}

// comprime manda HTML, CSS, JS e JSON em gzip a quem aceita: o texto encolhe ~3x, e a VPS fica a ~140 ms de RTT.
// O WebSocket passa direto (precisa do Hijack) e o HEAD também (não tem corpo).
func comprime(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "" || r.Method == http.MethodHead {
			h.ServeHTTP(w, r)
			return
		}
		// shortcut: não lê o q= do Accept-Encoding ("gzip;q=0" ainda comprime); nenhum navegador manda isso
		g := &gzipWriter{ResponseWriter: w, aceita: strings.Contains(r.Header.Get("Accept-Encoding"), "gzip")}
		defer g.fechar()
		h.ServeHTTP(g, r)
	})
}

type gzipWriter struct {
	http.ResponseWriter
	aceita, decidido bool
	gz               *gzip.Writer
}

// WriteHeader decide uma vez só, quando o tipo e o status já são conhecidos: só o 200 de texto é comprimido
// (o 206 do Range e o 304 não podem mudar de corpo).
func (g *gzipWriter) WriteHeader(code int) {
	if !g.decidido {
		g.decidido = true
		h := g.Header()
		ct := h.Get("Content-Type")
		if code == http.StatusOK && h.Get("Content-Encoding") == "" &&
			(strings.HasPrefix(ct, "text/") || strings.HasPrefix(ct, "application/json")) {
			h.Add("Vary", "Accept-Encoding")
			if g.aceita {
				h.Set("Content-Encoding", "gzip")
				h.Del("Content-Length")
				h.Del("Accept-Ranges") // o Range do FileServer conta bytes do arquivo sem compressão
				// shortcut: num corpo acima do http.MaxBytesReader, o servidor não fica sabendo pelo writer embrulhado
				// (a interface do net/http tem método não exportado) e descarta até 256 KB em vez de fechar a conexão
				// o corpo comprimido é outra representação: ETag fraco, que o If-None-Match ainda casa
				if e := h.Get("ETag"); strings.HasPrefix(e, `"`) {
					h.Set("ETag", "W/"+e)
				}
				g.gz = gzips.Get().(*gzip.Writer)
				g.gz.Reset(g.ResponseWriter)
			}
		}
		// o 304 já vem sem Content-Type: leva o mesmo Vary e o mesmo ETag fraco que o 200 comprimido levaria
		if code == http.StatusNotModified {
			h.Add("Vary", "Accept-Encoding")
			if e := h.Get("ETag"); g.aceita && strings.HasPrefix(e, `"`) {
				h.Set("ETag", "W/"+e)
			}
		}
	}
	g.ResponseWriter.WriteHeader(code)
}

func (g *gzipWriter) Write(b []byte) (int, error) {
	if !g.decidido {
		g.WriteHeader(http.StatusOK)
	}
	if g.gz != nil {
		return g.gz.Write(b)
	}
	return g.ResponseWriter.Write(b)
}

// Unwrap deixa o http.NewResponseController achar o ResponseWriter de verdade (prazos de leitura do login e do save).
func (g *gzipWriter) Unwrap() http.ResponseWriter { return g.ResponseWriter }

func (g *gzipWriter) fechar() {
	if g.gz != nil {
		g.gz.Close()
		gzips.Put(g.gz)
	}
}
