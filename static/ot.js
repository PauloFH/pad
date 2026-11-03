// Operações de texto concorrentes (OT), gêmeo de ot.go. Operação = array:
// número positivo = retain, negativo = delete, string = insert. Posições em
// unidades UTF-16 (as do textarea e de String.length).
(function () {
  const isHigh = (u) => u >= 0xd800 && u < 0xdc00;
  const isLow = (u) => u >= 0xdc00 && u < 0xe000;
  const isIns = (c) => typeof c === 'string';

  // junta vizinhos do mesmo tipo e descarta componentes vazios
  function push(o, c) {
    if (c === 0 || c === '') return;
    const l = o.length - 1;
    if (l >= 0 && isIns(c) && isIns(o[l])) o[l] += c;
    else if (l >= 0 && !isIns(c) && !isIns(o[l]) && c > 0 === o[l] > 0) o[l] += c;
    else o.push(c);
  }

  const baseLen = (op) => op.reduce((n, c) => n + (isIns(c) ? 0 : Math.abs(c)), 0);

  // Recusa tamanho errado e fronteira entre as duas metades de um par substituto.
  function apply(text, op) {
    if (baseLen(op) !== text.length) throw new Error(`operação para ${baseLen(op)} unidades, documento tem ${text.length}`);
    let out = '', pos = 0;
    for (const c of op) {
      if (isIns(c)) { out += c; continue; }
      if (!Number.isInteger(c) || c === 0) throw new Error('componente inválido');
      if (c > 0) out += text.slice(pos, pos + c);
      pos += Math.abs(c);
      if (pos > 0 && pos < text.length && isHigh(text.charCodeAt(pos - 1)) && isLow(text.charCodeAt(pos))) {
        throw new Error('operação corta um par substituto');
      }
    }
    return out;
  }

  // Percorre uma operação podendo consumir parte de um retain/delete/insert.
  function iter(op) {
    let i = 0, off = 0;
    return {
      done: () => i >= op.length,
      // componente atual sem o trecho já consumido
      cur() {
        const c = op[i];
        return isIns(c) ? c.slice(off) : c > 0 ? c - off : c + off;
      },
      take(n) { // consome n unidades do componente atual (n <= tamanho dele)
        const c = op[i];
        off += n;
        if (off >= (isIns(c) ? c.length : Math.abs(c))) { i++; off = 0; }
      },
    };
  }
  const len = (c) => (isIns(c) ? c.length : Math.abs(c));

  // Mesmo desempate do Go: no empate de insert, o de a fica à esquerda.
  function transform(a, b) {
    if (baseLen(a) !== baseLen(b)) throw new Error('operações sobre documentos diferentes');
    const a2 = [], b2 = [], ia = iter(a), ib = iter(b);
    while (!ia.done() || !ib.done()) {
      if (!ia.done() && isIns(ia.cur())) {
        const c = ia.cur();
        push(a2, c); push(b2, c.length); ia.take(c.length);
        continue;
      }
      if (!ib.done() && isIns(ib.cur())) {
        const c = ib.cur();
        push(a2, c.length); push(b2, c); ib.take(c.length);
        continue;
      }
      if (ia.done() || ib.done()) throw new Error('operações de tamanhos diferentes');
      const ca = ia.cur(), cb = ib.cur();
      const n = Math.min(len(ca), len(cb));
      if (ca > 0 && cb > 0) { push(a2, n); push(b2, n); }
      else if (ca < 0 && cb > 0) push(a2, -n);
      else if (ca > 0 && cb < 0) push(b2, -n);
      ia.take(n); ib.take(n);
    }
    return [a2, b2];
  }

  // apply(apply(t,a),b) === apply(t, compose(a,b))
  function compose(a, b) {
    if (a.reduce((n, c) => n + (isIns(c) ? c.length : c > 0 ? c : 0), 0) !== baseLen(b)) {
      throw new Error('operações não encadeiam');
    }
    const o = [], ia = iter(a), ib = iter(b);
    while (!ia.done() || !ib.done()) {
      if (!ia.done() && ia.cur() < 0) { const c = ia.cur(); push(o, c); ia.take(-c); continue; }
      if (!ib.done() && isIns(ib.cur())) { const c = ib.cur(); push(o, c); ib.take(c.length); continue; }
      if (ia.done() || ib.done()) throw new Error('operações de tamanhos diferentes');
      const ca = ia.cur(), cb = ib.cur(), n = Math.min(len(ca), len(cb));
      if (isIns(ca)) { if (cb > 0) push(o, ca.slice(0, n)); } // insert de a: mantido ou apagado por b
      else push(o, cb > 0 ? n : -n);
      ia.take(n); ib.take(n);
    }
    return o;
  }

  // desfaz op, que foi aplicada sobre textBefore
  function invert(op, textBefore) {
    const o = [];
    let pos = 0;
    for (const c of op) {
      if (isIns(c)) push(o, -c.length);
      else if (c > 0) { push(o, c); pos += c; }
      else { push(o, textBefore.slice(pos, pos - c)); pos -= c; }
    }
    return o;
  }

  // Com insert exatamente em i, a posição só anda para a direita se stickRight.
  function transformIndex(i, op, stickRight) {
    let res = i, pos = 0;
    for (const c of op) {
      if (isIns(c)) { if (pos < i || (pos === i && stickRight)) res += c.length; }
      else if (c < 0) { if (pos < i) res -= Math.min(-c, i - pos); pos -= c; }
      else pos += c;
    }
    return res;
  }

  // Apaga alguma unidade de [from,to) ou insere estritamente dentro (nas bordas não conta).
  function touchesRange(op, from, to) {
    let pos = 0;
    for (const c of op) {
      if (isIns(c)) { if (from < pos && pos < to) return true; }
      else if (c < 0) { if (pos < to && pos - c > from) return true; pos -= c; }
      else pos += c;
    }
    return false;
  }

  // Operação mínima oldText -> newText. O sufixo comum não passa do cursor
  // (cursorAfter), então numa repetição ("aa" -> "aaa") a inserção cai onde o usuário digitou.
  function diff(oldText, newText, cursorAfter) {
    const m = Math.min(oldText.length, newText.length);
    let s = 0;
    const maxS = Math.max(0, Math.min(m, newText.length - cursorAfter));
    while (s < maxS && oldText[oldText.length - 1 - s] === newText[newText.length - 1 - s]) s++;
    // não deixa a borda do trecho trocado partir um par substituto
    const cut = (t, k) => k > 0 && k < t.length && isHigh(t.charCodeAt(k - 1)) && isLow(t.charCodeAt(k));
    if (s > 0 && (cut(oldText, oldText.length - s) || cut(newText, newText.length - s))) s--;
    let p = 0;
    while (p < m - s && oldText[p] === newText[p]) p++;
    if (p > 0 && (cut(oldText, p) || cut(newText, p))) p--;
    const o = [];
    push(o, p);
    push(o, -(oldText.length - p - s));
    push(o, newText.slice(p, newText.length - s));
    push(o, s);
    return o;
  }

  // sem componentes, ou só um retain
  const isNoop = (op) => op.every((c) => !isIns(c) && c > 0) && op.length <= 1;

  const PadOT = { apply, transform, compose, invert, transformIndex, touchesRange, diff, isNoop };
  globalThis.PadOT = PadOT;
  if (typeof module !== 'undefined') module.exports = PadOT;
})();
