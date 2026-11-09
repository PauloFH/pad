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
  const el = (extra) => {
    const at = {};
    return Object.assign({ textContent: '', hidden: true, addEventListener: (k, f) => (L[k] ||= []).push(f), setAttribute: (k, v) => { at[k] = String(v); }, getAttribute: (k) => at[k] ?? null }, extra);
  };
  const t = el({
    value: 'oi', selectionStart: 0, selectionEnd: 0, selectionDirection: 'none', scrollTop: 0, readOnly: false,
    dataset: { name: 'ana/doc', canEdit: 'true' },
    setSelectionRange(s, e, d) { Object.assign(this, { selectionStart: s, selectionEnd: e, selectionDirection: d || 'none' }); },
  });
  const st = el(), salvo = el(), trava = el(), fim = el({ firstChild: { textContent: '' } });
  Object.assign(globalThis, {
    window: globalThis,
    document: { getElementById: (id) => ({ t, st, salvo, trava, fim })[id], addEventListener() {}, dispatchEvent: (e) => events.push(e.detail ? [e.type, e.detail] : e.type), activeElement: t },
    CustomEvent: class { constructor(type, init) { this.type = type; this.detail = init?.detail; } },
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
    t, st, salvo, trava, fim, L, timers, sockets, events,
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
  assert.deepStrictEqual(p.ops().at(-1), { t: 'op', rev: 3, op: [3, '!'], seq: 1, a: 4, h: 4 });
  p.recv({ t: 'op', rev: 4, op: ['>', 3], by: 'p2' });
  assert.strictEqual(p.t.value, '>olá!');
  assert.strictEqual(p.t.selectionStart, 5);
  p.recv({ t: 'ack', rev: 5 });
  assert.strictEqual(window.pad.sync.pending(), false);
  assert.ok(p.events.includes('pad:text'));
  assert.deepStrictEqual(p.events.filter((e) => e[0] === 'pad:op'), [['pad:op', { by: 'p2' }]]); // presença: quem digitou
});

test('barra de status: salvando… até o ack, depois salvo', () => {
  const p = boot();
  // salvo/salvando fica num span sem aria-live; #st (polite no HTML) só leva a queda e as recusas
  const barra = () => [p.salvo.textContent, p.salvo.className, p.st.textContent];
  p.recv({ t: 'init', rev: 1, text: 'oi', me: 'p1', canEdit: true, seq: 0 });
  assert.deepStrictEqual(barra(), ['salvo', 'ok', '']);
  p.type('oi!');
  assert.deepStrictEqual(barra(), ['salvando…', '', '']);
  p.recv({ t: 'ack', rev: 2 });
  assert.deepStrictEqual(barra(), ['salvo', 'ok', '']);
  p.drop();
  assert.deepStrictEqual(barra(), ['', '', 'reconectando…']);
  assert.strictEqual(p.st.getAttribute('aria-live'), null); // o editor não mexe no aria-live
  // só leitura não salva nada: sem "salvo"
  p.tick();
  p.recv({ t: 'init', rev: 2, text: 'oi!', me: 'p1', canEdit: false, seq: 1 });
  assert.deepStrictEqual(barra(), ['', '', '']);
  // recusa: o motivo fica em #st, sem "salvo" ao lado
  p.drop();
  p.tick();
  p.recv({ t: 'init', rev: 2, text: 'oi!', me: 'p1', canEdit: true, seq: 1 });
  assert.deepStrictEqual(barra(), ['salvo', 'ok', '']);
  p.recv({ t: 'reject', reason: 'trecho travado' });
  assert.deepStrictEqual(barra(), ['', '', 'trecho travado']);
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
  assert.strictEqual(p.trava.textContent, 'documento passa de 2 MB');
  assert.strictEqual(p.trava.hidden, false);
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

test('fechamento 1009: o texto grande é descartado e não volta a ser enviado', () => {
  const p = boot();
  p.recv({ t: 'init', rev: 1, text: 'olá', me: 'p1', canEdit: true, seq: 0 });
  p.type('olá, texto grande');
  assert.strictEqual(p.ops().length, 1);
  p.drop(1009, 'read limited');
  p.tick();
  p.recv({ t: 'init', rev: 1, text: 'olá', me: 'p1', canEdit: true, seq: 0 });
  assert.strictEqual(p.ops().length, 0, 'não reenvia a op');
  assert.strictEqual(p.t.value, 'olá');
  assert.strictEqual(p.trava.textContent, 'texto grande demais para este documento: a colagem não foi salva');
  // fica à vista, sem prazo para sumir, até a próxima edição
  p.timers.splice(0).forEach((x) => x.f());
  assert.strictEqual(p.trava.hidden, false);
  p.type('olá!');
  assert.strictEqual(p.trava.hidden, true);
});

test('insert alheio na borda não entra na seleção (trava) própria', () => {
  const p = boot();
  p.recv({ t: 'init', rev: 1, text: 'abcdefghijklmno', me: 'p1', canEdit: true, seq: 0 });
  p.t.setSelectionRange(5, 10);
  p.recv({ t: 'op', rev: 2, op: [5, 'x', 10], by: 'p2' }); // no início: a seleção anda
  assert.deepStrictEqual([p.t.selectionStart, p.t.selectionEnd], [6, 11]);
  p.recv({ t: 'op', rev: 3, op: [11, 'y', 5], by: 'p2' }); // no fim: a seleção fica
  assert.deepStrictEqual([p.t.selectionStart, p.t.selectionEnd], [6, 11]);
});

test('rebase que falha: o init seguinte recomeça e avisa', () => {
  const p = boot();
  p.recv({ t: 'init', rev: 1, text: 'olá', me: 'p1', canEdit: true, seq: 0 });
  p.type('olá!');
  p.drop();
  p.tick();
  window.pad.sync.rebase = () => { throw new Error('fora de compasso'); };
  const err = console.error;
  console.error = () => {};
  p.recv({ t: 'init', rev: 2, text: 'Zolá', me: 'p1', canEdit: true, seq: 0 });
  console.error = err;
  p.drop();
  p.tick();
  p.recv({ t: 'init', rev: 2, text: 'Zolá', me: 'p1', canEdit: true, seq: 0 });
  assert.strictEqual(p.t.value, 'Zolá');
  assert.strictEqual(p.trava.textContent, 'não deu para reaplicar o que você digitou durante a queda');
});

// arrastar um trecho do próprio textarea: o navegador apaga a origem e insere no destino
function drag(p, from, value, to) {
  p.t.setSelectionRange(from[0], from[1]);
  p.L.dragstart?.[0]();
  const del = p.t.value.slice(0, from[0]) + p.t.value.slice(from[1]);
  p.L.beforeinput[0]({ inputType: 'deleteByDrag' });
  p.t.value = del;
  p.t.setSelectionRange(from[0], from[0]);
  p.L.input[0]();
  if (to != null) {
    p.L.beforeinput[0]({ inputType: 'insertFromDrop' });
    p.t.value = value;
    p.t.setSelectionRange(to, to);
    p.L.input[0]();
  }
  p.L.dragend?.[0]();
}

test('arrastar para dentro de uma trava alheia recusa o mover inteiro', () => {
  const p = boot();
  p.recv({ t: 'init', rev: 1, text: 'abcdef', me: 'p1', canEdit: true, seq: 0 });
  p.recv({ t: 'peers', peers: [{ id: 'p2', name: 'bruno', a: 4, h: 6, lock: true }] });
  drag(p, [0, 2], 'cdeabf', 5);
  assert.strictEqual(p.t.value, 'abcdef');
  assert.deepStrictEqual([p.t.selectionStart, p.t.selectionEnd], [0, 2]);
  assert.strictEqual(p.trava.textContent, 'trecho travado por bruno');
  assert.deepStrictEqual(p.ops(), []);
  // fora da trava o mover sai numa op só
  drag(p, [0, 2], 'cabdef', 3);
  assert.strictEqual(p.ops().length, 1);
  assert.strictEqual(window.pad.sync.text, 'cabdef');
  // arrastado para fora do textarea: só a origem some, no dragend
  p.recv({ t: 'ack', rev: 2 });
  drag(p, [0, 1], null, null);
  assert.strictEqual(p.ops().length, 2);
  assert.strictEqual(window.pad.sync.text, 'abdef');
});

test('mover arrastando manda as duas metades compostas, não o trecho entre elas', () => {
  const p = boot();
  p.recv({ t: 'init', rev: 1, text: 'ab0123456789', me: 'p1', canEdit: true, seq: 0 });
  p.recv({ t: 'peers', peers: [
    { id: 'p2', name: 'bruno', a: 6, h: 6, lock: false },
    { id: 'p3', name: 'caio', a: 4, h: 6, lock: true }, // trava no caminho, não no destino
  ] });
  drag(p, [0, 2], '0123456789ab', 12);
  assert.strictEqual(p.trava.hidden, true);
  assert.deepStrictEqual(p.ops().map((m) => m.op), [[-2, 10, 'ab']]);
  p.recv({ t: 'ack', rev: 2 });
  assert.deepStrictEqual([window.pad.peers[0].a, window.pad.peers[0].h], [4, 4]); // o cursor do bruno não pula
  assert.strictEqual(window.pad.sync.text, '0123456789ab');
});

test('sem dragend, a próxima tecla encerra o mover e envia o que ficou', () => {
  const p = boot();
  p.recv({ t: 'init', rev: 1, text: 'abcdef', me: 'p1', canEdit: true, seq: 0 });
  p.t.setSelectionRange(0, 2);
  p.L.dragstart[0]();
  p.L.beforeinput[0]({ inputType: 'deleteByDrag' });
  p.t.value = 'cdef';
  p.t.setSelectionRange(0, 0);
  p.L.input[0]();
  p.L.keydown[0]({ key: 'x' });
  assert.deepStrictEqual(p.ops().map((m) => m.op), [[-2, 4]]);
  p.recv({ t: 'ack', rev: 2 });
  p.type('cdefx');
  assert.deepStrictEqual(p.ops().at(-1).op, [4, 'x']);
});
