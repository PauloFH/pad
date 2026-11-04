const test = require('node:test');
const assert = require('node:assert');
const { partes, quando } = require('./chat.js');

test('partes: texto, código e links', () => {
  assert.deepEqual(partes('oi\n```go\nx := 1\n```\nfim'),
    [{t:'texto', v:'oi\n'}, {t:'codigo', v:'x := 1\n'}, {t:'texto', v:'\nfim'}])
  assert.deepEqual(partes('veja @pad /ana/notas ok'),
    [{t:'texto', v:'veja '}, {t:'link', v:'@pad /ana/notas', href:'/ana/notas'}, {t:'texto', v:' ok'}])
  assert.deepEqual(partes('https://x.dev/a'), [{t:'link', v:'https://x.dev/a', href:'https://x.dev/a'}])
})
test('partes: HTML fica texto', () => {
  assert.deepEqual(partes('<script>alert(1)</script>'), [{t:'texto', v:'<script>alert(1)</script>'}])
})
test('partes: crases sem fechar ficam texto', () => {
  assert.deepEqual(partes('a ```b'), [{t:'texto', v:'a ```b'}])
})
test('partes: caminho de documento vira link, sem a pontuação do fim', () => {
  assert.deepEqual(partes('abri /ana/pasta/notas.'),
    [{t:'texto', v:'abri '}, {t:'link', v:'/ana/pasta/notas', href:'/ana/pasta/notas'}, {t:'texto', v:'.'}])
  assert.deepEqual(partes('veja (https://x.dev/a), ok'),
    [{t:'texto', v:'veja ('}, {t:'link', v:'https://x.dev/a', href:'https://x.dev/a'}, {t:'texto', v:'), ok'}])
})
test('partes: o que não é caminho de documento fica texto', () => {
  for (const s of ['e/ou', 'a/b/c', 'só /ana', 'javascript:alert(1)', 'x.dev/ab/cd']) {
    assert.deepEqual(partes(s), [{t:'texto', v:s}], s)
  }
})
test('partes: vazio não gera trechos', () => {
  assert.deepEqual(partes(''), [])
})
test('quando: dia/mês e hora, no fuso do navegador', () => {
  const d = new Date(2026, 10, 4, 14, 32, 5)
  assert.equal(quando(Math.floor(d.getTime() / 1000)), '04/11 14:32')
  assert.equal(quando(Math.floor(new Date(2026, 0, 9, 7, 5).getTime() / 1000)), '09/01 07:05')
})
