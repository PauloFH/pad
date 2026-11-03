// Cliente de sincronização no estilo ot.js: uma operação em voo (inflight)
// esperando ack e o resto acumulado no buffer. O desfazer é por usuário:
// só as operações locais entram, transformadas pelas remotas.
(function () {
  const OT = typeof module !== 'undefined' ? require('./ot.js') : globalThis.PadOT;

  // compose tolerando null (sem operação)
  const join = (a, b) => (a && b ? OT.compose(a, b) : a || b);

  class SyncClient {
    constructor(rev, text) { this.reset(rev, text); }

    reset(rev, text) {
      this.rev = rev;
      this.text = text;
      this.serverText = text; // texto na revisão rev, sem o pendente
      this.inflight = null;
      this.buffer = null;
    }

    msg() { return { t: 'op', rev: this.rev, op: this.inflight }; }

    local(op) {
      this.text = OT.apply(this.text, op);
      if (this.inflight) { this.buffer = join(this.buffer, op); return null; }
      this.inflight = op;
      return this.msg();
    }

    ack(rev) {
      this.rev = rev;
      this.serverText = OT.apply(this.serverText, this.inflight);
      this.inflight = this.buffer;
      this.buffer = null;
      return this.inflight ? this.msg() : null;
    }

    // o cliente é sempre o `a` do transform, igual ao servidor
    remote(rev, op) {
      this.rev = rev;
      this.serverText = OT.apply(this.serverText, op);
      if (this.inflight) [this.inflight, op] = OT.transform(this.inflight, op);
      if (this.buffer) [this.buffer, op] = OT.transform(this.buffer, op);
      this.text = OT.apply(this.text, op);
      return op;
    }

    // init depois de reconectar: o servidor pode ter outro texto e outra revisão
    rebase(rev, serverText) {
      const pend = join(this.inflight, this.buffer);
      let p2 = null;
      if (pend) [p2] = OT.transform(pend, OT.diff(this.serverText, serverText, serverText.length));
      this.reset(rev, serverText);
      if (p2 && !OT.isNoop(p2)) {
        this.text = OT.apply(serverText, p2);
        this.inflight = p2;
        return this.msg();
      }
      return null;
    }

    pending() { return this.inflight !== null; }

    // posições dos outros (na revisão do servidor) levadas para o texto local
    localIndex(i) {
      const pend = join(this.inflight, this.buffer);
      return pend ? OT.transformIndex(i, pend, true) : i;
    }
  }

  // Pilhas de inversas; o topo de cada uma vale sobre o texto atual.
  class UndoStack {
    constructor(groupMs = 1000) {
      this.groupMs = groupMs;
      this.undos = [];
      this.redos = [];
      this.last = -Infinity;
    }

    record(op, textBefore, now) {
      if (OT.isNoop(op)) return;
      const inv = OT.invert(op, textBefore);
      const n = this.undos.length;
      // desfazer o grupo = aplicar a inversa nova e depois a do topo
      if (n && now - this.last < this.groupMs) this.undos[n - 1] = OT.compose(inv, this.undos[n - 1]);
      else this.undos.push(inv);
      this.last = now;
      this.redos = [];
    }

    // como o UndoManager do ot.js: do topo para a base, levando a remota adiante
    transform(remoteOp) {
      for (const k of ['undos', 'redos']) {
        let r = remoteOp;
        const out = [];
        for (let i = this[k].length - 1; i >= 0; i--) {
          const [e, r2] = OT.transform(this[k][i], r);
          if (!OT.isNoop(e)) out.unshift(e);
          r = r2;
        }
        this[k] = out;
      }
    }

    peekUndo() { return this.undos.at(-1) ?? null; }
    peekRedo() { return this.redos.at(-1) ?? null; }

    undo(text) { return this.move(this.undos, this.redos, text); }
    redo(text) { return this.move(this.redos, this.undos, text); }

    move(from, to, text) {
      const op = from.pop();
      if (!op) return null;
      to.push(OT.invert(op, text));
      this.last = -Infinity; // digitar depois de desfazer abre grupo novo
      return op;
    }
  }

  const PadSync = { SyncClient, UndoStack };
  globalThis.PadSync = PadSync;
  if (typeof module !== 'undefined') module.exports = PadSync;
})();
