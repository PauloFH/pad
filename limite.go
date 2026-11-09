package main

import (
	"sync"
	"time"
)

// limite conta eventos por chave (em geral o IP) numa janela fixa.
// shortcut: a janela é uma só para todas as chaves e zera tudo junto, o que permite o dobro na virada; serve para frear abuso, não para cobrar cota exata.
type limite struct {
	max    int
	janela time.Duration
	mu     sync.Mutex
	inicio time.Time
	cont   map[string]int
}

func novoLimite(max int, janela time.Duration) *limite {
	return &limite{max: max, janela: janela, cont: map[string]int{}}
}

// zerar (com mu) começa uma janela nova quando a anterior acabou; é o que impede o mapa de crescer para sempre.
func (l *limite) zerar(now time.Time) {
	if now.Sub(l.inicio) >= l.janela {
		l.inicio = now
		clear(l.cont)
	}
}

// cheio diz se a chave já gastou a janela, sem contar nada.
func (l *limite) cheio(chave string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.zerar(now)
	return l.cont[chave] >= l.max
}

func (l *limite) somar(chave string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.zerar(now)
	l.cont[chave]++
}

// permite conta o evento e diz se ele cabe na janela.
func (l *limite) permite(chave string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.zerar(now)
	if l.cont[chave] >= l.max {
		return false
	}
	l.cont[chave]++
	return true
}

// ritmo freia uma conexão: gasta bytes e devolve quanto ela deve esperar antes de ler de novo.
// Um envio grande de uma vez passa (colar um texto), e o custo por mensagem freia as rajadas de mensagens pequenas.
type ritmo struct {
	porSeg, folga int // bytes por segundo e rajada sem espera
	divida        int
	ultimo        time.Time
}

const (
	custoMensagem = 256
	// abaixo do prazo do pingLoop: a leitura parada não processa o pong
	ritmoEsperaMax = 3 * time.Second
)

func (r *ritmo) gastar(n int, now time.Time) time.Duration {
	r.divida = max(0, r.divida-int(now.Sub(r.ultimo).Seconds()*float64(r.porSeg))) + n + custoMensagem
	r.ultimo = now
	if r.divida <= r.folga {
		return 0
	}
	return min(time.Duration(float64(r.divida-r.folga)/float64(r.porSeg)*float64(time.Second)), ritmoEsperaMax)
}
