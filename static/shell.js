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

  // item do menu que recebe o foco com a tecla; i = -1 quando o foco está fora dos itens
  function nextIndex(i, n, key) {
    if (key === 'Home') return 0;
    if (key === 'End') return n - 1;
    if (key === 'ArrowDown') return (i + 1) % n;
    if (key === 'ArrowUp') return i <= 0 ? n - 1 : i - 1;
    return -1;
  }

  const PadShell = { abas, coluna, pastas, montarColuna, montarAbas, menu, nextIndex };
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
    nav.replaceChildren(...l.map((nome, i) => {
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
        else {
          desenharAbas(nav, resto, atual, u);
          // o botão sumiu no redesenho: o foco vai para a aba que ficou no lugar (a atual nunca sai da lista)
          (nav.children[i] || nav.lastElementChild).querySelector('a').focus();
        }
      };
      aba.append(a, x);
      return aba;
    }));
  }

  // menus da barra (conta e tema): abre pelo botão, fecha com Esc, clique fora ou Tab para fora
  // aoAbrir roda com o menu já visível, antes do foco: pode montar ou marcar os itens
  function menu(btn, box, aoAbrir) {
    const m = $(btn.getAttribute('aria-controls'));
    const itens = () => [...m.querySelectorAll('.menu-item')];
    const abrir = (v) => {
      m.hidden = !v;
      btn.setAttribute('aria-expanded', String(v));
      if (!v) return;
      aoAbrir?.();
      (m.querySelector('[aria-checked="true"]') || itens()[0])?.focus();
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
      const l = itens(), j = nextIndex(l.indexOf(document.activeElement), l.length, e.key);
      if (j < 0) return;
      e.preventDefault();
      l[j].focus();
    });
    return abrir;
  }

  function tema() {
    const btn = $('tema');
    if (!btn) return;
    const T = globalThis.PadTheme, m = $('tema-menu');
    const rotular = () => {
      const t = T.temas.find((t) => t.id === document.documentElement.dataset.theme);
      // o rótulo some da vista em tela estreita (moldura.css), mas fica no nome acessível
      const rot = document.createElement('span');
      rot.className = 'rot';
      rot.textContent = 'tema: ';
      btn.replaceChildren(rot, (t ? t.nome : '') + ' ▾');
    };
    const item = (id, nome) => {
      const b = document.createElement('button');
      b.type = 'button';
      b.className = 'menu-item';
      b.setAttribute('role', 'menuitemradio');
      b.append(T.amostra(id), nome);
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
    m.append(item('sistema', 'Seguir o sistema'));
    for (const [tipo, txt] of [['escuro', 'escuros'], ['claro', 'claros']]) {
      m.append(grupo(txt), ...T.temas.filter((t) => t.tipo === tipo).map((t) => item(t.id, t.nome)));
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
      // a gaveta vem depois da barra no HTML: o foco entra nela, senão o Tab passaria pela barra antes
      if (gaveta.matches) {
        if (body.classList.toggle('gaveta')) col.querySelector('a, button, input')?.focus();
      } else body.classList.toggle('sem-col', !coluna.alternar(u, visivel()));
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
    col.addEventListener('focusout', (e) => {
      if (body.classList.contains('gaveta') && e.relatedTarget && !col.contains(e.relatedTarget) && e.relatedTarget !== btn) fecharGaveta();
    });
    col.addEventListener('click', (e) => {
      const p = e.target.closest('.pasta');
      if (p) {
        p.setAttribute('aria-expanded', String(p.getAttribute('aria-expanded') !== 'true'));
        pastas.gravar(u, [...col.querySelectorAll('.pasta[aria-expanded="true"]')].map((b) => b.dataset.pasta));
      } else if (e.target.closest('a')) fecharGaveta();
    });
  }

  document.addEventListener('DOMContentLoaded', () => {
    for (const box of document.querySelectorAll('.conta')) menu(box.querySelector('button'), box);
    tema();
    ligarColuna();
  });
})();
