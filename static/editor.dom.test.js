// editor.js com DOM e WebSocket falsos: o fluxo de mensagens sem navegador.
const test = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const vm = require('node:vm');
require('./ot.js');
require('./sync.js');
const src = fs.readFileSync(__dirname + '/editor.js', 'utf8');

function boot() {
  const L = {}, timers = [], sockets = [], events = [];
  const el = (extra) => Object.assign({ textContent: '', hidden: true, addEventListener: (k, f) => (L[k] ||= []).push(f) }, extra);
  const t = el({
    value: 'oi', selectionStart: 0, selectionEnd: 0, selectionDirection: 'none', scrollTop: 0, readOnly: false,
    dataset: { name: 'ana/doc', canEdit: 'true' },
    setSelectionRange(s, e, d) { Object.assign(this, { selectionStart: s, selectionEnd: e, selectionDirection: d || 'none' }); },
  });
  const st = el(), trava = el(), fim = el({ firstChild: { textContent: '' } });
  Object.assign(globalThis, {
    window: globalThis,
    document: { getElementById: (id) => ({ t, st, trava, fim })[id], addEventListener() {}, dispatchEvent: (e) => events.push(e.type), activeElement: t },
    CustomEvent: class { constructor(type) { this.type = type; } },
    location: { protocol: 'http:', host: 'h' },
    addEventListener() {},
    setTimeout: (f, ms) => timers.push({ f, ms }),
    clearTimeout() {},
    WebSocket: class {
      static OPEN = 1;
      constructor(url) { Object.assign(this, { url, readyState: 1, sent: [] }); sockets.push(this); }
      send(s) { this.sent.push(JSON.parse(s)); }
      close() {}
    },
  });
  vm.runInThisContext(src); // sem `module`, como no navegador
  const ws = () => sockets.at(-1);
  return {
    t, st, trava, fim, L, timers, sockets, events,
    ws,
    recv: (m) => ws().onmessage({ data: JSON.stringify(m) }),
    drop: (code = 1006, reason = '') => { ws().readyState = 3; ws().onclose({ code, reason }); },
    tick: () => timers.splice(0).forEach((x) => x.f()),
    type: (value, cursor = value.length) => {
      L.beforeinput[0]({ inputType: 'insertText' });
      t.value = value;
      t.setSelectionRange(cursor, cursor);
      L.input[0]();
    },
    ops: () => ws().sent.filter((m) => m.t === 'op'),
  };
}

test('init, op local, remota com pendente e ack', () => {
  const p = boot();
  assert.match(p.ws().url, /^ws:\/\/h\/_ws\/ana\/doc\?c=[0-9a-f]{24}$/);
  assert.strictEqual(p.t.readOnly, true);
  assert.strictEqual(p.st.textContent, 'reconectando…');
  p.recv({ t: 'init', rev: 3, text: 'olá', me: 'p1', canEdit: true, seq: 0, visitor: 'tok' });
  assert.strictEqual(p.t.value, 'olá');
  assert.strictEqual(p.t.readOnly, false);
  p.type('olá!');
  assert.deepStrictEqual(p.ops().at(-1), { t: 'op', rev: 3, op: [3, '!'], cid: p.ws().url.slice(-24), seq: 1, a: 4, h: 4 });
  p.recv({ t: 'op', rev: 4, op: ['>', 3], by: 'p2' });
  assert.strictEqual(p.t.value, '>olá!');
  assert.strictEqual(p.t.selectionStart, 5);
  p.recv({ t: 'ack', rev: 5 });
  assert.strictEqual(window.pad.sync.pending(), false);
  assert.ok(p.events.includes('pad:text'));
});

test('trava alheia barra o input; Ctrl+Z desfaz só o próprio', () => {
  const p = boot();
  p.recv({ t: 'init', rev: 1, text: 'olá', me: 'p1', canEdit: true, seq: 0 });
  p.type('olá!');
  p.recv({ t: 'op', rev: 2, op: ['>', 3], by: 'p2' });
  p.recv({ t: 'ack', rev: 3 });
  p.recv({ t: 'peers', peers: [{ id: 'p2', name: 'bruno', a: 1, h: 3, lock: true }] });
  p.t.setSelectionRange(2, 2);
  p.type('>oXlá!', 3);
  assert.strictEqual(p.t.value, '>olá!');
  assert.strictEqual(p.t.selectionStart, 2);
  assert.strictEqual(p.trava.textContent, 'trecho travado por bruno');
  let prevented = false;
  p.L.keydown[0]({ ctrlKey: true, key: 'z', preventDefault: () => (prevented = true) });
  assert.ok(prevented);
  assert.strictEqual(p.t.value, '>olá');
  // o init de uma reconexão mantém os peers até chegar o próximo peers
  p.drop();
  p.tick();
  p.recv({ t: 'init', rev: 4, text: '>olá', me: 'p1', canEdit: true, seq: 2 });
  p.t.setSelectionRange(2, 2);
  p.type('>oXlá', 3);
  assert.strictEqual(p.t.value, '>olá');
});

test('composição segura as mensagens até o fim', () => {
  const p = boot();
  p.recv({ t: 'init', rev: 1, text: 'olá', me: 'p1', canEdit: true, seq: 0 });
  p.L.compositionstart[0]();
  p.t.value = 'oláã';
  p.t.setSelectionRange(4, 4);
  p.recv({ t: 'op', rev: 2, op: [3, 'Z'], by: 'p2' });
  assert.strictEqual(p.t.value, 'oláã');
  p.L.compositionend[0]();
  assert.strictEqual(p.t.value, 'oláãZ');
  assert.strictEqual(window.pad.sync.text, 'oláãZ');
});

test('queda no meio de uma composição não perde o texto composto', () => {
  const p = boot();
  p.recv({ t: 'init', rev: 1, text: 'ab', me: 'p1', canEdit: true, seq: 0 });
  p.L.compositionstart[0]();
  p.t.value = 'abã';
  p.t.setSelectionRange(3, 3);
  p.drop();
  p.L.compositionend[0]();
  p.tick();
  assert.strictEqual(p.sockets.length, 2);
  p.recv({ t: 'init', rev: 2, text: 'Zab', me: 'p1', canEdit: true, seq: 0 }); // alguém digitou enquanto caía
  assert.strictEqual(p.t.value, 'Zabã');
  assert.deepStrictEqual(p.ops().map((m) => m.op), [[3, 'ã']]);
  p.recv({ t: 'ack', rev: 3 });
  p.type('Zabãc');
  assert.deepStrictEqual(p.ops().at(-1).op, [4, 'c']);
  assert.strictEqual(window.pad.sync.text, 'Zabãc');
});

test('reject, reconexão e fechamento 4000', () => {
  const p = boot();
  p.recv({ t: 'init', rev: 1, text: 'olá', me: 'p1', canEdit: true, seq: 0, visitor: 'tok' });
  p.type('olá!');
  p.recv({ t: 'reject', reason: 'documento passa de 2 MB' });
  p.recv({ t: 'init', rev: 1, text: 'olá', me: 'p1', canEdit: true, seq: 0 });
  assert.strictEqual(p.t.value, 'olá');
  assert.strictEqual(p.st.textContent, 'documento passa de 2 MB');
  assert.strictEqual(window.pad.undo.peekUndo(), null);
  const first = p.ws().url;
  p.drop();
  assert.strictEqual(p.timers.at(-1).ms, 500);
  p.tick();
  assert.strictEqual(p.ws().url, first + '&v=tok');
  p.drop();
  assert.strictEqual(p.timers.at(-1).ms, 1000);
  p.tick();
  p.drop(4000, 'acesso removido');
  assert.strictEqual(p.timers.length, 0);
  assert.strictEqual(p.fim.hidden, false);
  assert.strictEqual(p.fim.firstChild.textContent, 'acesso removido ');
});
