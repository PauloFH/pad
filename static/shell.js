// Moldura do editor: coluna de documentos, abas, menus da barra e barra de status.
// Coluna, pastas e abas ficam no localStorage por usuário, para quem divide o navegador
// não ver os nomes de documento do outro. Carregado no head, sem defer: o montarColuna e
// o montarAbas rodam inline logo depois do HTML deles, antes da primeira pintura.
(function () {
  const MAX_ABAS = 8;

  // aba anônima ou armazenamento bloqueado lançam exceção: vale o padrão
  function ler(chave) {
    try {
      return JSON.parse(globalThis.localStorage.getItem(chave));
    } catch {
      return null;
    }
  }
  function gravar(chave, v) {
    try {
      globalThis.localStorage.setItem(chave, JSON.stringify(v));
    } catch {} // sem armazenamento vale só nesta página
  }
  const lista = (chave) => {
    const v = ler(chave);
    return Array.isArray(v) ? v.filter((x) => typeof x === 'string') : [];
  };

  const abas = {
    ler: (u) => lista('pad:abas:' + u),
    // a atual vai para o começo, sem repetir
    abrir(u, nome) {
      const l = [nome, ...abas.ler(u).filter((n) => n !== nome)].slice(0, MAX_ABAS);
      gravar('pad:abas:' + u, l);
      return l;
    },
    fechar(u, nome) {
      const l = abas.ler(u).filter((n) => n !== nome);
      gravar('pad:abas:' + u, l);
      return l;
    },
  };

  const coluna = {
    aberta: (u) => ler('pad:col:' + u) !== false,
    // parte do estado na tela: sem armazenamento, o guardado nunca muda
    alternar(u, atual = coluna.aberta(u)) {
      gravar('pad:col:' + u, !atual);
      return !atual;
    },
  };

  const pastas = {
    ler: (u) => lista('pad:pastas:' + u),
    gravar: (u, l) => gravar('pad:pastas:' + u, l),
  };

  // linha e coluna (a partir de 1) do índice i
  function linhaColuna(text, i) {
    const antes = text.slice(0, i), nl = antes.lastIndexOf('\n');
    return [antes.split('\n').length, i - nl];
  }

  // quem edita agora, para quem só lê; uma pessoa por key, como as bolinhas
  function editando(peers, me) {
    const nomes = new Map();
    for (const p of peers) if (!p.reading && p.id !== me) nomes.set(p.key, p.name);
    const n = [...nomes.values()].map((s) => s.charAt(0).toUpperCase() + s.slice(1));
    if (!n.length) return '';
    if (n.length === 1) return n[0] + ' está editando agora';
    if (n.length === 2) return n[0] + ' e ' + n[1] + ' estão editando agora';
    return n.length + ' pessoas editando agora';
  }

  const PadShell = { abas, coluna, pastas, linhaColuna, editando, montarColuna, montarAbas };
  if (typeof module !== 'undefined') module.exports = PadShell;
  if (typeof document === 'undefined') return;
  globalThis.PadShell = PadShell;

  const $ = (id) => document.getElementById(id);
  const gaveta = matchMedia('(max-width: 767px)');
  const usuario = () => $('coluna')?.dataset.user;

  // coluna aberta ou fechada e as pastas abertas: as guardadas mais as que levam ao atual
  function montarColuna() {
    const u = usuario(), col = $('coluna');
    document.body.classList.toggle('sem-col', !coluna.aberta(u));
    const abertas = new Set(pastas.ler(u));
    const atual = col.querySelector('[aria-current="page"]')?.getAttribute('href').slice(1) || '';
    for (const b of col.querySelectorAll('.pasta')) {
      if (abertas.has(b.dataset.pasta) || atual.startsWith(b.dataset.pasta + '/')) b.setAttribute('aria-expanded', 'true');
    }
  }

  function montarAbas() {
    const nav = $('abas'), atual = nav.dataset.atual, u = usuario();
    desenharAbas(nav, abas.abrir(u, atual), atual, u);
  }

  function desenharAbas(nav, l, atual, u) {
    nav.replaceChildren(...l.map((nome) => {
      const aba = document.createElement('span'), a = document.createElement('a'), x = document.createElement('button');
      aba.className = 'aba';
      a.href = '/' + nome;
      a.title = nome;
      a.textContent = nome.startsWith(u + '/') ? nome.slice(u.length + 1) : nome;
      if (nome === atual) {
        a.setAttribute('aria-current', 'page');
        aba.classList.add('on');
      }
      x.type = 'button';
      x.textContent = '×';
      x.setAttribute('aria-label', 'fechar ' + a.textContent);
      x.onclick = () => {
        const resto = abas.fechar(u, nome);
        if (nome === atual) location.href = resto.length ? '/' + resto[0] : '/';
        else desenharAbas(nav, resto, atual, u);
      };
      aba.append(a, x);
      return aba;
    }));
  }

  // menus da barra (conta e tema): abre pelo botão, fecha com Esc, clique fora ou Tab para fora
  function menu(btn, box, aoAbrir) {
    const m = $(btn.getAttribute('aria-controls'));
    const itens = () => [...m.querySelectorAll('.menu-item')];
    const abrir = (v) => {
      if (v && aoAbrir) aoAbrir();
      m.hidden = !v;
      btn.setAttribute('aria-expanded', String(v));
      if (v) (m.querySelector('[aria-checked="true"]') || itens()[0])?.focus();
    };
    btn.addEventListener('click', () => abrir(m.hidden));
    document.addEventListener('pointerdown', (e) => { if (!m.hidden && !box.contains(e.target)) abrir(false); });
    box.addEventListener('focusout', (e) => { if (!m.hidden && e.relatedTarget && !box.contains(e.relatedTarget)) abrir(false); });
    box.addEventListener('keydown', (e) => {
      if (m.hidden) return;
      if (e.key === 'Escape') {
        abrir(false);
        btn.focus();
        return;
      }
      const l = itens(), i = l.indexOf(document.activeElement);
      const j = { ArrowDown: (i + 1) % l.length, ArrowUp: (i - 1 + l.length) % l.length, Home: 0, End: l.length - 1 }[e.key];
      if (j == null) return;
      e.preventDefault();
      l[j].focus();
    });
    return abrir;
  }

  // as amostras usam as cores das regras de tema do pad.css, sem repetir os valores aqui
  function amostras() {
    const cores = {};
    for (const s of document.styleSheets) {
      try {
        for (const r of s.cssRules) {
          const m = /data-theme="(\w+)"/.exec(r.selectorText || '');
          if (m) cores[m[1]] = [r.style.getPropertyValue('--bg'), r.style.getPropertyValue('--acc')];
        }
      } catch {} // folha de outra origem
    }
    return cores;
  }

  function tema() {
    const btn = $('tema');
    if (!btn) return;
    const T = globalThis.PadTheme, m = $('tema-menu');
    const rotular = () => {
      const t = T.temas.find((t) => t.id === document.documentElement.dataset.theme);
      btn.textContent = 'tema: ' + (t ? t.nome : '') + ' ▾';
    };
    const item = (id, nome, cor) => {
      const b = document.createElement('button');
      b.type = 'button';
      b.className = 'menu-item';
      b.setAttribute('role', 'menuitemradio');
      const i = document.createElement('i');
      i.className = 'amostra';
      if (cor) Object.assign(i.style, { background: cor[0], boxShadow: 'inset 0 0 0 3px ' + cor[1] });
      b.append(i, nome);
      b.onclick = () => {
        T.aplicar(id);
        abrir(false);
        btn.focus();
      };
      b.dataset.tema = id;
      return b;
    };
    const grupo = (txt) => {
      const g = Object.assign(document.createElement('span'), { className: 'menu-grupo', textContent: txt });
      g.setAttribute('role', 'presentation');
      return g;
    };
    const cores = amostras();
    m.append(item('sistema', 'Seguir o sistema'));
    for (const [tipo, txt] of [['escuro', 'escuros'], ['claro', 'claros']]) {
      m.append(grupo(txt), ...T.temas.filter((t) => t.tipo === tipo).map((t) => item(t.id, t.nome, cores[t.id])));
    }
    const abrir = menu(btn, btn.parentNode, () => {
      const e = T.escolha();
      for (const b of m.querySelectorAll('.menu-item')) b.setAttribute('aria-checked', String(b.dataset.tema === e));
    });
    rotular();
    new MutationObserver(rotular).observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] });
  }

  function ligarColuna() {
    const col = $('coluna'), btn = $('col');
    if (!col) return;
    const u = usuario(), body = document.body;
    const visivel = () => (gaveta.matches ? body.classList.contains('gaveta') : !body.classList.contains('sem-col'));
    const marcar = () => btn.setAttribute('aria-expanded', String(visivel()));
    const fecharGaveta = () => { body.classList.remove('gaveta'); marcar(); };
    marcar();
    gaveta.addEventListener('change', fecharGaveta);
    btn.addEventListener('click', () => {
      // no celular a gaveta abre por cima e começa fechada; no resto a escolha fica guardada
      if (gaveta.matches) body.classList.toggle('gaveta');
      else body.classList.toggle('sem-col', !coluna.alternar(u, visivel()));
      marcar();
    });
    document.addEventListener('keydown', (e) => {
      if (e.key === 'Escape' && body.classList.contains('gaveta')) {
        fecharGaveta();
        btn.focus();
      }
    });
    document.addEventListener('pointerdown', (e) => {
      if (body.classList.contains('gaveta') && !col.contains(e.target) && !btn.contains(e.target)) fecharGaveta();
    });
    col.addEventListener('click', (e) => {
      const p = e.target.closest('.pasta');
      if (p) {
        p.setAttribute('aria-expanded', String(p.getAttribute('aria-expanded') !== 'true'));
        pastas.gravar(u, [...col.querySelectorAll('.pasta[aria-expanded="true"]')].map((b) => b.dataset.pasta));
      } else if (e.target.closest('a')) fecharGaveta();
    });
  }

  // barra de status: pessoas no documento, linha e coluna, e quem edita (página só leitura)
  function status() {
    const t = $('t'), pessoas = $('pessoas'), lc = $('lc'), ed = $('editando');
    if (!t) return;
    const peers = () => {
      const pad = globalThis.pad, n = new Set(pad.peers.map((p) => p.key)).size;
      if (pessoas) pessoas.textContent = n ? '● ' + n + (n === 1 ? ' pessoa' : ' pessoas') : '';
      if (ed) ed.textContent = editando(pad.peers, pad.me);
    };
    const cursor = () => {
      if (!lc) return;
      const [l, c] = linhaColuna(t.value, t.selectionDirection === 'backward' ? t.selectionStart : t.selectionEnd);
      lc.textContent = 'linha ' + l + ', coluna ' + c;
    };
    document.addEventListener('pad:peers', peers);
    document.addEventListener('pad:text', cursor);
    document.addEventListener('selectionchange', () => { if (document.activeElement === t) cursor(); });
    t.addEventListener('input', cursor);
    cursor();
  }

  document.addEventListener('DOMContentLoaded', () => {
    for (const box of document.querySelectorAll('.conta')) menu(box.querySelector('button'), box);
    tema();
    ligarColuna();
    status();
  });
})();
