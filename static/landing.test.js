const test = require('node:test');
const assert = require('node:assert');
const { NOMES, sortear, pedacos, roteiro, secaoAtual } = require('./landing.js');

test('secaoAtual: a última que passou da linha, ou a primeira antes de todas', () => {
  assert.strictEqual(secaoAtual([120, 900, 1800], 97), 0);
  assert.strictEqual(secaoAtual([-800, 97, 900], 97), 1);
  assert.strictEqual(secaoAtual([-1800, -900, 40], 97), 2);
});

test('sortear devolve nomes diferentes da lista', () => {
  for (let i = 0; i < 50; i++) {
    const l = sortear(NOMES, 4);
    assert.strictEqual(new Set(l).size, 4);
    assert.ok(l.every((n) => NOMES.includes(n)));
  }
});

test('sortear depende do acaso e não mexe na lista original', () => {
  const antes = [...NOMES];
  assert.deepStrictEqual(sortear(NOMES, 8, () => 0.999), NOMES);
  assert.notDeepStrictEqual(sortear(NOMES, 8, () => 0), NOMES);
  assert.deepStrictEqual(NOMES, antes);
});

test('pedacos separa seleção, cursor e o que falta digitar', () => {
  assert.deepStrictEqual(pedacos('abcdef', 4, 2), [
    { tipo: 'texto', t: 'ab' }, { tipo: 'sel', t: 'cd' }, { tipo: 'cur', t: '' }, { tipo: 'texto', t: 'ef' },
  ]);
  assert.deepStrictEqual(pedacos('abcdef', 2, 2, 2), [
    { tipo: 'texto', t: 'ab' }, { tipo: 'cur', t: '' }, { tipo: 'oculto', t: 'cdef' },
  ]);
  assert.deepStrictEqual(pedacos('ab', 2), [{ tipo: 'texto', t: 'ab' }, { tipo: 'cur', t: '' }]);
});

test('o roteiro começa antes de digitar e termina com tudo escrito e selecionado', () => {
  const q = roteiro(30, 20, 50, 40);
  assert.deepStrictEqual([q[0].texto, q[0].titulo], [40, 20]);
  assert.deepStrictEqual([q.at(-1).texto, q.at(-1).titulo], [50, 30]);
  assert.strictEqual(q.length, 11 + 10);
});
