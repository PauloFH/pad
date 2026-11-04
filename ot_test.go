package main

import (
	"encoding/json"
	"math"
	"math/rand"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func mustApply(t *testing.T, doc []uint16, o Op) []uint16 {
	t.Helper()
	out, err := o.Apply(doc)
	if err != nil {
		t.Fatalf("Apply(%v): %v", o, err)
	}
	return out
}

// convergem confere apply(apply(d,a),b') == apply(apply(d,b),a').
func convergem(t *testing.T, doc []uint16, a, b Op) string {
	t.Helper()
	a2, b2, err := Transform(a, b)
	if err != nil {
		t.Fatalf("Transform(%v, %v): %v", a, b, err)
	}
	x := stringOf(mustApply(t, mustApply(t, doc, a), b2))
	y := stringOf(mustApply(t, mustApply(t, doc, b), a2))
	if x != y {
		t.Fatalf("divergiu em %q com a=%v b=%v: %q != %q", stringOf(doc), a, b, x, y)
	}
	return x
}

func TestOpVetores(t *testing.T) {
	raw, err := os.ReadFile("testdata/ot-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Apply []struct {
			Doc, Want string
			Op        Op
		}
		ApplyErr []struct {
			Doc string
			Op  Op
		}
		Transform []struct {
			Doc, Want string
			A, B      Op
		}
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	for _, c := range v.Apply {
		if got := stringOf(mustApply(t, utf16Of(c.Doc), c.Op)); got != c.Want {
			t.Errorf("apply %q %v = %q, quero %q", c.Doc, c.Op, got, c.Want)
		}
	}
	for _, c := range v.ApplyErr {
		if _, err := c.Op.Apply(utf16Of(c.Doc)); err == nil {
			t.Errorf("apply %q %v deveria falhar", c.Doc, c.Op)
		}
	}
	for _, c := range v.Transform {
		if got := convergem(t, utf16Of(c.Doc), c.A, c.B); got != c.Want {
			t.Errorf("transform %q = %q, quero %q", c.Doc, got, c.Want)
		}
	}
}

func TestOpJSON(t *testing.T) {
	const in = `[3,"x",-2]`
	var o Op
	if err := json.Unmarshal([]byte(in), &o); err != nil {
		t.Fatal(err)
	}
	want := Op{{Retain: 3}, {Insert: "x"}, {Delete: 2}}
	if !reflect.DeepEqual(o, want) {
		t.Fatalf("got %v", o)
	}
	out, _ := json.Marshal(o)
	if string(out) != in {
		t.Fatalf("ida e volta: %s", out)
	}
	for _, bad := range []string{`[0]`, `[""]`, `[true]`, `["\ud800"]`, `["\udc00x"]`, `[1.5]`, `[null]`} {
		var o Op
		if err := json.Unmarshal([]byte(bad), &o); err == nil {
			t.Errorf("%s deveria falhar", bad)
		}
	}
	var par Op
	if err := json.Unmarshal([]byte(`["😀"]`), &par); err != nil || par[0].Insert != "😀" {
		t.Errorf("par substituto válido: %v %v", par, err)
	}
}

// opAleatoria gera uma operação válida sobre doc sem cortar pares substitutos.
func opAleatoria(r *rand.Rand, doc []uint16) Op {
	var o Op
	add := func(c Comp) {
		o = append(o, c)
	}
	for i := 0; i < len(doc); {
		n := 1
		if doc[i] >= 0xd800 && doc[i] < 0xdc00 {
			n = 2
		}
		switch r.Intn(5) {
		case 0:
			add(Comp{Insert: []string{"a", "ç", "😀", "aç"}[r.Intn(4)]})
			continue
		case 1:
			add(Comp{Delete: n})
		default:
			add(Comp{Retain: n})
		}
		i += n
	}
	if r.Intn(3) == 0 {
		add(Comp{Insert: "😀"})
	}
	return o
}

func TestTransformAleatorio(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	alfabeto := []string{"a", "ç", "😀"}
	for i := 0; i < 2000; i++ {
		var s string
		for n := r.Intn(12); n > 0; n-- {
			s += alfabeto[r.Intn(3)]
		}
		doc := utf16Of(s)
		convergem(t, doc, opAleatoria(r, doc), opAleatoria(r, doc))
	}
}

func TestTransformIndex(t *testing.T) {
	ins := Op{{Retain: 2}, {Insert: "xy"}, {Retain: 1}}
	if got := TransformIndex(2, ins, true); got != 4 {
		t.Errorf("stickRight = %d", got)
	}
	if got := TransformIndex(2, ins, false); got != 2 {
		t.Errorf("sem stickRight = %d", got)
	}
	del := Op{{Retain: 1}, {Delete: 3}, {Retain: 1}}
	if got := TransformIndex(2, del, true); got != 1 {
		t.Errorf("dentro do delete = %d", got)
	}
	if got := TransformIndex(5, del, true); got != 2 {
		t.Errorf("depois do delete = %d", got)
	}
}

func TestTouchesRange(t *testing.T) {
	cases := []struct {
		name string
		op   Op
		want bool
	}{
		{"insert em 5", Op{{Retain: 5}, {Insert: "x"}, {Retain: 7}}, false},
		{"insert em 7", Op{{Retain: 7}, {Insert: "x"}, {Retain: 5}}, true},
		{"insert em 10", Op{{Retain: 10}, {Insert: "x"}, {Retain: 2}}, false},
		{"apaga [4,5)", Op{{Retain: 4}, {Delete: 1}, {Retain: 7}}, false},
		{"apaga [9,11)", Op{{Retain: 9}, {Delete: 2}, {Retain: 1}}, true},
	}
	for _, c := range cases {
		if got := TouchesRange(c.op, 5, 10); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
	}
}

// um componente fora de qualquer tamanho de documento não pode virar operação nem derrubar o Apply.
func TestOpComponenteForaDoLimite(t *testing.T) {
	for _, n := range []int{maxComp + 1, -maxComp - 1, math.MaxInt, math.MinInt} {
		var c Comp
		if err := c.UnmarshalJSON([]byte(strconv.Itoa(n))); err == nil {
			t.Errorf("aceitou o componente %d: %+v", n, c)
		}
	}
	for _, o := range []Op{
		{{Retain: math.MaxInt}, {Retain: math.MaxInt}, {Retain: 2}},
		{{Retain: math.MaxInt}, {Delete: math.MaxInt}, {Retain: 2}},
		{{Delete: math.MaxInt}, {Delete: math.MaxInt}, {Delete: 2}},
	} {
		if _, err := o.Apply(nil); err == nil {
			t.Errorf("Apply(%+v) sem erro", o)
		}
	}
}

func TestOpComponentesDemais(t *testing.T) {
	limite := "[" + strings.TrimSuffix(strings.Repeat("1,", maxCompsOp), ",") + "]"
	var o Op
	if err := json.Unmarshal([]byte(limite), &o); err != nil || len(o) != maxCompsOp {
		t.Fatalf("no limite: %d, %v", len(o), err)
	}
	if err := json.Unmarshal([]byte("[1,"+limite[1:]), &o); err == nil {
		t.Fatal("aceitou componentes demais")
	}
	for _, bad := range []string{`{}`, `1`, `"x"`} {
		if err := json.Unmarshal([]byte(bad), &o); err == nil {
			t.Errorf("aceitou %s", bad)
		}
	}
	o = Op{{Retain: 1}}
	if err := json.Unmarshal([]byte(`null`), &o); err != nil || o != nil {
		t.Errorf("null: %v, %v", o, err)
	}
}
