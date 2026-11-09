// Apresentação do pad: sorteia os nomes da página, roda a cena de edição da abertura,
// marca a aba da seção à vista e liga os botões de tema. Sem JS a página fica no estado final.
(function () {
  const NOMES = ['Paulo', 'JP', 'Diego', 'Kyval', 'Pedao', 'Julio', 'JV', 'Adryel'];

  // n nomes diferentes, em ordem aleatória (Fisher-Yates)
  function sortear(nomes, n, rnd = Math.random) {
    const l = [...nomes];
    for (let i = l.length - 1; i > 0; i--) {
      const j = Math.floor(rnd() * (i + 1));
      [l[i], l[j]] = [l[j], l[i]];
    }
    return l.slice(0, n);
  }

  // Pedaços de um texto com um cursor em pos e seleção de ini até pos.
  // O que passa de visivel ainda não foi digitado: ocupa lugar, mas não aparece.
  function pedacos(texto, pos, ini = pos, visivel = texto.length) {
    return [
      { tipo: 'texto', t: texto.slice(0, ini) },
      { tipo: 'sel', t: texto.slice(ini, pos) },
      { tipo: 'cur', t: '' },
      { tipo: 'texto', t: texto.slice(pos, visivel) },
      { tipo: 'oculto', t: texto.slice(Math.max(pos, visivel)) },
    ].filter((p) => p.t || p.tipo === 'cur');
  }

  // A primeira pessoa digita o fim do parágrafo; depois a segunda seleciona o fim do título.
  function roteiro(tamTitulo, iniSel, tamTexto, iniDig) {
    const q = [];
    for (let p = iniDig; p <= tamTexto; p++) q.push({ texto: p, titulo: iniSel, ms: p === iniDig ? 900 : 70 });
    for (let p = iniSel + 1; p <= tamTitulo; p++) q.push({ texto: tamTexto, titulo: p, ms: p === iniSel + 1 ? 600 : 50 });
    return q;
  }

  // índice da última seção cujo topo já passou da linha; antes de todas, a primeira
  function secaoAtual(topos, linha) {
    return Math.max(0, topos.findLastIndex((t) => t <= linha));
  }

  const PadLanding = { NOMES, sortear, pedacos, roteiro, secaoAtual };
  if (typeof module !== 'undefined') module.exports = PadLanding;
  if (typeof document === 'undefined') return;

  const $ = (id) => document.getElementById(id);

  function nomes() {
    const l = sortear(NOMES, 4);
    for (const el of document.querySelectorAll('[data-pessoa]')) {
      const n = l[el.dataset.pessoa];
      el.textContent = 'inicial' in el.dataset ? n.charAt(0) : 'min' in el.dataset ? n.toLowerCase() : n;
    }
  }

  function cena() {
    const elT = $('d-titulo'), elX = $('d-texto'), btn = $('demo'), salvo = $('salvo'), st = $('st');
    const curT = elT.querySelector('.cur'), curX = elX.querySelector('.cur');
    const classeT = curT.className.split(' ').at(-1);
    const semCursor = (el) => {
      const c = el.cloneNode(true);
      c.querySelector('.cur').remove();
      return c.textContent;
    };
    const titulo = semCursor(elT), texto = semCursor(elX);
    const iniSel = titulo.length - elT.querySelector('.sel').textContent.length;
    const iniDig = texto.length - elX.querySelector('.dig').textContent.length;
    const quadros = roteiro(titulo.length, iniSel, texto.length, iniDig);

    const montar = (el, cur, l) => el.replaceChildren(...l.map((p) => {
      if (p.tipo === 'cur') return cur;
      if (p.tipo === 'texto') return p.t;
      const s = document.createElement('span');
      s.textContent = p.t;
      s.className = p.tipo === 'sel' ? 'sel ' + classeT : 'oculto';
      return s;
    }));
    const desenhar = (q) => {
      montar(elT, curT, pedacos(titulo, q.titulo, iniSel));
      montar(elX, curX, pedacos(texto, q.texto, q.texto, q.texto));
      const digitando = q.texto < texto.length;
      salvo.classList.toggle('ok', !digitando);
      st.textContent = digitando ? 'salvando…' : 'salvo';
    };

    let timer = null;
    const fim = () => {
      clearTimeout(timer);
      timer = null;
      desenhar(quadros.at(-1));
      btn.textContent = 'Repetir demonstração';
    };
    const tocar = (i) => {
      if (i === quadros.length) return fim();
      desenhar(quadros[i]);
      timer = setTimeout(() => tocar(i + 1), quadros[i].ms);
    };
    const comecar = () => {
      btn.textContent = 'Parar demonstração';
      tocar(0);
    };
    btn.hidden = false;
    btn.addEventListener('click', () => (timer ? fim() : comecar()));
    if (matchMedia('(prefers-reduced-motion: reduce)').matches) fim();
    else comecar();
  }

  // a aba da seção que passou da linha onde as âncoras param fica acesa, e à vista quando as abas rolam de lado
  function abas() {
    const nav = $('abas'), secoes = [...document.querySelectorAll('.doc section')];
    let acesa = 'inicio', pendente = false;
    const marcar = (id) => {
      if (id === acesa) return;
      acesa = id;
      for (const a of nav.querySelectorAll('a')) {
        const on = a.hash === '#' + id;
        a.parentNode.classList.toggle('on', on);
        if (on) {
          a.setAttribute('aria-current', 'location');
          const n = nav.getBoundingClientRect(), r = a.getBoundingClientRect();
          if (r.left < n.left || r.right > n.right) nav.scrollLeft += r.left - n.left - 12;
        } else a.removeAttribute('aria-current');
      }
    };
    const atualizar = () => {
      pendente = false;
      // +1: a rolagem até a âncora pode parar uma fração de pixel antes da linha
      const linha = parseFloat(getComputedStyle(document.documentElement).scrollPaddingTop) + 1;
      marcar(secoes[secaoAtual(secoes.map((s) => s.getBoundingClientRect().top), linha)].id);
    };
    const agendar = () => {
      if (pendente) return;
      pendente = true;
      requestAnimationFrame(atualizar);
    };
    addEventListener('scroll', agendar, { passive: true });
    addEventListener('resize', agendar);
    atualizar();
  }

  function temas() {
    const T = globalThis.PadTheme, lista = $('temas-lista');
    const botoes = T.temas.map((t) => {
      const b = document.createElement('button');
      b.type = 'button';
      b.className = 'btn';
      b.dataset.tema = t.id;
      b.append(T.amostra(t.id), t.nome);
      b.onclick = () => T.aplicar(t.id);
      return b;
    });
    lista.append(...botoes);
    const marcar = () => {
      for (const b of botoes) b.setAttribute('aria-pressed', String(b.dataset.tema === document.documentElement.dataset.theme));
    };
    marcar();
    new MutationObserver(marcar).observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] });
  }

  // os nomes trocam já, antes da primeira pintura; o theme.js (defer) só existe no DOMContentLoaded
  nomes();
  cena();
  abas();
  document.addEventListener('DOMContentLoaded', temas);
})();
