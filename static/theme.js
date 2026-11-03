// Tema da página: a escolha fica em localStorage['pad:tema'] ('sistema' ou o id).
// O script inline do head já aplicou antes da primeira pintura; aqui ficam a troca e o seguir o sistema.
(function () {
  const CHAVE = 'pad:tema';
  const temas = [
    { id: 'grafite', nome: 'Grafite', tipo: 'escuro' },
    { id: 'madrugada', nome: 'Madrugada', tipo: 'escuro' },
    { id: 'cafe', nome: 'Café', tipo: 'escuro' },
    { id: 'floresta', nome: 'Floresta', tipo: 'escuro' },
    { id: 'papel', nome: 'Papel', tipo: 'claro' },
    { id: 'ceu', nome: 'Céu', tipo: 'claro' },
    { id: 'areia', nome: 'Areia', tipo: 'claro' },
    { id: 'contraste', nome: 'Alto contraste', tipo: 'escuro' },
  ];
  const valido = (id) => temas.some((t) => t.id === id);

  // 'sistema' e qualquer valor desconhecido seguem o claro/escuro do sistema
  function resolver(escolha, escuroNoSistema) {
    if (valido(escolha)) return escolha;
    return escuroNoSistema ? 'grafite' : 'papel';
  }

  // aba anônima ou armazenamento bloqueado lançam exceção: vale 'sistema'
  function escolha() {
    try {
      const v = globalThis.localStorage.getItem(CHAVE);
      return valido(v) ? v : 'sistema';
    } catch {
      return 'sistema';
    }
  }

  const escuro = () => globalThis.matchMedia('(prefers-color-scheme: dark)').matches;

  function aplicar(e) {
    try {
      globalThis.localStorage.setItem(CHAVE, e);
    } catch {} // sem armazenamento o tema vale só nesta página
    document.documentElement.dataset.theme = resolver(e, escuro());
  }

  const PadTheme = { temas, resolver, aplicar, escolha };
  if (typeof module !== 'undefined') module.exports = PadTheme;
  if (typeof document === 'undefined') return;
  globalThis.PadTheme = PadTheme;
  // o sistema trocou de claro para escuro (ou o contrário): só importa para quem segue o sistema
  globalThis.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => {
    if (escolha() === 'sistema') aplicar('sistema');
  });
})();
