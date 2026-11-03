// Painel de compartilhar: abre pelo botão, fecha com Esc ou clique fora.
// Abre sozinho (data-aberto) quando o servidor devolveu o erro de alvo não encontrado.
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
  // o clique fora não devolve o foco: ele já foi para onde a pessoa clicou
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
  if (painel.hasAttribute('data-aberto')) {
    painel.removeAttribute('data-aberto');
    abrir();
  }
})();
