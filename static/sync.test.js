const test = require('node:test');
const assert = require('node:assert');
const OT = require('./ot.js');
const { SyncClient, UndoStack } = require('./sync.js');

// pseudo-aleatório determinístico (mulberry32)
function rng(seed) {
  return () => {
    seed = (seed + 0x6d2b79f5) | 0;
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}
const int = (r, n) => Math.floor(r() * n);

// edição aleatória em fronteira de code point, para não partir o 😀
function randomOp(r, text) {
  const cps = Array.from(text);
  const i = int(r, cps.length + 1), k = int(r, Math.min(3, cps.length - i) + 1);
  let ins = '';
  for (let n = int(r, 3); n > 0; n--) ins += ['a', 'ç', '😀'][int(r, 3)];
  const before = cps.slice(0, i).join('') + ins;
  return OT.diff(text, before + cps.slice(i + k).join(''), before.length);
}

test('dois clientes e um servidor simulado convergem', () => {
  for (let seed = 1; seed <= 20; seed++) {
    const r = rng(seed);
    const server = { text: 'olá 😀', log: [] };
    const clients = [new SyncClient(0, server.text), new SyncClient(0, server.text)];
    const up = [[], []], down = [[], []]; // canais FIFO cliente→servidor e servidor→cliente
    const made = [0, 0];

    // o servidor transforma a operação contra o log posterior a rev, com ela como `a`
    const receive = (from, msg) => {
      let op = msg.op;
      for (const s of server.log.slice(msg.rev)) [op] = OT.transform(op, s);
      server.text = OT.apply(server.text, op);
      server.log.push(op);
      const rev = server.log.length;
      down[from].push({ t: 'ack', rev });
      down[1 - from].push({ t: 'op', rev, op });
    };
    const deliver = (to, msg) => {
      const c = clients[to];
      if (msg.t === 'ack') {
        const m = c.ack(msg.rev);
        if (m) up[to].push(m);
      } else {
        const before = c.text;
        const op = c.remote(msg.rev, msg.op);
        assert.strictEqual(c.text, OT.apply(before, op));
      }
    };

    for (;;) {
      const moves = [];
      for (const i of [0, 1]) {
        if (made[i] < 200) moves.push(() => {
          const op = randomOp(r, clients[i].text);
          if (OT.isNoop(op)) return;
          made[i]++;
          const m = clients[i].local(op);
          if (m) up[i].push(m);
        });
        if (up[i].length) moves.push(() => receive(i, up[i].shift()));
        if (down[i].length) moves.push(() => deliver(i, down[i].shift()));
      }
      if (!moves.length) break;
      moves[int(r, moves.length)]();
    }
    assert.strictEqual(clients[0].text, server.text, `seed ${seed}`);
    assert.strictEqual(clients[1].text, server.text, `seed ${seed}`);
    assert.ok(!clients[0].pending() && !clients[1].pending());
  }
});

test('local e ack numeram as operações por cid', () => {
  const c = new SyncClient(0, '', 'aba1');
  assert.deepStrictEqual(c.local(['a']), { t: 'op', rev: 0, op: ['a'], cid: 'aba1', seq: 1 });
  assert.strictEqual(c.local([1, 'b']), null);
  assert.deepStrictEqual(c.ack(1), { t: 'op', rev: 1, op: [1, 'b'], cid: 'aba1', seq: 2 });
  assert.strictEqual(c.ack(2), null);
  const outro = new SyncClient(0, '');
  assert.ok(outro.cid && outro.cid !== new SyncClient(0, '').cid);
});

test('rebase preserva pendente após reinício', () => {
  const c = new SyncClient(5, 'abc', 'aba1');
  assert.deepStrictEqual(c.local([3, 'X']), { t: 'op', rev: 5, op: [3, 'X'], cid: 'aba1', seq: 1 });
  assert.strictEqual(c.local([4, 'Y']), null);
  const old = c.text;
  const { msg, op } = c.rebase(0, 'abc!', 0);
  for (const s of ['X', 'Y', '!']) assert.ok(c.text.includes(s), c.text);
  assert.strictEqual(msg.rev, 0);
  assert.strictEqual(msg.seq, 1); // o servidor nunca viu a seq 1: reenvia com a mesma
  assert.strictEqual(OT.apply('abc!', msg.op), c.text);
  assert.strictEqual(OT.apply(old, op), c.text);
  assert.strictEqual(c.buffer, null);
});

test('rebase não duplica inflight já aplicado', () => {
  const c = new SyncClient(5, 'abc', 'aba1');
  c.local([3, 'X']);
  const r = c.rebase(9, 'abcX', 1);
  assert.strictEqual(c.text, 'abcX');
  assert.ok(!c.pending());
  assert.deepStrictEqual(r, { msg: null, op: null });

  // com buffer: só ele vai, como operação nova
  const d = new SyncClient(5, 'abc', 'aba1');
  d.local([3, 'X']);
  d.local([4, 'Y']);
  const { msg, op } = d.rebase(9, 'abcX', 1);
  assert.strictEqual(d.text, 'abcXY');
  assert.deepStrictEqual(msg, { t: 'op', rev: 9, op: [4, 'Y'], cid: 'aba1', seq: 2 });
  assert.strictEqual(op, null);
});

test('rebase usa o sufixo comum', () => {
  const c = new SyncClient(0, 'abc');
  c.local([2, 'X', 1]);
  const old = c.text;
  const { op } = c.rebase(0, 'Zabc', 0);
  assert.strictEqual(c.text, 'ZabXc');
  assert.strictEqual(OT.apply(old, op), 'ZabXc');
});

test('rebase sem pendente só adota o texto do servidor', () => {
  const c = new SyncClient(3, 'abc');
  const { msg, op } = c.rebase(0, 'xyz', 0);
  assert.strictEqual(msg, null);
  assert.strictEqual(c.text, 'xyz');
  assert.strictEqual(OT.apply('abc', op), 'xyz');
  assert.ok(!c.pending());
});

test('reset descarta o pendente', () => {
  const c = new SyncClient(1, 'abc');
  c.local([3, 'X']);
  const op = c.reset(7, 'zz');
  assert.strictEqual(OT.apply('abcX', op), 'zz');
  assert.strictEqual(c.text, 'zz');
  assert.strictEqual(c.rev, 7);
  assert.ok(!c.pending());
  assert.strictEqual(c.reset(8, 'zz'), null);
});

test('desfazer só o meu', () => {
  const c = new SyncClient(0, '');
  const u = new UndoStack();
  const before = c.text;
  c.local(['oi']);
  u.record(['oi'], before, 0);
  c.ack(1);
  u.transform(c.remote(2, [2, ' ana']));
  assert.strictEqual(c.text, 'oi ana');
  assert.deepStrictEqual(u.peekUndo(), u.peekUndo()); // não remove
  const op = u.undo(c.text);
  assert.strictEqual(OT.apply(c.text, op), ' ana');
  // refazer devolve o "oi"
  const t = OT.apply(c.text, op);
  assert.strictEqual(OT.apply(t, u.redo(t)), 'oi ana');
});

test('agrupa digitação', () => {
  const u = new UndoStack();
  let text = '';
  const type = (s, now) => {
    const op = [text.length, s].filter((c) => c !== 0);
    u.record(op, text, now);
    text = OT.apply(text, op);
  };
  type('a', 0); type('b', 100); type('c', 200);
  type('d', 2200);
  text = OT.apply(text, u.undo(text));
  assert.strictEqual(text, 'abc');
  text = OT.apply(text, u.undo(text));
  assert.strictEqual(text, '');
  assert.strictEqual(u.undo(text), null);
});

test('novo record limpa o refazer', () => {
  const u = new UndoStack();
  u.record(['a'], '', 0);
  u.undo('a');
  assert.ok(u.peekRedo());
  u.record(['b'], '', 5000);
  assert.strictEqual(u.peekRedo(), null);
});

test('remota quebra o agrupamento', () => {
  const u = new UndoStack();
  u.record(['a'], '', 0);
  u.record([1, 'b'], 'a', 5000);
  u.transform([1, -1]); // outro apagou o "b"
  u.record([1, 'c'], 'a', 5100);
  assert.strictEqual(OT.apply('ac', u.undo('ac')), 'a');
});

test('clear esvazia as pilhas', () => {
  const u = new UndoStack();
  u.record(['a'], '', 0);
  u.undo('a');
  u.record(['b'], '', 5000);
  u.clear();
  assert.strictEqual(u.peekUndo(), null);
  assert.strictEqual(u.peekRedo(), null);
});

test('localIndex', () => {
  const c = new SyncClient(0, 'xyz');
  c.local(['ab', 3]);
  assert.strictEqual(c.localIndex(0), 2);
  assert.strictEqual(c.localIndex(0, false), 0); // fim de trava: insert na borda fica fora
});
