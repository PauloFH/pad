// Editor ao vivo: liga o textarea #t ao servidor por WebSocket. As operações
// vêm de PadOT, a revisão e o desfazer de PadSync; aqui fica só o DOM.
(function () {
  const OT = typeof module !== 'undefined' ? require('./ot.js') : globalThis.PadOT;

  // Nome de quem trava um trecho que op toca. As posições dos peers valem na
  // revisão do servidor; map(i, stickRight) as leva para o texto local (sync.localIndex).
  // Como no servidor, insert colado a uma borda fica fora da trava: o início anda, o fim fica.
  function lockedBy(op, peers, me, map) {
    for (const p of peers) {
      if (p.lock && p.id !== me && OT.touchesRange(op, map(Math.min(p.a, p.h), true), map(Math.max(p.a, p.h), false))) return p.name;
    }
    return null;
  }

  // as posições dos outros acompanham uma op já aplicada no servidor, com a mesma regra de lá
  function shift(peers, op) {
    for (const p of peers) {
      const back = p.a > p.h;
      const s = OT.transformIndex(back ? p.h : p.a, op, true), e = OT.transformIndex(back ? p.a : p.h, op, !p.lock);
      [p.a, p.h] = back ? [e, s] : [s, e];
    }
  }

  if (typeof module !== 'undefined') module.exports = { lockedBy, shift };
  if (typeof document === 'undefined') return;

  const t = document.getElementById('t'), st = document.getElementById('st'), salvo = document.getElementById('salvo');
  const lockMsg = document.getElementById('trava'), fim = document.getElementById('fim');
  const canEditPage = t.dataset.canEdit === 'true';
  // o cid vale a vida da aba, entre reconexões: o servidor reconhece a op cujo ack se perdeu.
  // getRandomValues funciona também fora de contexto seguro (http), ao contrário de randomUUID
  const cid = Array.from(crypto.getRandomValues(new Uint8Array(12)), (b) => b.toString(16).padStart(2, '0')).join('');
  const pad = (window.pad = { sync: null, undo: new PadSync.UndoStack(), socket: null, peers: [], me: null });
  let prev = t.value; // texto que o sync conhece; o textarea difere dele só no meio de um input
  // resetNext: o próximo init recomeça do servidor; guarda o motivo a mostrar
  let composing = false, moving = null, queue = [], resetNext = '', delay = 500, selTimer = 0, msgTimer = 0, alertaFixo = false;
  let saved = { s: 0, e: 0, d: 'none' }; // seleção antes do input, para desfazer um input barrado
  let visitor = null;
  try { visitor = localStorage['pad:visitor'] || null; } catch {}

  const fire = (type, detail) => document.dispatchEvent(new CustomEvent(type, { detail }));
  const local = (i, stick) => pad.sync.localIndex(i, stick);
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

  // "salvo" quando o servidor já confirmou tudo (ele grava em disco logo depois); só leitura não salva nada.
  // Fica em #salvo, sem aria-live, para não ser anunciado a cada pausa; #st (sempre polite) leva só a queda e as recusas
  function saveState() {
    const ok = !t.readOnly && !pad.sync.pending();
    st.textContent = '';
    salvo.textContent = t.readOnly ? '' : ok ? 'salvo' : 'salvando…';
    salvo.className = ok ? 'ok' : '';
  }

  function say(text) {
    salvo.textContent = '';
    salvo.className = '';
    st.textContent = text;
  }

  function warn(text) {
    lockMsg.textContent = text;
    lockMsg.hidden = false;
    alertaFixo = false;
    clearTimeout(msgTimer);
    msgTimer = setTimeout(() => (lockMsg.hidden = true), 2000);
  }

  // o que a pessoa escreveu foi descartado: o aviso fica à vista até a próxima edição, não some em 2 s
  function alerta(text) {
    lockMsg.textContent = text;
    lockMsg.hidden = false;
    alertaFixo = true;
    clearTimeout(msgTimer);
  }

  // troca o texto mantendo a seleção própria e a rolagem. Uma seleção é trava: como no
  // servidor, insert colado a uma borda fica fora dela (o início anda, o fim fica)
  function show(text, op, stick) {
    const { s, e, d } = raw(), top = t.scrollTop;
    t.value = text;
    const sel = s < e;
    t.setSelectionRange(OT.transformIndex(s, op, sel || stick), OT.transformIndex(e, op, !sel && stick), d);
    t.scrollTop = top;
  }

  // prev segue o sync, nunca o textarea: assim o diff do próximo input sempre encaixa
  function changed() {
    prev = pad.sync.text;
    fire('pad:text');
  }

  // sem checar readOnly: o compositionend de uma composição cortada pela queda chega
  // com o textarea já travado, e o sync guarda a op até o rebase da reconexão
  function edit() {
    if (composing || !pad.sync) return;
    const op = OT.diff(moving?.op ? moving.text : prev, t.value, t.selectionEnd);
    if (!moving) return commit(op);
    // cada metade do mover tem o seu diff: um só, da origem ao destino, apagaria e
    // reinseriria tudo entre elas (cursores alheios pulam, travas no caminho barram)
    moving.op = moving.op ? OT.compose(moving.op, op) : op;
    moving.text = t.value;
  }

  // fim do mover arrastando: as metades compostas saem numa op só, aceita ou barrada inteira
  function endMove() {
    const op = moving?.op;
    moving = null;
    if (op) commit(op);
  }

  function commit(op) {
    const before = prev;
    if (OT.isNoop(op)) return;
    const who = lockedBy(op, pad.peers, pad.me, local);
    if (who) {
      t.value = before;
      t.setSelectionRange(saved.s, saved.e, saved.d);
      return warn('trecho travado por ' + who);
    }
    if (alertaFixo) {
      alertaFixo = false;
      lockMsg.hidden = true;
    }
    pad.undo.record(op, before, Date.now());
    send(pad.sync.local(op));
    changed();
    saveState();
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
    send(pad.sync.local(op));
    changed();
    saveState();
  }

  function init(m) {
    if (m.visitor) {
      visitor = m.visitor;
      try { localStorage['pad:visitor'] = visitor; } catch {}
    }
    pad.me = m.me;
    delay = 500;
    let msg = null;
    if (!pad.sync) pad.sync = new PadSync.SyncClient(m.rev, m.text);
    else if (resetNext) {
      pad.sync.reset(m.rev, m.text);
      pad.undo.clear();
    } else {
      let op;
      ({ op, msg } = pad.sync.rebase(m.rev, m.text, m.seq));
      if (op) pad.undo.transform(op);
    }
    // com o textarea igual ao texto local antigo, este diff é a própria op do rebase
    if (t.value !== pad.sync.text) show(pad.sync.text, OT.diff(t.value, pad.sync.text, 0), false);
    changed();
    send(msg);
    t.readOnly = !(canEditPage && m.canEdit);
    saveState();
    if (resetNext) alerta(resetNext); // depois de um reject, o motivo fica à vista
    resetNext = '';
    sendSel();
    fire('pad:peers');
  }

  function receive(m) {
    if (composing) return queue.push(m); // nada mexe no textarea no meio de uma composição
    if (moving?.op) endMove(); // as duas metades chegam na mesma tarefa: um dragend se perdeu
    try {
      if (m.t === 'init') init(m);
      else if (m.t === 'ack') {
        shift(pad.peers, pad.sync.inflight);
        const msg = pad.sync.ack(m.rev);
        saveState();
        if (msg) send(msg);
        else sendSel();
      } else if (m.t === 'op') {
        shift(pad.peers, m.op);
        const op = pad.sync.remote(m.rev, m.op);
        pad.undo.transform(op);
        show(pad.sync.text, op, false);
        changed();
        fire('pad:op', { by: m.by }); // a presença mostra quem está digitando
      } else if (m.t === 'peers') {
        pad.peers = m.peers;
        fire('pad:peers');
      } else if (m.t === 'reject') {
        say((resetNext = m.reason || 'operação recusada'));
      }
    } catch (err) {
      // estado fora de compasso: reconecta; se nem o rebase serviu, o próximo init recomeça do servidor
      console.error(err);
      if (m.t === 'init') resetNext = 'não deu para reaplicar o que você digitou durante a queda';
      pad.socket.close();
    }
  }

  function connect() {
    t.readOnly = true;
    say('reconectando…');
    const url = (location.protocol === 'https:' ? 'wss:' : 'ws:') + '//' + location.host + '/_ws/' + t.dataset.name +
      '?c=' + cid + (visitor ? '&v=' + encodeURIComponent(visitor) : '');
    const ws = (pad.socket = new WebSocket(url));
    ws.onmessage = (e) => { if (ws.readyState === WebSocket.OPEN) receive(JSON.parse(e.data)); };
    ws.onclose = (e) => {
      t.readOnly = true;
      queue = []; // o próximo init ressincroniza
      if (e.code === 4000) {
        say('');
        fim.firstChild.textContent = e.reason + ' ';
        fim.hidden = false;
        return;
      }
      // 1009: a mensagem passou do limite. Reenviar a mesma op cairia de novo, para sempre; o próximo init a descarta
      if (e.code === 1009) resetNext = 'texto grande demais para este documento: a colagem não foi salva';
      say('reconectando…');
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
    if (e.inputType !== 'deleteByDrag' && e.inputType !== 'insertFromDrop') endMove(); // dragend perdido não segura a digitação
    if (e.inputType === 'historyUndo' || e.inputType === 'historyRedo') {
      e.preventDefault();
      history(e.inputType === 'historyRedo');
    } else if (!composing && !moving) saved = raw();
  });
  // mover arrastando = apagar a origem + inserir no destino (sem juntar, soltar numa trava
  // alheia apagava o trecho); dragend também vem quando o trecho foi solto fora do textarea
  t.addEventListener('dragstart', () => {
    endMove();
    moving = { op: null, text: '' };
    saved = raw();
  });
  t.addEventListener('dragend', endMove);
  t.addEventListener('keydown', (e) => {
    endMove();
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
