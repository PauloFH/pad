const test = require('node:test');
const assert = require('node:assert');
const OT = require('./ot.js');
const { lockedBy, shift } = require('./editor.js');

const id = (i) => i;
const bruno = { id: 'b', name: 'bruno', a: 5, h: 10, lock: true };

test('trava alheia barra só o que cai dentro dela', () => {
  const peers = [bruno];
  assert.strictEqual(lockedBy([7, 'x', 8], peers, 'a', id), 'bruno'); // insere em 7
  assert.strictEqual(lockedBy([5, 'x', 10], peers, 'a', id), null); // insere na borda
  assert.strictEqual(lockedBy([4, -1, 10], peers, 'a', id), null); // apaga [4,5)
  assert.strictEqual(lockedBy([4, -2, 9], peers, 'a', id), 'bruno'); // apaga [4,6)
});

test('a própria trava, seleção sem trava e seleção de trás para frente', () => {
  assert.strictEqual(lockedBy([7, 'x', 8], [bruno], 'b', id), null);
  assert.strictEqual(lockedBy([7, 'x', 8], [{ ...bruno, lock: false }], 'a', id), null);
  assert.strictEqual(lockedBy([7, 'x', 8], [{ ...bruno, a: 10, h: 5 }], 'a', id), 'bruno');
});

test('posições do servidor passam pelo mapa para o texto local', () => {
  const pend = (i) => i + 3; // três unidades pendentes antes da trava
  assert.strictEqual(lockedBy([7, 'x', 8], [bruno], 'a', pend), null); // 7 < 8 (início local)
  assert.strictEqual(lockedBy([9, 'x', 6], [bruno], 'a', pend), 'bruno');
});

test('insert próprio colado ao fim da trava fica fora dela, como no servidor', () => {
  const pendente = [10, 'x', 5]; // digitei "x" em 10, ainda sem ack
  const map = (i, stick) => OT.transformIndex(i, pendente, stick);
  assert.strictEqual(lockedBy([10, -1, 5], [bruno], 'a', map), null); // apaga o "x"
  assert.strictEqual(lockedBy([11, 'y', 5], [bruno], 'a', map), null); // digita depois do "x"
  assert.strictEqual(lockedBy([9, -1, 6], [bruno], 'a', map), 'bruno'); // apaga dentro
  // e colado ao início: o "x" em 5 fica antes da trava
  const antes = (i, stick) => OT.transformIndex(i, [5, 'x', 10], stick);
  assert.strictEqual(lockedBy([5, -1, 10], [bruno], 'a', antes), null);
});

test('shift: trava anda o início e segura o fim; seleção comum anda as duas pontas', () => {
  const peers = [{ ...bruno }, { ...bruno, h: 5, a: 10 }, { id: 'c', a: 5, h: 10, lock: false }];
  shift(peers, [5, 'x', 5, 'y', 5]); // inserts exatamente em 5 e em 10
  assert.deepStrictEqual(peers.map((p) => [p.a, p.h]), [[6, 11], [11, 6], [6, 12]]);
});
