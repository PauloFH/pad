// Painel /_mcp: refaz a tabela dos agentes conectados a cada 5 s. Só textContent: login e apelido vêm de fora.
(function () {
  const tabela = document.getElementById('agentes');
  if (!tabela) return;
  const resumo = document.getElementById('resumo');

  function desenhar(d) {
    resumo.textContent = d.resumo;
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
      const r = await fetch('/_mcp/conectados', { headers: { Accept: 'application/json' } });
      if (r.ok) desenhar(await r.json());
    } catch {} // sem rede ou sessão vencida: mantém o que está na tela e tenta de novo
  }
  setInterval(() => { if (!document.hidden) atualizar(); }, 5000);
})();
