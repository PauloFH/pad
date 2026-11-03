const test = require('node:test');
const assert = require('node:assert');
const { lockedBy } = require('./editor.js');

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
