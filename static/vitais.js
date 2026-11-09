// Web Vitals de quem usa o pad, medidas com as APIs do próprio navegador (sem biblioteca) e mandadas uma vez,
// quando a página sai de vista, para /_vitais, que as grava no log do servidor pelo tipo da página.
(function () {
  if (typeof PerformanceObserver === 'undefined' || !navigator.sendBeacon) return;
  const v = { pagina: location.pathname, lcp: 0, inp: 0, cls: 0, fcp: 0, ttfb: 0 };
  const ver = (opcoes, f) => {
    try { new PerformanceObserver((l) => l.getEntries().forEach(f)).observe({ buffered: true, ...opcoes }); } catch {} // tipo que o navegador não conhece
  };
  ver({ type: 'largest-contentful-paint' }, (e) => { v.lcp = e.startTime; });
  ver({ type: 'paint' }, (e) => { if (e.name === 'first-contentful-paint') v.fcp = e.startTime; });
  // shortcut: soma todos os deslocamentos, sem as janelas de sessão do CLS oficial; superestima páginas abertas por muito tempo
  ver({ type: 'layout-shift' }, (e) => { if (!e.hadRecentInput) v.cls += e.value; });
  // shortcut: a pior interação, e não o percentil 98 do INP oficial; só difere com mais de 50 interações
  ver({ type: 'event', durationThreshold: 16 }, (e) => { if (e.interactionId) v.inp = Math.max(v.inp, e.duration); });
  const nav = performance.getEntriesByType('navigation')[0];
  if (nav) v.ttfb = nav.responseStart;
  let enviado = false;
  document.addEventListener('visibilitychange', () => {
    if (enviado || document.visibilityState !== 'hidden') return;
    enviado = true;
    navigator.sendBeacon('/_vitais', new Blob([JSON.stringify(v)], { type: 'application/json' }));
  });
})();
