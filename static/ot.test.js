const test = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const path = require('node:path');
const OT = require('./ot.js');

const vec = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'testdata', 'ot-vectors.json'), 'utf8'));

// mesma lógica de convergência do Go: apply(apply(d,a),b') == apply(apply(d,b),a')
function converge(doc, a, b) {
  const [a2, b2] = OT.transform(a, b);
  const x = OT.apply(OT.apply(doc, a), b2);
  const y = OT.apply(OT.apply(doc, b), a2);
  assert.strictEqual(x, y, `divergiu em ${JSON.stringify(doc)} a=${JSON.stringify(a)} b=${JSON.stringify(b)}`);
  return x;
}

// pseudo-aleatório determinístico (mulberry32)
function rng(seed) {
  return () => {
    seed = (seed + 0x6d2b79f5) | 0;
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}
const pick = (r, xs) => xs[Math.floor(r() * xs.length)];
const isHigh = (c) => c >= 0xd800 && c < 0xdc00;

function randomDoc(r) {
  let s = '';
  for (let n = Math.floor(r() * 12); n > 0; n--) s += pick(r, ['a', 'ç', '😀']);
  return s;
}

// operação válida sobre doc, sem cortar pares substitutos
function randomOp(r, doc) {
  const o = [];
  const add = (c) => {
    const l = o.length - 1;
    if (l >= 0 && typeof c === 'number' && typeof o[l] === 'number' && (c > 0) === (o[l] > 0)) o[l] += c;
    else if (l >= 0 && typeof c === 'string' && typeof o[l] === 'string') o[l] += c;
    else o.push(c);
  };
  for (let i = 0; i < doc.length; ) {
    const n = isHigh(doc.charCodeAt(i)) ? 2 : 1;
    const k = Math.floor(r() * 5);
    if (k === 0) { add(pick(r, ['a', 'ç', '😀', 'aç', '-1', '5'])); continue; }
    add(k === 1 ? -n : n);
    i += n;
  }
  if (r() < 1 / 3) add('😀');
  return o;
}

test('vetores: apply', () => {
  for (const c of vec.apply) assert.strictEqual(OT.apply(c.doc, c.op), c.want);
});

test('vetores: applyErr lança', () => {
  for (const c of vec.applyErr) assert.throws(() => OT.apply(c.doc, c.op), `${c.doc} ${JSON.stringify(c.op)}`);
});

test('vetores: transform converge', () => {
  for (const c of vec.transform) assert.strictEqual(converge(c.doc, c.a, c.b), c.want);
});

test('transform aleatório converge e recusa bases diferentes', () => {
  const r = rng(42);
  for (let i = 0; i < 2000; i++) {
    const doc = randomDoc(r);
    converge(doc, randomOp(r, doc), randomOp(r, doc));
  }
  assert.throws(() => OT.transform([1], [2]));
});

test('transform: empate de insert, a vem primeiro', () => {
  assert.strictEqual(converge('abc', [1, 'X', 2], [1, 'Y', 2]), 'aXYbc');
});

test('compose', () => {
  const r = rng(7);
  for (let i = 0; i < 1000; i++) {
    const t = randomDoc(r);
    const a = randomOp(r, t);
    const mid = OT.apply(t, a);
    const b = randomOp(r, mid);
    assert.strictEqual(OT.apply(t, OT.compose(a, b)), OT.apply(mid, b), `${JSON.stringify([t, a, b])}`);
  }
});

test('invert', () => {
  const r = rng(9);
  for (let i = 0; i < 1000; i++) {
    const t = randomDoc(r);
    const op = randomOp(r, t);
    assert.strictEqual(OT.apply(OT.apply(t, op), OT.invert(op, t)), t);
  }
});

test('diff', () => {
  assert.deepStrictEqual(OT.diff('aa', 'aaa', 1), ['a', 2]);
  assert.deepStrictEqual(OT.diff('aa', 'aaa', 3), [2, 'a']);
  assert.deepStrictEqual(OT.diff('abc', 'abc', 1), [3]);
  assert.deepStrictEqual(OT.diff('aXc', 'ac', 1), [1, -1, 1]);
  assert.deepStrictEqual(OT.diff('', '', 0), []);
});

test('diff nunca corta par substituto e reproduz o texto', () => {
  const r = rng(11);
  for (let i = 0; i < 2000; i++) {
    const a = randomDoc(r), b = randomDoc(r);
    assert.strictEqual(OT.apply(a, OT.diff(a, b, Math.floor(r() * (b.length + 1)))), b);
  }
  // emoji trocado por outro com a mesma unidade alta
  assert.strictEqual(OT.apply('😀', OT.diff('😀', '😁', 2)), '😁');
  assert.strictEqual(OT.apply('😀x', OT.diff('😀x', '😁x', 1)), '😁x');
});

test('isNoop', () => {
  assert.ok(OT.isNoop([]));
  assert.ok(OT.isNoop([5]));
  assert.ok(!OT.isNoop([5, 'x']));
  assert.ok(!OT.isNoop([-1]));
});

test('transformIndex', () => {
  const ins = [2, 'xy', 1];
  assert.strictEqual(OT.transformIndex(2, ins, true), 4);
  assert.strictEqual(OT.transformIndex(2, ins, false), 2);
  const del = [1, -3, 1];
  assert.strictEqual(OT.transformIndex(2, del, true), 1);
  assert.strictEqual(OT.transformIndex(5, del, true), 2);
});

test('touchesRange', () => {
  const cases = [
    [[5, 'x', 7], false],
    [[7, 'x', 5], true],
    [[10, 'x', 2], false],
    [[4, -1, 7], false],
    [[9, -2, 1], true],
  ];
  for (const [op, want] of cases) assert.strictEqual(OT.touchesRange(op, 5, 10), want, JSON.stringify(op));
});

test('compose: insert que parece número não vira delete', () => {
  assert.deepStrictEqual(OT.compose(['-1'], [2]), ['-1']);
  assert.deepStrictEqual(OT.compose(['-1', 1], [3]), ['-1', 1]);
});
