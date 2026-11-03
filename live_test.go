package main

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

func novoPeer(d *liveDoc, id, key, login, visitor string, canEdit bool) *peer {
	p := &peer{id: id, key: key, login: login, visitor: visitor, canEdit: canEdit, cid: "c-" + id}
	d.add(p)
	return p
}

// ins monta a operação que insere s em pos num documento de n unidades.
func ins(pos int, s string, n int) Op {
	var b opBuilder // descarta os retains vazios
	b.add(Comp{Retain: pos})
	b.add(Comp{Insert: s})
	b.add(Comp{Retain: n - pos})
	return Op(b)
}

// envia faz um submit com seq crescente por peer, como o cliente.
func envia(t *testing.T, d *liveDoc, p *peer, rev int, op Op, a, h int, now time.Time) error {
	t.Helper()
	_, err := d.submit(p, rev, d.lastSeq(p.cid)+1, op, a, h, now)
	return err
}

func sel(t *testing.T, d *liveDoc, p *peer, a, h int, now time.Time) {
	t.Helper()
	if err := d.setSel(p, d.rev, a, h, now); err != nil {
		t.Fatal(err)
	}
}

func TestLiveDocNormalizaQuebras(t *testing.T) {
	d := newLiveDoc("ana/x", "a\r\nb\rc")
	if got := stringOf(d.text); got != "a\nb\nc" || len(d.text) != 5 {
		t.Fatalf("texto %q (%d)", got, len(d.text))
	}
	if d.max != maxDoc || newLiveDoc("publico/x", "").max != maxPublicoDoc {
		t.Fatal("limite errado")
	}
}

func TestSubmitConcorrente(t *testing.T) {
	d := newLiveDoc("ana/x", "abc")
	a := novoPeer(d, "1", "u:1", "ana", "Capivara", true)
	b := novoPeer(d, "2", "u:2", "bruno", "Tatu", true)
	opA, opB := Op{{Retain: 1}, {Insert: "X"}, {Retain: 2}}, Op{{Retain: 1}, {Insert: "Y"}, {Retain: 2}}
	if err := envia(t, d, a, 0, opA, 2, 2, t0); err != nil {
		t.Fatal(err)
	}
	got, err := d.submit(b, 0, 1, opB, 2, 2, t0)
	if err != nil {
		t.Fatal(err)
	}
	// o servidor chama Transform com o cliente como a: o mesmo caminho do TestTransform
	want := convergem(t, utf16Of("abc"), opB, opA)
	if stringOf(d.text) != want || want != "aYXbc" || d.rev != 2 {
		t.Fatalf("texto %q rev %d, quero %q", stringOf(d.text), d.rev, want)
	}
	if got.BaseLen() != 4 {
		t.Fatalf("op transformada %v", got)
	}
}

func TestSubmitStale(t *testing.T) {
	old := logMax
	logMax = 2
	t.Cleanup(func() { logMax = old })
	d := newLiveDoc("ana/x", "")
	a := novoPeer(d, "1", "u:1", "ana", "Capivara", true)
	for i := 0; i < 3; i++ {
		if err := envia(t, d, a, i, ins(i, "x", i), i+1, i+1, t0); err != nil {
			t.Fatal(err)
		}
	}
	if len(d.log) != 2 {
		t.Fatalf("log com %d", len(d.log))
	}
	if err := envia(t, d, a, 0, ins(0, "y", 0), 1, 1, t0); !errors.Is(err, errStale) {
		t.Fatalf("err = %v", err)
	}
	if err := envia(t, d, a, 1, ins(0, "y", 1), 1, 1, t0); err != nil {
		t.Fatalf("rev 1 ainda está no log: %v", err)
	}
}

func TestSubmitDuplicado(t *testing.T) {
	d := newLiveDoc("ana/x", "abc")
	a := novoPeer(d, "1", "u:1", "ana", "Capivara", true)
	if _, err := d.submit(a, 0, 1, ins(0, "x", 3), 1, 1, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := d.submit(a, 0, 1, ins(0, "x", 3), 1, 1, t0); !errors.Is(err, errDuplicate) {
		t.Fatalf("err = %v", err)
	}
	if stringOf(d.text) != "xabc" || d.rev != 1 || d.lastSeq(a.cid) != 1 {
		t.Fatalf("texto %q rev %d seq %d", stringOf(d.text), d.rev, d.lastSeq(a.cid))
	}
	// a aba reconecta com o mesmo cid: a memória é do documento, não do peer
	d.remove(a)
	a2 := novoPeer(d, "2", "u:1", "ana", "Capivara", true)
	a2.cid = a.cid
	if _, err := d.submit(a2, 0, 1, ins(0, "x", 3), 1, 1, t0); !errors.Is(err, errDuplicate) {
		t.Fatalf("depois de reconectar: %v", err)
	}
	if _, err := d.submit(a2, 1, 2, ins(0, "y", 4), 1, 1, t0); err != nil || stringOf(d.text) != "yxabc" {
		t.Fatalf("seq 2: %v %q", err, stringOf(d.text))
	}
	if d.lastSeq("outro") != 0 {
		t.Fatal("cid desconhecido deveria dar 0")
	}
}

func TestTravaBloqueiaOutros(t *testing.T) {
	const txt = "0123456789abcdef"
	d := newLiveDoc("ana/x", txt)
	a := novoPeer(d, "1", "u:1", "ana", "Capivara", true)
	b := novoPeer(d, "2", "u:2", "bruno", "Tatu", true)
	sel(t, d, b, 5, 10, t0)
	if !b.lock {
		t.Fatal("B deveria travar")
	}
	err := envia(t, d, a, 0, ins(7, "x", 16), 8, 8, t0)
	if !errors.Is(err, errLocked) || err.Error() != "trecho travado por bruno" {
		t.Fatalf("insert em 7: %v", err)
	}
	if err := envia(t, d, a, 0, ins(5, "x", 16), 6, 6, t0); err != nil {
		t.Fatalf("insert na borda: %v", err)
	}
	if b.a != 6 || b.h != 11 {
		t.Fatalf("trava andou para [%d,%d), quero [6,11)", b.a, b.h)
	}
	if err := envia(t, d, a, 1, Op{{Retain: 4}, {Delete: 1}, {Retain: 12}}, 4, 4, t0); err != nil {
		t.Fatalf("apaga [4,5): %v", err)
	}
	if err := envia(t, d, b, 2, ins(7, "z", 16), 8, 8, t0); err != nil {
		t.Fatalf("dono edita dentro: %v", err)
	}
	if got := stringOf(d.text); got != "0123x56z789abcdef" {
		t.Fatalf("texto %q", got)
	}
}

func TestTravaBordaFinal(t *testing.T) {
	d := newLiveDoc("ana/x", "0123456789abcdef")
	a := novoPeer(d, "1", "u:1", "ana", "Capivara", true)
	b := novoPeer(d, "2", "u:2", "bruno", "Tatu", true)
	sel(t, d, b, 10, 5, t0) // seleção de trás para frente
	if err := envia(t, d, a, 0, ins(10, "xy", 16), 12, 12, t0); err != nil {
		t.Fatal(err)
	}
	if b.a != 10 || b.h != 5 || !b.lock {
		t.Fatalf("insert na borda final entrou na trava: a=%d h=%d", b.a, b.h)
	}
}

func TestTravaPrimeiroGanha(t *testing.T) {
	d := newLiveDoc("ana/x", "0123456789abcdef")
	b := novoPeer(d, "2", "u:2", "bruno", "Tatu", true)
	c := novoPeer(d, "3", "u:3", "carla", "Arara", true)
	sel(t, d, b, 5, 10, t0)
	sel(t, d, c, 8, 12, t0.Add(time.Second))
	if !b.lock || c.lock {
		t.Fatalf("B %v C %v", b.lock, c.lock)
	}
	// estender a seleção não tira a prioridade de quem selecionou antes
	sel(t, d, b, 5, 11, t0.Add(2*time.Second))
	if !b.lock || c.lock {
		t.Fatalf("depois de estender: B %v C %v", b.lock, c.lock)
	}
	sel(t, d, b, 5, 5, t0.Add(3*time.Second))
	if b.lock || !c.lock {
		t.Fatalf("B soltou: B %v C %v", b.lock, c.lock)
	}
}

func TestTravaEDaAba(t *testing.T) {
	d := newLiveDoc("ana/x", "0123456789abcdef")
	outro := novoPeer(d, "0", "u:9", "zeca", "Gato", true)
	a1 := novoPeer(d, "1", "u:1", "ana", "Capivara", true)
	a2 := novoPeer(d, "2", "u:1", "ana", "Capivara", true)
	sel(t, d, a1, 5, 10, t0)
	if err := envia(t, d, a2, 0, ins(7, "x", 16), 8, 8, t0); !errors.Is(err, errLocked) {
		t.Fatalf("a outra aba deveria ser bloqueada: %v", err)
	}
	if a1.color == "" || a1.color != a2.color || a1.color == outro.color {
		t.Fatalf("cores %q %q %q", a1.color, a2.color, outro.color)
	}
}

func TestCoresDistintas(t *testing.T) {
	d := newLiveDoc("ana/x", "")
	seen := map[string]bool{}
	var ps []*peer
	for i := 0; i < len(palette); i++ {
		p := novoPeer(d, string(rune('a'+i)), "u:"+string(rune('a'+i)), "", "V", true)
		if seen[p.color] {
			t.Fatalf("cor repetida %q", p.color)
		}
		seen[p.color] = true
		ps = append(ps, p)
	}
	// quem sai libera a cor; quem volta retoma a sua se estiver livre
	d.remove(ps[3])
	volta := novoPeer(d, "x", ps[3].key, "", "V", true)
	if volta.color != ps[3].color {
		t.Fatalf("cor de volta %q, quero %q", volta.color, ps[3].color)
	}
}

func TestTravaExpira(t *testing.T) {
	d := newLiveDoc("ana/x", "0123456789abcdef")
	b := novoPeer(d, "2", "u:2", "bruno", "Tatu", true)
	sel(t, d, b, 5, 10, t0)
	if d.expireLocks(t0.Add(29 * time.Second)) {
		t.Fatal("expirou cedo")
	}
	if !d.expireLocks(t0.Add(31*time.Second)) || b.lock {
		t.Fatal("trava deveria soltar")
	}
	if d.expireLocks(t0.Add(40 * time.Second)) {
		t.Fatal("nada mudou, deveria dar false")
	}
	sel(t, d, b, 5, 10, t0.Add(41*time.Second))
	if !b.lock {
		t.Fatal("nova seleção deveria restaurar a trava")
	}
}

func TestLeitorNaoEdita(t *testing.T) {
	d := newLiveDoc("ana/x", "0123456789abcdef")
	r := novoPeer(d, "1", "v:1", "", "Tatu", false)
	if err := envia(t, d, r, 0, ins(0, "x", 16), 1, 1, t0); !errors.Is(err, errReadOnly) {
		t.Fatalf("err = %v", err)
	}
	sel(t, d, r, 5, 10, t0)
	if r.lock {
		t.Fatal("leitor não trava")
	}
}

func TestNomesPorDestinatario(t *testing.T) {
	nome := func(vs []peerView, id string) peerView {
		for _, v := range vs {
			if v.ID == id {
				return v
			}
		}
		t.Fatalf("peer %s ausente", id)
		return peerView{}
	}
	d := newLiveDoc("ana/x", "")
	a := novoPeer(d, "1", "u:1", "ana", "Capivara Anônima", true)
	novoPeer(d, "2", "u:2", "bruno", "Tatu Curioso", true)
	novoPeer(d, "3", "u:2", "bruno", "Tatu Curioso", true)
	r := novoPeer(d, "4", "v:9", "", "Arara Azul", false)
	if v := nome(d.views(a), "2"); v.Name != "bruno" || v.Reading {
		t.Fatalf("editor vê %+v", v)
	}
	if v := nome(d.views(r), "2"); v.Name != "Tatu Curioso" {
		t.Fatalf("leitor vê %+v", v)
	}
	if v := nome(d.views(a), "4"); v.Name != "Arara Azul" || !v.Reading {
		t.Fatalf("editor vê o leitor %+v", v)
	}
	vb, vb2 := nome(d.views(a), "2"), nome(d.views(a), "3")
	if vb.Key != vb2.Key || vb.Key == nome(d.views(a), "1").Key || strings.Contains(vb.Key, "u:") || vb.Key == "" {
		t.Fatalf("apelidos %q %q", vb.Key, vb2.Key)
	}
	if len(d.views(r)) != 4 {
		t.Fatal("presença deve incluir todo mundo, a própria aba também")
	}

	p := newLiveDoc("publico/x", "")
	pa := novoPeer(p, "1", "v:1", "ana", "Capivara Anônima", true)
	pb := novoPeer(p, "2", "v:2", "bruno", "Tatu Curioso", true)
	if v := nome(p.views(pb), "1"); v.Name != "Capivara Anônima" {
		t.Fatalf("no /publico vê %+v", v)
	}
	if v := nome(p.views(pa), "2"); v.Name != "Tatu Curioso" {
		t.Fatalf("no /publico vê %+v", v)
	}
}

func TestSubmitLimite(t *testing.T) {
	d := newLiveDoc("publico/x", strings.Repeat("a", maxPublicoDoc-1))
	a := novoPeer(d, "1", "v:1", "", "Capivara", true)
	n := len(d.text)
	err := envia(t, d, a, 0, ins(n, "é", n), n+1, n+1, t0)
	if !errors.Is(err, errTooBig) || err.Error() != "documento da área pública passa de 256 KB" {
		t.Fatalf("err = %v", err)
	}
	if len(d.text) != n || d.rev != 0 || d.dirty {
		t.Fatal("documento mudou")
	}
	if err := envia(t, d, a, 0, ins(n, "b", n), n+1, n+1, t0); err != nil {
		t.Fatalf("exatamente no limite: %v", err)
	}
	g := newLiveDoc("ana/x", strings.Repeat("a", maxDoc))
	ga := novoPeer(g, "1", "u:1", "ana", "Capivara", true)
	if err := envia(t, g, ga, 0, ins(0, "b", maxDoc), 1, 1, t0); !errors.Is(err, errTooBig) || err.Error() != "documento passa de 2 MB" {
		t.Fatalf("err = %v", err)
	}
}

func TestSubmitInvalida(t *testing.T) {
	d := newLiveDoc("ana/x", "a😀b")
	a := novoPeer(d, "1", "u:1", "ana", "Capivara", true)
	for _, op := range []Op{{{Retain: 2}}, {{Retain: 2}, {Delete: 1}, {Retain: 1}}} {
		if err := envia(t, d, a, 0, op, 0, 0, t0); !errors.Is(err, errInvalid) {
			t.Fatalf("%v: err = %v", op, err)
		}
	}
	if err := d.setSel(a, 5, 0, 0, t0); !errors.Is(err, errStale) {
		t.Fatalf("rev do futuro: %v", err)
	}
	sel(t, d, a, -3, 99, t0)
	if a.a != 0 || a.h != 4 {
		t.Fatalf("seleção fora do texto [%d,%d)", a.a, a.h)
	}
}

func TestCursorAndaComInsercao(t *testing.T) {
	d := newLiveDoc("ana/x", "abcdef")
	a := novoPeer(d, "1", "u:1", "ana", "Capivara", true)
	c := novoPeer(d, "3", "u:3", "carla", "Arara", true)
	sel(t, d, c, 3, 3, t0)
	if err := envia(t, d, a, 0, ins(1, "xx", 6), 3, 3, t0); err != nil {
		t.Fatal(err)
	}
	if c.a != 5 || c.h != 5 {
		t.Fatalf("cursor de C = [%d,%d], quero 5", c.a, c.h)
	}
	if a.a != 3 || !d.dirty || d.rev != 1 {
		t.Fatal("estado do autor")
	}
}

func TestTravaNaoTomadaPorEspera(t *testing.T) {
	d := newLiveDoc("ana/x", "0123456789abcdefghijk")
	a := novoPeer(d, "1", "u:1", "ana", "Capivara", true)
	b := novoPeer(d, "2", "u:2", "bruno", "Tatu", true)
	c := novoPeer(d, "3", "u:3", "carla", "Arara", true)
	sel(t, d, a, 5, 10, t0)
	sel(t, d, b, 8, 20, t0.Add(time.Second))
	sel(t, d, c, 12, 15, t0.Add(2*time.Second))
	if !a.lock || b.lock || !c.lock {
		t.Fatalf("A %v B %v C %v", a.lock, b.lock, c.lock)
	}
	sel(t, d, a, 5, 5, t0.Add(3*time.Second))
	if b.lock || !c.lock {
		t.Fatalf("A soltou e C perdeu: B %v C %v", b.lock, c.lock)
	}
	// C digita dentro da própria seleção: B, que espera há mais tempo, não toma a trava
	if err := envia(t, d, c, d.rev, Op{{Retain: 13}, {Insert: "x"}, {Retain: 8}}, 12, 16, t0.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	if b.lock || !c.lock {
		t.Fatalf("depois de C digitar: B %v C %v", b.lock, c.lock)
	}
	sel(t, d, c, 12, 12, t0.Add(5*time.Second))
	if !b.lock {
		t.Fatal("C soltou: B deveria travar")
	}
}

func TestTravaNaoTomadaPorSalto(t *testing.T) {
	d := newLiveDoc("ana/x", "0123456789abcdef")
	b := novoPeer(d, "2", "u:2", "bruno", "Tatu", true)
	c := novoPeer(d, "3", "u:3", "carla", "Arara", true)
	sel(t, d, b, 1, 3, t0)
	sel(t, d, c, 12, 15, t0.Add(time.Second))
	sel(t, d, b, 13, 14, t0.Add(2*time.Second)) // pula direto, sem passar por seleção vazia
	if b.lock || !c.lock {
		t.Fatalf("B %v C %v", b.lock, c.lock)
	}
	if !b.selSince.Equal(t0.Add(2 * time.Second)) {
		t.Fatal("seleção nova sem sobreposição deveria renovar selSince")
	}
}

func TestTravaVoltaDoIdle(t *testing.T) {
	d := newLiveDoc("ana/x", "0123456789abcdef")
	b := novoPeer(d, "2", "u:2", "bruno", "Tatu", true)
	c := novoPeer(d, "3", "u:3", "carla", "Arara", true)
	sel(t, d, b, 5, 10, t0)
	d.expireLocks(t0.Add(31 * time.Second))
	sel(t, d, c, 8, 12, t0.Add(32*time.Second))
	if !c.lock {
		t.Fatal("C deveria travar com B ocioso")
	}
	sel(t, d, b, 5, 10, t0.Add(40*time.Second)) // B volta com a seleção antiga
	if b.lock || !c.lock {
		t.Fatalf("B voltou: B %v C %v", b.lock, c.lock)
	}
	sel(t, d, c, 8, 8, t0.Add(41*time.Second))
	if !b.lock {
		t.Fatal("C soltou: B deveria travar")
	}
}

func TestSubmitRecusadoContaAtividade(t *testing.T) {
	d := newLiveDoc("ana/x", "0123456789abcdef")
	b := novoPeer(d, "2", "u:2", "bruno", "Tatu", true)
	sel(t, d, b, 5, 10, t0)
	d.expireLocks(t0.Add(31 * time.Second))
	now := t0.Add(32 * time.Second)
	if err := envia(t, d, b, 99, ins(0, "x", 16), 1, 1, now); !errors.Is(err, errStale) {
		t.Fatalf("err = %v", err)
	}
	if b.idle || !b.active.Equal(now) || !b.lock {
		t.Fatalf("recusa deveria contar como atividade: idle %v lock %v", b.idle, b.lock)
	}
	if d.expireLocks(t0.Add(40 * time.Second)) {
		t.Fatal("ativa há 8 s, não expira")
	}
}

func TestSubmitDocJaAcima(t *testing.T) {
	d := newLiveDoc("publico/x", strings.Repeat("a", maxPublicoDoc+10))
	a := novoPeer(d, "1", "v:1", "", "Capivara", true)
	n := len(d.text)
	if err := envia(t, d, a, 0, Op{{Delete: 1}, {Retain: n - 1}}, 0, 0, t0); err != nil {
		t.Fatalf("encolher deveria passar: %v", err)
	}
	if err := envia(t, d, a, 1, ins(0, "b", n-1), 1, 1, t0); !errors.Is(err, errTooBig) {
		t.Fatalf("crescer acima do limite: %v", err)
	}
}

// luminância relativa do WCAG de uma cor "#rrggbb" ou "#rgb"
func luminance(t *testing.T, hex string) float64 {
	if len(hex) == 4 && hex[0] == '#' {
		hex = string([]byte{'#', hex[1], hex[1], hex[2], hex[2], hex[3], hex[3]})
	}
	if len(hex) != 7 || hex[0] != '#' {
		t.Fatalf("cor inválida %q", hex)
	}
	var rgb [3]float64
	for i := range rgb {
		v, err := strconv.ParseUint(hex[1+2*i:3+2*i], 16, 8)
		if err != nil {
			t.Fatalf("cor inválida %q", hex)
		}
		c := float64(v) / 255
		if c <= 0.04045 {
			c /= 12.92
		} else {
			c = math.Pow((c+0.055)/1.055, 2.4)
		}
		rgb[i] = c
	}
	return 0.2126*rgb[0] + 0.7152*rgb[1] + 0.0722*rgb[2]
}

// razão de contraste do WCAG entre duas cores
func contraste(t *testing.T, a, b string) float64 {
	x, y := luminance(t, a), luminance(t, b)
	return (max(x, y) + 0.05) / (min(x, y) + 0.05)
}

// cada cor vira cursor e seleção sobre o --bg de cada tema, e fundo de etiqueta com texto branco
func TestPaletaContraste(t *testing.T) {
	fundos := []string{"#ffffff"} // texto da etiqueta
	for _, props := range lerTemas(t) {
		fundos = append(fundos, props["--bg"])
	}
	if len(fundos) != 1+len(temaIDs) {
		t.Errorf("%d fundos, quero os %d temas mais o branco", len(fundos), len(temaIDs))
	}
	seen := map[string]bool{}
	for _, c := range palette {
		if seen[c] {
			t.Errorf("cor repetida %q", c)
		}
		seen[c] = true
		for _, bg := range fundos {
			if r := contraste(t, c, bg); r < 3 {
				t.Errorf("%s contra %s: %.2f:1, quero 3:1", c, bg, r)
			}
		}
	}
	if len(seen) != 12 {
		t.Errorf("%d cores distintas, quero 12", len(seen))
	}
}
