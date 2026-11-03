package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf16"
)

// Operações de texto concorrentes (OT) no formato do ot.js. Todas as
// posições e comprimentos são em unidades UTF-16, as mesmas do
// textarea.selectionStart no navegador.

// Comp é um componente da operação: exatamente um campo preenchido.
type Comp struct {
	Retain, Delete int
	Insert         string
}

// Op é uma sequência de componentes. No JSON: número positivo = retain,
// negativo = delete, string = insert.
type Op []Comp

func utf16Of(s string) []uint16  { return utf16.Encode([]rune(s)) }
func stringOf(u []uint16) string { return string(utf16.Decode(u)) }
func isHigh(u uint16) bool       { return u >= 0xd800 && u < 0xdc00 }
func isLow(u uint16) bool        { return u >= 0xdc00 && u < 0xe000 }
func (c Comp) insertLen() int    { return len(utf16Of(c.Insert)) }
func (c Comp) length() int       { return c.Retain + c.Delete + c.insertLen() }
func (c Comp) isInsert() bool    { return c.Insert != "" }

func (c Comp) MarshalJSON() ([]byte, error) {
	switch {
	case c.Insert != "":
		return json.Marshal(c.Insert)
	case c.Delete > 0:
		return json.Marshal(-c.Delete)
	}
	return json.Marshal(c.Retain)
}

func (c *Comp) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) > 0 && b[0] == '"' {
		if loneSurrogate(b) {
			return errors.New("insert com surrogate solto")
		}
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		if s == "" {
			return errors.New("insert vazio")
		}
		*c = Comp{Insert: s}
		return nil
	}
	var n int
	if err := json.Unmarshal(b, &n); err != nil || bytes.Equal(b, []byte("null")) {
		return errors.New("componente deve ser inteiro ou string")
	}
	switch {
	case n > 0:
		*c = Comp{Retain: n}
	case n < 0:
		*c = Comp{Delete: -n}
	default:
		return errors.New("componente zero")
	}
	return nil
}

// loneSurrogate olha as sequências \uXXXX do JSON cru: o encoding/json troca
// um surrogate solto por U+FFFD em silêncio, então só dá para flagrar aqui.
func loneSurrogate(raw []byte) bool {
	hex := func(i int) (uint16, bool) {
		if i+6 > len(raw) || raw[i] != '\\' || raw[i+1] != 'u' {
			return 0, false
		}
		var v uint16
		for _, ch := range raw[i+2 : i+6] {
			switch {
			case ch >= '0' && ch <= '9':
				ch -= '0'
			case ch >= 'a' && ch <= 'f':
				ch -= 'a' - 10
			case ch >= 'A' && ch <= 'F':
				ch -= 'A' - 10
			default:
				return 0, false
			}
			v = v<<4 | uint16(ch)
		}
		return v, true
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' {
			continue
		}
		if v, ok := hex(i); ok {
			if isHigh(v) {
				if w, ok := hex(i + 6); !ok || !isLow(w) {
					return true
				}
				i += 6
			} else if isLow(v) {
				return true
			}
			i += 5
		} else {
			i++ // pula o caractere escapado (\" ou \\)
		}
	}
	return false
}

// BaseLen é o comprimento do documento sobre o qual a operação vale.
func (o Op) BaseLen() (n int) {
	for _, c := range o {
		n += c.Retain + c.Delete
	}
	return
}

// TargetLen é o comprimento do documento depois da operação.
func (o Op) TargetLen() (n int) {
	for _, c := range o {
		n += c.Retain + c.insertLen()
	}
	return
}

// Apply aplica a operação. Recusa tamanho errado e fronteira de componente
// entre as duas metades de um par substituto: o texto sairia corrompido.
func (o Op) Apply(doc []uint16) ([]uint16, error) {
	if o.BaseLen() != len(doc) {
		return nil, fmt.Errorf("operação para %d unidades, documento tem %d", o.BaseLen(), len(doc))
	}
	out := make([]uint16, 0, o.TargetLen())
	pos := 0
	for _, c := range o {
		switch {
		case c.Retain < 0 || c.Delete < 0:
			return nil, errors.New("componente negativo")
		case c.isInsert():
			out = append(out, utf16Of(c.Insert)...)
			continue
		case c.Retain > 0:
			out = append(out, doc[pos:pos+c.Retain]...)
			pos += c.Retain
		default:
			pos += c.Delete
		}
		if pos > 0 && pos < len(doc) && isHigh(doc[pos-1]) && isLow(doc[pos]) {
			return nil, errors.New("operação corta um par substituto")
		}
	}
	return out, nil
}

// opBuilder junta componentes vizinhos do mesmo tipo e descarta os vazios.
type opBuilder Op

func (b *opBuilder) add(c Comp) {
	if c.length() == 0 {
		return
	}
	if n := len(*b); n > 0 {
		last := &(*b)[n-1]
		switch {
		case c.Retain > 0 && last.Retain > 0:
			last.Retain += c.Retain
			return
		case c.Delete > 0 && last.Delete > 0:
			last.Delete += c.Delete
			return
		case c.isInsert() && last.isInsert():
			last.Insert += c.Insert
			return
		}
	}
	*b = append(*b, c)
}

// opIter percorre uma operação podendo consumir só parte de um retain ou delete.
type opIter struct {
	o      Op
	i, off int
}

func (it *opIter) done() bool { return it.i >= len(it.o) }

// cur é o componente atual, já sem o trecho consumido.
func (it *opIter) cur() Comp {
	c := it.o[it.i]
	c.Retain -= min(it.off, c.Retain)
	c.Delete -= min(it.off, c.Delete)
	return c
}

func (it *opIter) take(n int) {
	if it.off += n; it.off >= it.o[it.i].Retain+it.o[it.i].Delete {
		it.i++
		it.off = 0
	}
}

// Transform transforma duas operações feitas sobre o mesmo documento:
// apply(apply(d,a),b') == apply(apply(d,b),a'). No empate de inserção na
// mesma posição o insert de a vem primeiro; o servidor passa a operação do
// cliente como a, então quem chegou antes no log fica à esquerda.
func Transform(a, b Op) (Op, Op, error) {
	if a.BaseLen() != b.BaseLen() {
		return nil, nil, fmt.Errorf("operações sobre documentos diferentes (%d e %d)", a.BaseLen(), b.BaseLen())
	}
	var a2, b2 opBuilder
	ia, ib := opIter{o: a}, opIter{o: b}
	for !ia.done() || !ib.done() {
		if !ia.done() && ia.cur().isInsert() {
			c := ia.cur()
			a2.add(c)
			b2.add(Comp{Retain: c.insertLen()})
			ia.i++
			continue
		}
		if !ib.done() && ib.cur().isInsert() {
			c := ib.cur()
			a2.add(Comp{Retain: c.insertLen()})
			b2.add(c)
			ib.i++
			continue
		}
		if ia.done() || ib.done() {
			return nil, nil, errors.New("operações de tamanhos diferentes")
		}
		ca, cb := ia.cur(), ib.cur()
		n := min(ca.Retain+ca.Delete, cb.Retain+cb.Delete)
		switch {
		case ca.Retain > 0 && cb.Retain > 0:
			a2.add(Comp{Retain: n})
			b2.add(Comp{Retain: n})
		case ca.Delete > 0 && cb.Retain > 0:
			a2.add(Comp{Delete: n})
		case ca.Retain > 0 && cb.Delete > 0:
			b2.add(Comp{Delete: n})
		}
		ia.take(n)
		ib.take(n)
	}
	return Op(a2), Op(b2), nil
}

// TransformIndex leva a posição i do documento antigo para o novo. Com insert
// exatamente em i, a posição só anda para a direita se stickRight.
func TransformIndex(i int, o Op, stickRight bool) int {
	res, pos := i, 0
	for _, c := range o {
		switch {
		case c.isInsert():
			if pos < i || (pos == i && stickRight) {
				res += c.insertLen()
			}
		case c.Delete > 0:
			if pos < i {
				res -= min(c.Delete, i-pos)
			}
			pos += c.Delete
		default:
			pos += c.Retain
		}
	}
	return res
}

// TouchesRange diz se o apaga alguma unidade de [from,to) ou insere
// estritamente dentro dele (inserir nas bordas não altera o trecho).
func TouchesRange(o Op, from, to int) bool {
	pos := 0
	for _, c := range o {
		switch {
		case c.isInsert():
			if from < pos && pos < to {
				return true
			}
		case c.Delete > 0:
			if pos < to && pos+c.Delete > from {
				return true
			}
			pos += c.Delete
		default:
			pos += c.Retain
		}
	}
	return false
}
