// Páginas do MCP: o painel /{usuario}/_mcp (tabela dos agentes ao vivo) e o chat de cada conversa (/{usuario}/_mcp/c/{nome}).
// Tudo que vem de fora (mensagem, login, apelido) entra na página só por textContent.
(function () {
  // trecho de código; a linha da linguagem fica de fora
  const CERCA = /```[^\n]*\n([\s\S]*?)```/g;
  // @pad /x/y, http(s)://..., ou /usuario/doc (a regra do validUser, depois um ou mais /trecho)
  const LINK = /@pad (\/[\w.-]+(?:\/[\w.-]+)*)|https?:\/\/[^\s<>"]+|(?<![\w/.:-])\/[a-z0-9][a-z0-9_-]{1,39}(?:\/[\w.-]+)+/g;
  const PONTA = /[.,;:!?)\]]+$/;

  function links(s, fora) {
    let i = 0;
    for (const m of s.matchAll(LINK)) {
      let v = m[0].replace(PONTA, '');
      if (m.index > i) fora.push({ t: 'texto', v: s.slice(i, m.index) });
      fora.push({ t: 'link', v, href: m[1] ? m[1].replace(PONTA, '') : v });
      i = m.index + v.length;
    }
    if (i < s.length) fora.push({ t: 'texto', v: s.slice(i) });
  }

  function partes(texto) {
    const fora = [];
    let i = 0;
    for (const m of texto.matchAll(CERCA)) {
      if (m.index > i) links(texto.slice(i, m.index), fora);
      fora.push({ t: 'codigo', v: m[1] });
      i = m.index + m[0].length;
    }
    if (i < texto.length) links(texto.slice(i), fora);
    return fora;
  }

  function quando(unix) {
    const d = new Date(unix * 1000);
    const dois = (n) => String(n).padStart(2, '0');
    return `${dois(d.getDate())}/${dois(d.getMonth() + 1)} ${dois(d.getHours())}:${dois(d.getMinutes())}`;
  }

  if (typeof module !== 'undefined') module.exports = { partes, quando };
  if (typeof document === 'undefined') return;

  function painel() {
    const tabela = document.getElementById('agentes');
    if (!tabela) return;
    const resumo = document.getElementById('resumo');

    function desenhar(d) {
      // região aria-live: só mexe quando o texto muda, senão o leitor de tela repete a cada 5 s
      if (resumo.textContent !== d.resumo) resumo.textContent = d.resumo;
      const linhas = d.agentes.map((a) => {
        const tr = document.createElement('tr');
        [a.apelido, a.maquina, a.tipo, a.conversas.join(', '), a.ultima].forEach((txt, i) => {
          const td = document.createElement('td');
          td.textContent = txt;
          if (i === 0) td.className = 'mono';
          tr.appendChild(td);
        });
        return tr;
      });
      tabela.tBodies[0].replaceChildren(...linhas);
    }

    async function atualizar() {
      try {
        const r = await fetch(tabela.dataset.base + '/conectados', { headers: { Accept: 'application/json' } });
        if (r.ok) desenhar(await r.json());
      } catch {} // sem rede ou sessão vencida: mantém o que está na tela e tenta de novo
    }
    setInterval(() => { if (!document.hidden) atualizar(); }, 5000);
  }

  function balao(m) {
    const li = document.createElement('li');
    li.className = m.humano ? 'balao eu' : 'balao';
    const cab = document.createElement('div');
    cab.className = 'cab';
    const quem = document.createElement('span');
    quem.className = 'quem';
    quem.textContent = m.rotulo;
    const hora = document.createElement('time');
    hora.textContent = quando(m.em);
    cab.append(quem, hora);
    const corpo = document.createElement('div');
    corpo.className = 'texto';
    for (const p of partes(m.texto)) {
      let el;
      if (p.t === 'codigo') {
        el = document.createElement('pre');
      } else if (p.t === 'link') {
        el = document.createElement('a');
        el.href = p.href;
        if (/^https?:/.test(p.href)) { el.target = '_blank'; el.rel = 'noopener noreferrer'; }
      } else {
        corpo.append(document.createTextNode(p.v));
        continue;
      }
      el.textContent = p.v;
      corpo.append(el);
    }
    li.append(cab, corpo);
    return li;
  }

  function chat() {
    const raiz = document.getElementById('chat');
    if (!raiz) return;
    const base = raiz.dataset.base + '/c/' + encodeURIComponent(raiz.dataset.nome);
    const lista = document.getElementById('msgs');
    const ativos = document.getElementById('ativos');
    const form = document.getElementById('enviar');
    const caixa = document.getElementById('texto');
    const botao = form.querySelector('button');
    const erro = document.getElementById('erro');
    let ultimo = 0;

    function agentes(ags) {
      const t = ags.length ? 'ativos agora: ' + ags.join(', ') : 'nenhum agente ativo agora';
      if (ativos.textContent !== t) ativos.textContent = t;
    }

    function desenhar(ms, primeira) {
      // só acompanha o fim se a pessoa já estava nele; quem subiu para reler não é arrastado
      const noFim = primeira || lista.scrollHeight - lista.scrollTop - lista.clientHeight < 40;
      for (const m of ms) {
        lista.append(balao(m));
        ultimo = Math.max(ultimo, m.id);
      }
      if (ms.length && noFim) lista.scrollTop = lista.scrollHeight;
    }

    async function laco() {
      let primeira = true;
      for (;;) {
        try {
          const r = await fetch(`${base}/msgs?depois=${ultimo}${primeira ? '' : '&esperar=1'}`, { headers: { Accept: 'application/json' } });
          if (r.status === 404) {
            ativos.textContent = 'esta conversa não existe mais';
            return;
          }
          if (!r.ok) throw new Error(String(r.status));
          const d = await r.json();
          desenhar(d.mensagens, primeira);
          agentes(d.agentes);
          primeira = false;
        } catch {
          await new Promise((ok) => setTimeout(ok, 3000));
        }
      }
    }

    async function enviar() {
      const texto = caixa.value;
      if (!texto.trim()) return;
      botao.disabled = true;
      erro.textContent = '';
      try {
        const r = await fetch(base + '/msgs', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ texto }),
        });
        if (r.ok) {
          caixa.value = '';
        } else {
          erro.textContent = (await r.text()).trim() || 'não foi possível enviar';
        }
      } catch {
        erro.textContent = 'sem conexão: o texto continua na caixa';
      } finally {
        botao.disabled = false;
        caixa.focus();
      }
    }

    form.addEventListener('submit', (e) => { e.preventDefault(); enviar(); });
    caixa.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
        e.preventDefault();
        if (!botao.disabled) form.requestSubmit();
      }
    });

    // apagar em duas etapas, sem confirm(): o estado fica no próprio botão
    const apagar = document.getElementById('apagar');
    if (apagar) {
      const b = apagar.querySelector('button');
      const rotulo = b.textContent;
      b.addEventListener('click', (e) => {
        if (b.dataset.confirma) return;
        e.preventDefault();
        b.dataset.confirma = '1';
        b.textContent = 'confirmar apagar';
      });
      b.addEventListener('blur', () => { delete b.dataset.confirma; b.textContent = rotulo; });
    }

    laco();
  }

  painel();
  chat();
})();
