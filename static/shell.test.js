const test = require('node:test');
const assert = require('node:assert');
const S = require('./shell.js');

// localStorage em memória; devolve o objeto guardado para conferir as chaves
function memoria() {
  const guardado = {};
  globalThis.localStorage = { getItem: (k) => guardado[k] ?? null, setItem: (k, v) => { guardado[k] = String(v); } };
  return guardado;
}

test('abas: a atual no começo, sem duplicatas, no máximo 8', () => {
  memoria();
  for (const n of ['a', 'b', 'c', 'd', 'e', 'f', 'g', 'h', 'i']) S.abas.abrir('ana', 'ana/' + n);
  assert.deepStrictEqual(S.abas.ler('ana'), ['ana/i', 'ana/h', 'ana/g', 'ana/f', 'ana/e', 'ana/d', 'ana/c', 'ana/b']);
  assert.deepStrictEqual(S.abas.abrir('ana', 'ana/e').slice(0, 3), ['ana/e', 'ana/i', 'ana/h']);
  assert.strictEqual(S.abas.ler('ana').length, 8);
  assert.strictEqual(S.abas.ler('ana').filter((n) => n === 'ana/e').length, 1);
});

test('abas: fechar tira da lista', () => {
  memoria();
  S.abas.abrir('ana', 'ana/a');
  S.abas.abrir('ana', 'ana/b');
  assert.deepStrictEqual(S.abas.fechar('ana', 'ana/a'), ['ana/b']);
  assert.deepStrictEqual(S.abas.ler('ana'), ['ana/b']);
});

test('abas, coluna e pastas de usuários diferentes não se misturam', () => {
  const guardado = memoria();
  S.abas.abrir('ana', 'ana/a');
  S.abas.abrir('bia', 'bia/z');
  assert.deepStrictEqual(S.abas.ler('ana'), ['ana/a']);
  assert.deepStrictEqual(S.abas.ler('bia'), ['bia/z']);
  assert.strictEqual(S.coluna.aberta('ana'), true); // padrão: aberta
  S.coluna.alternar('ana', true);
  assert.strictEqual(S.coluna.aberta('ana'), false);
  assert.strictEqual(S.coluna.aberta('bia'), true);
  S.pastas.gravar('ana', ['ana/p']);
  assert.deepStrictEqual(S.pastas.ler('ana'), ['ana/p']);
  assert.deepStrictEqual(S.pastas.ler('bia'), []);
  assert.deepStrictEqual(Object.keys(guardado).sort(), ['pad:abas:ana', 'pad:abas:bia', 'pad:col:ana', 'pad:pastas:ana']);
});

test('localStorage que lança exceção: listas vazias, sem lançar', () => {
  globalThis.localStorage = { getItem() { throw new Error('bloqueado'); }, setItem() { throw new Error('bloqueado'); } };
  assert.deepStrictEqual(S.abas.ler('ana'), []);
  assert.deepStrictEqual(S.abas.abrir('ana', 'ana/a'), ['ana/a']);
  assert.deepStrictEqual(S.abas.fechar('ana', 'ana/a'), []);
  assert.deepStrictEqual(S.pastas.ler('ana'), []);
  assert.strictEqual(S.coluna.aberta('ana'), true);
  assert.strictEqual(S.coluna.alternar('ana', true), false); // vale na página, mesmo sem guardar
  // lixo guardado também vira lista vazia
  memoria()['pad:abas:ana'] = '{"x":1}';
  assert.deepStrictEqual(S.abas.ler('ana'), []);
});

test('linha e coluna do cursor, a partir de 1', () => {
  assert.deepStrictEqual(S.linhaColuna('', 0), [1, 1]);
  assert.deepStrictEqual(S.linhaColuna('ab\ncd', 2), [1, 3]);
  assert.deepStrictEqual(S.linhaColuna('ab\ncd', 3), [2, 1]);
  assert.deepStrictEqual(S.linhaColuna('ab\ncd', 5), [2, 3]);
});

test('quem está editando, para o visitante', () => {
  const p = (id, key, name, reading) => ({ id, key, name, reading });
  assert.strictEqual(S.editando([p('1', 'v', 'gato', true)], '1'), '');
  assert.strictEqual(S.editando([p('1', 'v', 'gato', true), p('2', 'u:ana', 'ana', false), p('3', 'u:ana', 'ana', false)], '1'), 'Ana está editando agora');
  assert.strictEqual(S.editando([p('2', 'u:ana', 'ana', false), p('4', 'u:bia', 'bia', false)], '1'), 'Ana e Bia estão editando agora');
  assert.strictEqual(S.editando([p('2', 'a', 'ana', false), p('4', 'b', 'bia', false), p('5', 'c', 'caio', false)], '1'), '3 pessoas editando agora');
});
