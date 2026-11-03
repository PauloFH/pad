// Painel de compartilhar: abre pelo botão, fecha com Esc, clique fora ou foco que sai dele.
// O HTML vem aberto (sem JS os formulários continuam alcançáveis); aqui ele fecha, exceto
// com data-aberto, que o servidor põe quando o alvo não foi encontrado.
(function () {
  const raiz = document.getElementById('compartilhar');
  if (!raiz) return;
  const botao = document.getElementById('comp-abrir');
  const painel = document.getElementById('comp-painel');
  const alvo = painel.querySelector('input[name=alvo]');

  const aberto = () => !painel.hidden;
  function abrir() {
    painel.hidden = false;
    botao.setAttribute('aria-expanded', 'true');
    alvo.focus();
  }
  // o clique fora e o Tab para fora não devolvem o foco: ele já foi para onde a pessoa quis
  function fechar(devolverFoco) {
    painel.hidden = true;
    botao.setAttribute('aria-expanded', 'false');
    if (devolverFoco) botao.focus();
  }

  botao.addEventListener('click', () => (aberto() ? fechar(true) : abrir()));
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && aberto()) fechar(true);
  });
  document.addEventListener('click', (e) => {
    if (aberto() && !raiz.contains(e.target)) fechar(false);
  });
  // relatedTarget nulo é clique em texto do painel ou janela perdendo o foco: não fecha
  raiz.addEventListener('focusout', (e) => {
    if (aberto() && e.relatedTarget && !raiz.contains(e.relatedTarget)) fechar(false);
  });
  if (painel.hasAttribute('data-aberto')) abrir();
  else fechar(false);
})();
