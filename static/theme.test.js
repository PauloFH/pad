const test = require('node:test');
const assert = require('node:assert');
const T = require('./theme.js');

test('resolver: sistema segue o claro/escuro, tema inválido também', () => {
  assert.strictEqual(T.resolver('sistema', true), 'grafite');
  assert.strictEqual(T.resolver('sistema', false), 'papel');
  assert.strictEqual(T.resolver('cafe', false), 'cafe');
  assert.strictEqual(T.resolver('xyz', true), 'grafite');
  assert.strictEqual(T.resolver(null, false), 'papel');
});

test('os 8 temas, com nome e tipo', () => {
  assert.deepStrictEqual(T.temas.map((t) => t.id), ['grafite', 'madrugada', 'cafe', 'floresta', 'papel', 'ceu', 'areia', 'contraste']);
  assert.deepStrictEqual(T.temas.map((t) => t.nome), ['Grafite', 'Madrugada', 'Café', 'Floresta', 'Papel', 'Céu', 'Areia', 'Alto contraste']);
  for (const t of T.temas) assert.ok(t.tipo === 'claro' || t.tipo === 'escuro', t.id);
});

test('escolha: guardada, inválida ou sem localStorage', () => {
  const antes = globalThis.localStorage;
  try {
    const guardado = {};
    globalThis.localStorage = { getItem: (k) => guardado[k] ?? null, setItem: (k, v) => { guardado[k] = String(v); } };
    assert.strictEqual(T.escolha(), 'sistema'); // primeira visita
    guardado['pad:tema'] = 'ceu';
    assert.strictEqual(T.escolha(), 'ceu');
    guardado['pad:tema'] = 'xyz';
    assert.strictEqual(T.escolha(), 'sistema');
    globalThis.localStorage = { getItem() { throw new Error('bloqueado'); }, setItem() { throw new Error('bloqueado'); } };
    assert.strictEqual(T.escolha(), 'sistema');
  } finally {
    globalThis.localStorage = antes;
  }
});

test('aplicar guarda a escolha e põe o tema no <html>, mesmo sem localStorage', () => {
  const antes = { ls: globalThis.localStorage, doc: globalThis.document, mm: globalThis.matchMedia };
  try {
    const guardado = {};
    globalThis.localStorage = { getItem: (k) => guardado[k] ?? null, setItem: (k, v) => { guardado[k] = String(v); } };
    globalThis.document = { documentElement: { dataset: {} } };
    globalThis.matchMedia = () => ({ matches: true });
    T.aplicar('areia');
    assert.strictEqual(guardado['pad:tema'], 'areia');
    assert.strictEqual(document.documentElement.dataset.theme, 'areia');
    T.aplicar('sistema');
    assert.strictEqual(guardado['pad:tema'], 'sistema');
    assert.strictEqual(document.documentElement.dataset.theme, 'grafite');
    globalThis.localStorage = { getItem() { throw new Error('bloqueado'); }, setItem() { throw new Error('bloqueado'); } };
    T.aplicar('cafe');
    assert.strictEqual(document.documentElement.dataset.theme, 'cafe');
  } finally {
    Object.assign(globalThis, { localStorage: antes.ls, document: antes.doc, matchMedia: antes.mm });
  }
});
