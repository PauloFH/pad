const test = require('node:test');
const assert = require('node:assert');
const P = require('./presence.js');

test('linha de um índice UTF-16', () => {
  const st = P.lineStarts('ab\n😀c\n\nfim');
  assert.deepStrictEqual(st, [0, 3, 7, 8]);
  assert.strictEqual(P.lineAt(st, 0), 0);
  assert.strictEqual(P.lineAt(st, 2), 0); // antes do \n
  assert.strictEqual(P.lineAt(st, 3), 1);
  assert.strictEqual(P.lineAt(st, 6), 1); // o emoji conta 2
  assert.strictEqual(P.lineAt(st, 7), 2); // linha vazia
  assert.strictEqual(P.lineAt(st, 11), 3);
  assert.deepStrictEqual(P.lineStarts(''), [0]);
});

test('estado de cada pessoa no menu', () => {
  const st = P.lineStarts('l1\nl2\nl3\nl4\nl5');
  const p = { a: 4, h: 4, reading: false };
  assert.strictEqual(P.state(p, st, 0, 10000), 'linha 2');
  assert.strictEqual(P.state(p, st, 8000, 10000), 'digitando na linha 2'); // op há 2 s
  assert.strictEqual(P.state(p, st, 7000, 10000), 'linha 2'); // há 3 s já não conta
  assert.strictEqual(P.state({ a: 13, h: 10 }, st, 0, 10000), 'selecionou as linhas 4–5');
  assert.strictEqual(P.state({ a: 9, h: 10 }, st, 0, 10000), 'selecionou a linha 4');
  assert.strictEqual(P.state({ a: 12, h: 9 }, st, 0, 10000), 'selecionou a linha 4'); // a quebra no fim não puxa a linha 5
  assert.deepStrictEqual(P.span({ a: 12, h: 9 }, st), [3, 3]);
  assert.deepStrictEqual(P.span({ a: 4, h: 4 }, st), [1, 1]);
  assert.strictEqual(P.state({ a: 0, h: 0, reading: true }, st, 0, 10000), 'lendo');
  assert.strictEqual(P.state({ a: 0, h: 2, reading: true }, st, 0, 10000), 'selecionou a linha 1');
  assert.strictEqual(P.state({ a: 13, h: 10 }, st, 9500, 10000), 'digitando na linha 4'); // digitar vence a seleção
});

test('bolinhas: uma por key, a própria por último', () => {
  const peers = [
    { id: 'me', key: 'u:paulo', name: 'paulo', color: '#ea580c' },
    { id: 'a1', key: 'u:ana', name: 'ana', color: '#16a34a' },
    { id: 'b1', key: 'v:x', name: 'bruno', color: '#7c3aed' },
    { id: 'a2', key: 'u:ana', name: 'ana', color: '#16a34a' },
    { id: 'me2', key: 'u:paulo', name: 'paulo', color: '#ea580c' },
  ];
  const g = P.groups(peers, 'me');
  assert.deepStrictEqual(g.map((x) => [x.key, x.initial, x.mine, x.tabs.map((t) => t.id)]), [
    ['u:ana', 'A', false, ['a1', 'a2']],
    ['v:x', 'B', false, ['b1']],
    ['u:paulo', 'P', true, ['me', 'me2']],
  ]);
  assert.strictEqual(P.groups([{ id: 'x', key: 'k', name: '😀ze', color: '' }], 'me')[0].initial, '😀');
});

test('a aba que representa a pessoa é a da op mais recente', () => {
  const tabs = [{ id: 'a1' }, { id: 'a2' }];
  assert.strictEqual(P.pick(tabs, {}).id, 'a1');
  assert.strictEqual(P.pick(tabs, { a1: 5, a2: 9 }).id, 'a2');
  assert.strictEqual(P.pick(tabs, { a1: 5 }).id, 'a1');
});

test('acompanhar para com rolagem ou digitação do próprio usuário', () => {
  assert.ok(P.stopsFollow({ type: 'input' }));
  assert.ok(P.stopsFollow({ type: 'wheel' }));
  for (const key of ['PageUp', 'PageDown', 'ArrowUp', 'ArrowDown', 'Home', 'End']) assert.ok(P.stopsFollow({ type: 'keydown', key }), key);
  for (const key of ['a', 'Shift', 'Control', 'Tab']) assert.ok(!P.stopsFollow({ type: 'keydown', key }), key);
});

test('linhas trocadas entre dois textos', () => {
  assert.deepStrictEqual(P.changed(['a', 'b', 'c'], ['a', 'x', 'c']), [1, 2, 2]);
  assert.deepStrictEqual(P.changed(['a', 'c'], ['a', 'b', 'c']), [1, 1, 2]); // linha nova no meio
  assert.deepStrictEqual(P.changed(['a', 'b'], ['a', 'b']), [2, 2, 2]);
  assert.deepStrictEqual(P.changed(['x', 'x'], ['x']), [1, 2, 1]); // prefixo e sufixo não se cruzam
  assert.deepStrictEqual(P.changed([], ['a']), [0, 0, 1]);
});
