package main

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
)

// Documento aberto na edição ao vivo: texto, log de operações, presença e
// travas por seleção. Sem rede nem relógio próprio: o tempo entra por
// parâmetro para os testes controlarem.

const lockIdle = 30 * time.Second

// log curto: quem está mais atrás que isso recebe um init novo; var para o teste baixar
var logMax = 1000

// tons médios: aparecem nos fundos claro e escuro e aguentam etiqueta branca (TestPaletaContraste)
var palette = [...]string{
	"#dc2626", "#2563eb", "#16a34a", "#ea580c", "#8b5cf6", "#0d9488",
	"#db2777", "#a16207", "#0891b2", "#c026d3", "#4d7c0f", "#64748b",
}

var (
	errReadOnly  = errors.New("documento só para leitura")
	errStale     = errors.New("revisão velha demais")
	errLocked    = errors.New("trecho travado")
	errTooBig    = errors.New("documento passa de 2 MB")
	errInvalid   = errors.New("operação inválida")
	errDuplicate = errors.New("operação já aplicada")
)

// msgErr troca a mensagem e continua respondendo a errors.Is(err, base).
type msgErr struct {
	base error
	msg  string
}

func (e msgErr) Error() string        { return e.msg }
func (e msgErr) Is(target error) bool { return target == e.base }

// peer é uma aba conectada. key agrupa as abas da mesma identidade
// ("u:"+uid para editor logado fora do /publico, "v:"+visitante no resto).
type peer struct {
	id, key, login, visitor, color string
	cid                            string // identidade da aba, para descartar op repetida após reconexão
	canEdit                        bool
	a, h                           int
	lock, idle                     bool
	selSince, active               time.Time
	send                           chan []byte
}

// Os métodos não travam d.mu: quem chama segura o lock, para juntar a
// mudança e o envio às abas numa ordem só.
type liveDoc struct {
	mu      sync.Mutex
	name    string
	publico bool
	max     int
	text    []uint16
	rev     int
	log     []Op // log[i] levou o documento à revisão rev-len(log)+i+1
	peers   map[*peer]bool
	colors  map[string]string // key -> cor
	aliases map[string]string // key -> apelido opaco ("k1"...) mostrado às abas
	seqs    map[string]int    // cid -> última seq aplicada; sobrevive à saída da aba
	dirty   bool
	// usados pelo hub (hub.go): refs e dropT sob hub.mu; flushT e peersT sob mu
	refs                  int
	dropT, flushT, peersT *time.Timer
	stop                  chan struct{} // encerra o tique de 1 s
}

type peerView struct {
	ID      string `json:"id"`
	Key     string `json:"key"`
	Name    string `json:"name"`
	Color   string `json:"color"`
	A       int    `json:"a"`
	H       int    `json:"h"`
	Lock    bool   `json:"lock"`
	Reading bool   `json:"reading"`
}

func newLiveDoc(name, text string) *liveDoc {
	// o textarea normaliza as quebras; sem isso os comprimentos divergem
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	d := &liveDoc{
		name: name, publico: isPublico(name), max: maxDoc, text: utf16Of(text),
		peers: map[*peer]bool{}, colors: map[string]string{}, aliases: map[string]string{}, seqs: map[string]int{},
	}
	if d.publico {
		d.max = maxPublicoDoc
	}
	return d
}

func (d *liveDoc) add(p *peer) {
	taken := func(c string) bool {
		for q := range d.peers {
			if q.key != p.key && q.color == c {
				return true
			}
		}
		return false
	}
	c, ok := d.colors[p.key]
	if !ok || taken(c) {
		c = palette[len(d.colors)%len(palette)] // todas em uso: repete
		for _, pc := range palette {
			if !taken(pc) {
				c = pc
				break
			}
		}
	}
	p.color, d.colors[p.key] = c, c
	if d.aliases[p.key] == "" {
		d.aliases[p.key] = fmt.Sprintf("k%d", len(d.aliases)+1)
	}
	d.peers[p] = true
}

func (d *liveDoc) remove(p *peer) {
	delete(d.peers, p)
	d.recomputeLocks()
}

func (d *liveDoc) lastSeq(cid string) int { return d.seqs[cid] }

// since devolve as operações do log posteriores a rev.
func (d *liveDoc) since(rev int) ([]Op, error) {
	if rev < d.rev-len(d.log) || rev > d.rev {
		return nil, errStale
	}
	return d.log[len(d.log)-(d.rev-rev):], nil
}

func (d *liveDoc) submit(p *peer, rev, seq int, op Op, a, h int, now time.Time) (Op, error) {
	d.touch(p, now)
	if !p.canEdit {
		return nil, errReadOnly
	}
	if p.cid != "" && seq <= d.seqs[p.cid] {
		return nil, errDuplicate
	}
	later, err := d.since(rev)
	if err != nil {
		return nil, err
	}
	for _, l := range later {
		var l2 Op
		if op, l2, err = Transform(op, l); err != nil {
			return nil, errInvalid
		}
		// a/h valem depois da op do cliente: andam pela operação do log já transformada por ela
		a, h = TransformIndex(a, l2, true), TransformIndex(h, l2, true)
	}
	for q := range d.peers {
		if q != p && q.lock && TouchesRange(op, min(q.a, q.h), max(q.a, q.h)) {
			return nil, msgErr{errLocked, "trecho travado por " + d.nameFor(p, q)}
		}
	}
	text, err := op.Apply(d.text)
	if err != nil {
		return nil, errInvalid
	}
	// documento que já veio do disco acima do limite ainda pode encolher
	if n := utf8Len(text); n > d.max && n > utf8Len(d.text) {
		if d.publico {
			return nil, msgErr{errTooBig, "documento da área pública passa de 256 KB"}
		}
		return nil, errTooBig
	}
	d.text = text
	d.rev++
	if d.log = append(d.log, op); len(d.log) > logMax {
		d.log = d.log[len(d.log)-logMax:]
	}
	d.dirty = true
	if p.cid != "" {
		d.seqs[p.cid] = seq
	}
	// p também anda, para o place comparar a seleção antiga e a nova no mesmo texto
	for q := range d.peers {
		if q.lock {
			// bordas da trava não engolem insert vizinho: o início anda, o fim fica
			s, e := &q.a, &q.h
			if q.a > q.h {
				s, e = e, s
			}
			*s, *e = TransformIndex(*s, op, true), TransformIndex(*e, op, false)
		} else {
			q.a, q.h = TransformIndex(q.a, op, true), TransformIndex(q.h, op, true)
		}
	}
	d.place(p, a, h, now)
	return op, nil
}

func (d *liveDoc) setSel(p *peer, rev, a, h int, now time.Time) error {
	d.touch(p, now)
	later, err := d.since(rev)
	if err != nil {
		return err
	}
	for _, l := range later {
		a, h = TransformIndex(a, l, true), TransformIndex(h, l, true)
	}
	d.place(p, a, h, now)
	return nil
}

// touch conta qualquer mensagem da aba, aceita ou recusada, como atividade.
// Quem volta do ócio entra no fim da fila da trava.
func (d *liveDoc) touch(p *peer, now time.Time) {
	p.active = now
	if p.idle {
		p.idle, p.selSince = false, now
		d.recomputeLocks()
	}
}

// place grava a seleção de p e refaz as travas.
func (d *liveDoc) place(p *peer, a, h int, now time.Time) {
	a, h = min(max(a, 0), len(d.text)), min(max(h, 0), len(d.text))
	s, e := min(a, h), max(a, h)
	// estender a seleção mantém a prioridade; uma seleção em outro lugar é nova
	if !overlaps(s, e, min(p.a, p.h), max(p.a, p.h)) {
		p.selSince = now
	}
	p.a, p.h = a, h
	// trava concedida só cai pelo próprio dono: quem avança sobre a de outro perde a sua
	for q := range d.peers {
		if p.lock && q != p && q.lock && overlaps(s, e, min(q.a, q.h), max(q.a, q.h)) {
			p.lock = false
		}
	}
	d.recomputeLocks()
}

// overlaps diz se [s1,e1) e [s2,e2) dividem alguma unidade; trecho vazio não divide.
func overlaps(s1, e1, s2, e2 int) bool { return s1 < e1 && s2 < e2 && s1 < e2 && s2 < e1 }

// expireLocks marca como ociosas as abas paradas há mais de lockIdle e diz
// se alguma trava mudou.
func (d *liveDoc) expireLocks(now time.Time) bool {
	for p := range d.peers {
		if now.Sub(p.active) > lockIdle {
			p.idle = true
		}
	}
	return d.recomputeLocks()
}

// recomputeLocks concede as travas a quem não se sobrepõe a uma já concedida:
// primeiro quem já trava, depois por ordem de selSince. Diz se alguma mudou.
func (d *liveDoc) recomputeLocks() bool {
	var cand []*peer
	for p := range d.peers {
		if p.canEdit && p.a != p.h && !p.idle {
			cand = append(cand, p)
		}
	}
	slices.SortFunc(cand, func(x, y *peer) int {
		if x.lock != y.lock {
			if x.lock {
				return -1
			}
			return 1
		}
		if c := x.selSince.Compare(y.selSince); c != 0 {
			return c
		}
		return strings.Compare(x.id, y.id)
	})
	want := map[*peer]bool{}
	var granted [][2]int
next:
	for _, p := range cand {
		s, e := min(p.a, p.h), max(p.a, p.h)
		for _, g := range granted {
			if overlaps(s, e, g[0], g[1]) {
				continue next
			}
		}
		granted = append(granted, [2]int{s, e})
		want[p] = true
	}
	changed := false
	for p := range d.peers {
		if p.lock != want[p] {
			p.lock, changed = want[p], true
		}
	}
	return changed
}

// nameFor é o nome de p visto por to: login só entre editores fora do /publico.
func (d *liveDoc) nameFor(to, p *peer) string {
	if !d.publico && to.canEdit && p.canEdit && p.login != "" {
		return p.login
	}
	return p.visitor
}

func (d *liveDoc) views(to *peer) []peerView {
	vs := make([]peerView, 0, len(d.peers))
	for p := range d.peers {
		vs = append(vs, peerView{
			ID: p.id, Key: d.aliases[p.key], Name: d.nameFor(to, p), Color: p.color,
			A: p.a, H: p.h, Lock: p.lock, Reading: !p.canEdit,
		})
	}
	slices.SortFunc(vs, func(x, y peerView) int { return strings.Compare(x.ID, y.ID) })
	return vs
}

// utf8Len conta os bytes UTF-8 sem montar a string: cada metade de um par
// substituto vale 2, o par inteiro 4.
func utf8Len(u []uint16) (n int) {
	for _, c := range u {
		switch {
		case c < 0x80:
			n++
		case c < 0x800, c >= 0xd800 && c < 0xe000:
			n += 2
		default:
			n += 3
		}
	}
	return
}
