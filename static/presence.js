// Presença sobre o editor ao vivo: cursores e seleções das outras abas, números de
// linha, bolinhas no cabeçalho e o modo acompanhar. Só lê window.pad (editor.js).
(function () {
  const TYPING_MS = 3000;
  const SCROLL_KEYS = new Set(['PageUp', 'PageDown', 'ArrowUp', 'ArrowDown', 'Home', 'End']);

  // índice UTF-16 onde começa cada linha lógica
  function lineStarts(text) {
    const st = [0];
    for (let i = text.indexOf('\n'); i >= 0; i = text.indexOf('\n', i + 1)) st.push(i + 1);
    return st;
  }

  // linha (a partir de 0) do índice i
  function lineAt(st, i) {
    let lo = 0, hi = st.length - 1;
    while (lo < hi) {
      const mid = (lo + hi + 1) >> 1;
      if (st[mid] <= i) lo = mid;
      else hi = mid - 1;
    }
    return lo;
  }

  // primeira e última linha da seleção; a quebra logo no fim não puxa a linha seguinte
  function span(p, st) {
    const s = Math.min(p.a, p.h), e = Math.max(p.a, p.h);
    return [lineAt(st, s), lineAt(st, e > s ? e - 1 : e)];
  }

  // o que a pessoa está fazendo, para o menu; lastOp é a hora da última op dela
  function state(p, st, lastOp, now) {
    if (now - lastOp < TYPING_MS) return 'digitando na linha ' + (lineAt(st, p.h) + 1);
    if (p.a !== p.h) {
      const [x, y] = span(p, st);
      return x === y ? 'selecionou a linha ' + (x + 1) : 'selecionou as linhas ' + (x + 1) + '–' + (y + 1);
    }
    if (p.reading) return 'lendo';
    return 'linha ' + (lineAt(st, p.h) + 1);
  }

  // uma bolinha por key (as abas da mesma identidade), na ordem de chegada e a própria por último
  function groups(peers, me) {
    const by = new Map();
    for (const p of peers) {
      let g = by.get(p.key);
      if (!g) by.set(p.key, (g = { key: p.key, name: p.name, color: p.color, initial: (Array.from(p.name)[0] || '?').toUpperCase(), mine: false, tabs: [] }));
      g.tabs.push(p);
      if (p.id === me) g.mine = true;
    }
    const all = [...by.values()];
    return [...all.filter((g) => !g.mine), ...all.filter((g) => g.mine)];
  }

  // a aba que fala pela pessoa: a da op mais recente, ou a primeira
  const pick = (tabs, last) => tabs.reduce((b, t) => ((last[t.id] || 0) > (last[b.id] || 0) ? t : b));

  // rolar ou digitar por conta própria desliga o acompanhar
  const stopsFollow = (e) => e.type === 'input' || e.type === 'wheel' || (e.type === 'keydown' && SCROLL_KEYS.has(e.key));

  // trecho de linhas que mudou: [início, fim no antigo, fim no novo]; o resto do espelho fica
  function changed(old, now) {
    let a = 0, b = 0;
    while (a < old.length && a < now.length && old[a] === now[a]) a++;
    while (b < old.length - a && b < now.length - a && old[old.length - 1 - b] === now[now.length - 1 - b]) b++;
    return [a, old.length - b, now.length - b];
  }

  if (typeof module !== 'undefined') module.exports = { lineStarts, lineAt, span, state, groups, pick, stopsFollow, changed };
  if (typeof document === 'undefined') return;

  const $ = (id) => document.getElementById(id);
  const pad = window.pad, t = $('t'), mir = $('mir'), lay = $('lay'), gut = $('gut'), area = $('area');
  const box = $('peers'), btn = $('quem'), menu = $('quem-menu'), bar = $('seguir'), who = bar.querySelector('b');
  const range = document.createRange();
  const last = {}; // id da aba → hora da última op dela
  let text = null, lines = [], starts = [0], frame = 0, follow = null, chase = false, tick = 0;

  const schedule = () => { if (!frame) frame = requestAnimationFrame(draw); };
  const el = (tag, cls, txt) => {
    const e = document.createElement(tag);
    if (cls) e.className = cls;
    if (txt != null) e.textContent = txt; // nomes vêm do servidor: só como texto
    return e;
  };
  const place = (e, x, y, w, h) => Object.assign(e.style, { left: x + 'px', top: y + 'px', width: w == null ? '' : w + 'px', height: h + 'px' });

  // o espelho tem uma div por linha lógica, com a mesma quebra do textarea
  function mirror() {
    if (pad.sync.text === text) return;
    text = pad.sync.text;
    const next = text.split('\n'), [a, oldEnd, newEnd] = changed(lines, next), ref = mir.children[oldEnd] || null;
    for (let k = a; k < oldEnd; k++) mir.children[a].remove();
    for (let k = a; k < newEnd; k++) mir.insertBefore(el('div', '', next[k] || '\u200b'), ref); // linha vazia precisa de altura
    lines = next;
    starts = lineStarts(text);
  }

  // posições do servidor no texto local; a própria aba usa a seleção do textarea, mais atual
  function local(p) {
    if (p.id === pad.me) return { ...p, a: t.selectionStart, h: t.selectionEnd };
    const clamp = (i, stick) => Math.max(0, Math.min(text.length, pad.sync.localIndex(i, stick)));
    const s = clamp(Math.min(p.a, p.h), true), e = p.a === p.h ? s : clamp(Math.max(p.a, p.h), false);
    return { ...p, a: p.a > p.h ? e : s, h: p.a > p.h ? s : e };
  }

  // retângulos (na tela) do trecho [s, e) da linha L
  function rects(L, s, e) {
    const node = mir.children[L].firstChild, len = lines[L].length;
    range.setStart(node, Math.min(len, Math.max(0, s - starts[L])));
    range.setEnd(node, Math.min(len, Math.max(0, e - starts[L])));
    return range.getClientRects();
  }

  function caret(i) {
    const L = lineAt(starts, i);
    return rects(L, i, i)[0] || mir.children[L].getBoundingClientRect();
  }

  // linhas lógicas que aparecem no textarea agora
  function visible() {
    const els = mir.children, top = t.scrollTop, bottom = top + t.clientHeight;
    let lo = 0, hi = els.length - 1;
    while (lo < hi) {
      const mid = (lo + hi) >> 1;
      if (els[mid].offsetTop + els[mid].offsetHeight <= top) lo = mid + 1;
      else hi = mid;
    }
    let end = lo;
    while (end + 1 < els.length && els[end + 1].offsetTop < bottom) end++;
    return [lo, end];
  }

  function following(gs) {
    if (!follow) return;
    const g = gs.find((g) => g.key === follow && !g.mine);
    if (!g) return stop();
    bar.hidden = false;
    bar.style.background = g.color;
    who.textContent = g.name;
    area.classList.add('fol');
    area.style.setProperty('--fc', g.color);
    if (!chase) return;
    chase = false;
    const r = caret(pick(g.tabs, last).h), y = r.top - mir.getBoundingClientRect().top;
    if (y < t.scrollTop || y + r.height > t.scrollTop + t.clientHeight) t.scrollTop = y - t.clientHeight / 3;
  }

  function overlay(view, v0, v1) {
    const o = mir.getBoundingClientRect(), dx = -o.left - t.scrollLeft, dy = -o.top - t.scrollTop, out = [];
    for (const p of view) {
      if (p.id === pad.me) continue; // a própria aba já tem o caret e a seleção nativos
      const s = Math.min(p.a, p.h), e = Math.max(p.a, p.h);
      let end = null;
      for (let L = Math.max(v0, lineAt(starts, s)); s !== e && L <= v1 && starts[L] <= e; L++) {
        for (const r of rects(L, s, e)) {
          const d = el('div', 's');
          d.style.background = p.color;
          place(d, r.left + dx, r.top + dy, r.width, r.height);
          out.push(d);
          end = r;
        }
      }
      if (p.lock && end) {
        const k = el('div', 'k', '🔒 ' + p.name);
        k.style.background = p.color;
        place(k, end.right + dx + 4, end.top + dy, null, end.height);
        out.push(k);
      }
      const r = caret(p.h), y = r.top + dy;
      if (y + r.height < 0 || y > t.clientHeight) continue;
      const c = el('div', y < 16 ? 'c dn' : 'c'), flag = el('span', '', p.name);
      c.style.background = flag.style.background = p.color;
      place(c, r.left + dx, y, null, r.height);
      c.append(flag);
      out.push(c);
    }
    Object.assign(lay.style, { width: t.clientWidth + 'px', height: t.clientHeight + 'px' });
    lay.replaceChildren(...out);
  }

  function gutter(view, v0, v1) {
    const on = new Map(); // linha → cor de quem está nela
    for (const p of view) {
      const [x, y] = span(p, starts);
      for (let L = Math.max(x, v0); L <= Math.min(y, v1); L++) if (!on.has(L)) on.set(L, p.color);
    }
    const out = [];
    for (let L = v0; L <= v1; L++) {
      const d = el('div', on.has(L) ? 'on' : '', String(L + 1));
      d.style.top = mir.children[L].offsetTop - t.scrollTop + 'px';
      if (on.has(L)) d.style.color = on.get(L);
      out.push(d);
    }
    gut.style.width = `calc(${String(lines.length).length}ch + 1rem)`;
    gut.replaceChildren(...out);
  }

  function bubbles(gs, now) {
    const st = (g) => {
      const p = (g.mine && g.tabs.find((p) => p.id === pad.me)) || pick(g.tabs, last);
      return state(p, starts, last[p.id], now);
    };
    btn.hidden = !gs.length;
    btn.setAttribute('aria-label', gs.length + (gs.length === 1 ? ' pessoa' : ' pessoas') + ' no documento');
    btn.replaceChildren(...gs.map((g) => {
      const d = el('span', g.key === follow ? 'dot ring' : 'dot', g.initial);
      d.style.background = g.color;
      d.style.setProperty('--c', g.color);
      d.title = (g.mine ? 'você' : g.name) + ' · ' + st(g);
      return d;
    }));
    if (menu.hidden) return;
    const focused = menu.contains(document.activeElement) ? document.activeElement.dataset.key : null;
    menu.replaceChildren(...gs.map((g) => {
      const b = el('button'), sq = el('span', 'sq'), txt = el('span', '', g.mine ? 'você' : g.name);
      Object.assign(b, { type: 'button', onclick: () => choose(g) });
      b.setAttribute('role', 'menuitem');
      b.dataset.key = g.key;
      if (g.mine) b.setAttribute('aria-disabled', 'true');
      sq.style.background = g.color;
      txt.append(el('small', '', st(g) + (g.key === follow ? ' · acompanhando' : '')));
      b.append(sq, txt);
      return b;
    }));
    if (focused != null) [...menu.children].find((b) => b.dataset.key === focused)?.focus();
  }

  function draw() {
    frame = 0;
    if (!pad.sync) return;
    mirror();
    mir.style.width = t.clientWidth + 'px'; // sem a barra de rolagem, como o texto do textarea
    const view = pad.peers.map(local), gs = groups(view, pad.me);
    following(gs);
    const [v0, v1] = visible();
    overlay(view, v0, v1);
    gutter(view, v0, v1);
    bubbles(gs, Date.now());
  }

  function stop() {
    follow = null;
    bar.hidden = true;
    area.classList.remove('fol');
    schedule();
  }

  function open(v) {
    menu.hidden = !v;
    btn.setAttribute('aria-expanded', String(v));
    clearInterval(tick);
    if (!v) return;
    tick = setInterval(schedule, 1000); // "digitando" vence sozinho
    draw();
    menu.querySelector('button')?.focus();
  }

  function choose(g) {
    if (g.mine) return;
    follow = g.key;
    chase = true;
    open(false);
    btn.focus();
    schedule();
  }

  document.addEventListener('pad:peers', () => { chase = true; schedule(); });
  document.addEventListener('pad:text', schedule);
  document.addEventListener('pad:op', (e) => { last[e.detail.by] = Date.now(); schedule(); });
  t.addEventListener('scroll', schedule);
  new ResizeObserver(schedule).observe(t);
  const quit = (e) => { if (follow && stopsFollow(e)) stop(); };
  t.addEventListener('input', (e) => { last[pad.me] = Date.now(); quit(e); });
  t.addEventListener('keydown', quit);
  area.addEventListener('wheel', quit, { passive: true });
  bar.querySelector('button').addEventListener('click', stop);
  btn.addEventListener('click', () => open(menu.hidden));
  document.addEventListener('pointerdown', (e) => { if (!menu.hidden && !box.contains(e.target)) open(false); });
  box.addEventListener('keydown', (e) => {
    if (menu.hidden) return;
    if (e.key === 'Escape') {
      open(false);
      btn.focus();
    } else if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      const items = [...menu.children], i = items.indexOf(document.activeElement), n = items.length;
      items[(i + (e.key === 'ArrowDown' ? 1 : n - 1)) % n]?.focus();
    }
  });
})();
