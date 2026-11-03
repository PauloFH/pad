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

test('rebase preserva pendente após reinício', () => {
  const c = new SyncClient(5, 'abc');
  assert.deepStrictEqual(c.local([3, 'X']), { t: 'op', rev: 5, op: [3, 'X'] });
  assert.strictEqual(c.local([4, 'Y']), null);
  const m = c.rebase(0, 'abc!');
  for (const s of ['X', 'Y', '!']) assert.ok(c.text.includes(s), c.text);
  assert.strictEqual(m.rev, 0);
  assert.strictEqual(OT.apply('abc!', m.op), c.text);
  assert.strictEqual(c.buffer, null);
});

test('rebase sem pendente só adota o texto do servidor', () => {
  const c = new SyncClient(3, 'abc');
  assert.strictEqual(c.rebase(0, 'xyz'), null);
  assert.strictEqual(c.text, 'xyz');
  assert.ok(!c.pending());
});

test('reset descarta o pendente', () => {
  const c = new SyncClient(1, 'abc');
  c.local([3, 'X']);
  c.reset(7, 'zz');
  assert.strictEqual(c.text, 'zz');
  assert.strictEqual(c.rev, 7);
  assert.ok(!c.pending());
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

test('localIndex', () => {
  const c = new SyncClient(0, 'xyz');
  c.local(['ab', 3]);
  assert.strictEqual(c.localIndex(0), 2);
});
