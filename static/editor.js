// Editor ao vivo: liga o textarea #t ao servidor por WebSocket. As operações
// vêm de PadOT, a revisão e o desfazer de PadSync; aqui fica só o DOM.
(function () {
  const OT = typeof module !== 'undefined' ? require('./ot.js') : globalThis.PadOT;

  // Nome de quem trava um trecho que op toca. As posições dos peers valem na
  // revisão do servidor; map as leva para o texto local (sync.localIndex).
  function lockedBy(op, peers, me, map) {
    for (const p of peers) {
      if (p.lock && p.id !== me && OT.touchesRange(op, map(Math.min(p.a, p.h)), map(Math.max(p.a, p.h)))) return p.name;
    }
    return null;
  }

  if (typeof module !== 'undefined') module.exports = { lockedBy };
  if (typeof document === 'undefined') return;

  const t = document.getElementById('t'), st = document.getElementById('st');
  const lockMsg = document.getElementById('trava'), fim = document.getElementById('fim');
  const canEditPage = t.dataset.canEdit === 'true';
  // o cid vale a vida da aba, entre reconexões: o servidor reconhece a op cujo ack se perdeu
  const cid = Array.from(crypto.getRandomValues(new Uint8Array(12)), (b) => b.toString(16).padStart(2, '0')).join('');
  const pad = (window.pad = { sync: null, undo: new PadSync.UndoStack(), socket: null, peers: [], me: null });
  let prev = t.value; // texto que o sync conhece; o textarea difere dele só no meio de um input
  let composing = false, queue = [], resetNext = false, delay = 500, selTimer = 0, msgTimer = 0;
  let saved = { s: 0, e: 0, d: 'none' }; // seleção antes do input, para desfazer um input barrado
  let visitor = null;
  try { visitor = localStorage['pad:visitor'] || null; } catch {}

  const fire = (type) => document.dispatchEvent(new CustomEvent(type));
  const local = (i) => pad.sync.localIndex(i);
  const raw = () => ({ s: t.selectionStart, e: t.selectionEnd, d: t.selectionDirection });

  function send(m) {
    if (!m || pad.socket?.readyState !== WebSocket.OPEN) return; // o pendente volta no rebase
    const back = t.selectionDirection === 'backward'; // de trás para frente, a âncora é o fim
    pad.socket.send(JSON.stringify({ ...m, a: back ? t.selectionEnd : t.selectionStart, h: back ? t.selectionStart : t.selectionEnd }));
  }

  function sendSel() {
    if (selTimer) return;
    selTimer = setTimeout(() => {
      selTimer = 0;
      if (pad.sync && !pad.sync.pending() && !composing) send({ t: 'sel', rev: pad.sync.rev });
    }, 50);
  }

  function warn(text) {
    lockMsg.textContent = text;
    lockMsg.hidden = false;
    clearTimeout(msgTimer);
    msgTimer = setTimeout(() => (lockMsg.hidden = true), 2000);
  }

  // troca o texto mantendo a seleção própria e a rolagem
  function show(text, op, stick) {
    const { s, e, d } = raw(), top = t.scrollTop;
    t.value = text;
    t.setSelectionRange(OT.transformIndex(s, op, stick), OT.transformIndex(e, op, stick), d);
    t.scrollTop = top;
  }

  function changed() {
    prev = t.value;
    fire('pad:text');
  }

  // as posições dos outros acompanham uma op já aplicada no servidor, como lá
  function shift(op) {
    for (const p of pad.peers) {
      p.a = OT.transformIndex(p.a, op, true);
      p.h = OT.transformIndex(p.h, op, true);
    }
  }

  function edit() {
    if (composing || t.readOnly) return;
    const before = prev, op = OT.diff(before, t.value, t.selectionEnd);
    if (OT.isNoop(op)) return;
    const who = lockedBy(op, pad.peers, pad.me, local);
    if (who) {
      t.value = before;
      t.setSelectionRange(saved.s, saved.e, saved.d);
      return warn('trecho travado por ' + who);
    }
    pad.undo.record(op, before, Date.now());
    changed();
    send(pad.sync.local(op));
  }

  function history(redo) {
    if (composing || t.readOnly) return;
    const op = redo ? pad.undo.peekRedo() : pad.undo.peekUndo();
    if (!op) return;
    const who = lockedBy(op, pad.peers, pad.me, local);
    if (who) return warn('trecho travado por ' + who);
    if (redo) pad.undo.redo(prev);
    else pad.undo.undo(prev);
    show(OT.apply(prev, op), op, true);
    changed();
    send(pad.sync.local(op));
  }

  function init(m) {
    if (m.visitor) {
      visitor = m.visitor;
      try { localStorage['pad:visitor'] = visitor; } catch {}
    }
    pad.me = m.me;
    pad.peers = []; // as posições antigas eram de outra conexão
    delay = 500;
    let op, msg = null;
    if (!pad.sync) {
      op = OT.diff(t.value, m.text, 0);
      pad.sync = new PadSync.SyncClient(m.rev, m.text, cid);
    } else if (resetNext) {
      op = pad.sync.reset(m.rev, m.text);
      pad.undo.clear();
    } else {
      ({ op, msg } = pad.sync.rebase(m.rev, m.text, m.seq));
      if (op) pad.undo.transform(op);
    }
    if (op) show(pad.sync.text, op, false);
    changed();
    send(msg);
    if (!resetNext) st.textContent = ''; // depois de um reject, o motivo fica à vista
    resetNext = false;
    t.readOnly = !(canEditPage && m.canEdit);
    sendSel();
    fire('pad:peers');
  }

  function receive(m) {
    if (composing) return queue.push(m); // nada mexe no textarea no meio de uma composição
    try {
      if (m.t === 'init') init(m);
      else if (m.t === 'ack') {
        shift(pad.sync.inflight);
        st.textContent = '';
        const msg = pad.sync.ack(m.rev);
        if (msg) send(msg);
        else sendSel();
      } else if (m.t === 'op') {
        shift(m.op);
        const op = pad.sync.remote(m.rev, m.op);
        pad.undo.transform(op);
        show(pad.sync.text, op, false);
        changed();
      } else if (m.t === 'peers') {
        pad.peers = m.peers;
        fire('pad:peers');
      } else if (m.t === 'reject') {
        st.textContent = m.reason;
        resetNext = true;
      }
    } catch (err) {
      // estado fora de compasso: reconecta; se nem o rebase serviu, o próximo init recomeça do servidor
      console.error(err);
      if (m.t === 'init') resetNext = true;
      pad.socket.close();
    }
  }

  function connect() {
    t.readOnly = true;
    st.textContent = 'reconectando…';
    const url = (location.protocol === 'https:' ? 'wss:' : 'ws:') + '//' + location.host + '/_ws/' + t.dataset.name +
      '?c=' + cid + (visitor ? '&v=' + encodeURIComponent(visitor) : '');
    const ws = (pad.socket = new WebSocket(url));
    ws.onmessage = (e) => { if (ws.readyState === WebSocket.OPEN) receive(JSON.parse(e.data)); };
    ws.onclose = (e) => {
      t.readOnly = true;
      queue = []; // o próximo init ressincroniza
      if (e.code === 4000) {
        st.textContent = '';
        fim.firstChild.textContent = e.reason + ' ';
        fim.hidden = false;
        return;
      }
      st.textContent = 'reconectando…';
      setTimeout(connect, delay);
      delay = Math.min(delay * 2, 10000);
    };
  }

  t.addEventListener('input', edit);
  t.addEventListener('compositionstart', () => {
    composing = true;
    saved = raw();
  });
  t.addEventListener('compositionend', () => {
    composing = false;
    edit();
    const q = queue;
    queue = [];
    q.forEach(receive);
  });
  t.addEventListener('beforeinput', (e) => {
    if (e.inputType === 'historyUndo' || e.inputType === 'historyRedo') {
      e.preventDefault();
      history(e.inputType === 'historyRedo');
    } else if (!composing) saved = raw();
  });
  t.addEventListener('keydown', (e) => {
    if (!(e.ctrlKey || e.metaKey) || e.altKey) return; // Ctrl+Alt é AltGr no Windows
    const k = e.key.toLowerCase();
    if (k === 'z' || k === 'y') {
      e.preventDefault();
      history(k === 'y' || e.shiftKey);
    }
  });
  t.addEventListener('keyup', sendSel);
  t.addEventListener('mouseup', sendSel);
  document.addEventListener('selectionchange', () => { if (document.activeElement === t) sendSel(); });
  addEventListener('beforeunload', (e) => { if (pad.sync?.pending()) e.preventDefault(); });
  connect();
})();
