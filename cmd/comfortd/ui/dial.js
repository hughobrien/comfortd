// SPDX-License-Identifier: GPL-3.0-or-later
//
// Temperature wheel: drag (or arrow-key) a handle round the ring; on release a
// "tempcommit" event carries {path, v} to the datastar handler on the <svg>.
// Delegated from document so it survives datastar morphing the card.
(() => {
  const RAD = Math.PI / 180, START = 210, SWEEP = 300;
  const num = (el, k) => parseFloat(el.dataset[k]);
  const ang = (svg, v) => START + SWEEP * (v - num(svg, 'min')) / (num(svg, 'max') - num(svg, 'min'));
  const pt = (svg, r, a) => [num(svg, 'cx') + r * Math.sin(a * RAD), num(svg, 'cy') - r * Math.cos(a * RAD)];
  const arc = (svg, v0, v1) => {
    const r = num(svg, 'r'), a0 = ang(svg, v0), a1 = ang(svg, v1);
    if (a1 <= a0) return '';
    const [x0, y0] = pt(svg, r, a0), [x1, y1] = pt(svg, r, a1);
    return `M${x0.toFixed(2)} ${y0.toFixed(2)}A${r} ${r} 0 ${a1 - a0 > 180 ? 1 : 0} 1 ${x1.toFixed(2)} ${y1.toFixed(2)}`;
  };
  const handles = (svg) => [...svg.querySelectorAll('[data-handle]')];
  const val = (h) => parseFloat(h.dataset.v);

  function render(svg) {
    const hs = handles(svg), min = num(svg, 'min');
    hs.forEach((h) => {
      const [x, y] = pt(svg, num(svg, 'r'), ang(svg, val(h)));
      h.setAttribute('cx', x.toFixed(2));
      h.setAttribute('cy', y.toFixed(2));
      h.setAttribute('aria-valuenow', h.dataset.v);
    });
    const fill = svg.querySelector('.fill');
    if (fill) fill.setAttribute('d', hs.length > 1 ? arc(svg, val(hs[0]), val(hs[1])) : arc(svg, min, val(hs[0])));
    const c = svg.querySelector('.tval');
    if (c) c.textContent = hs.map((h) => h.dataset.v).join(' – ');
  }

  function fromPointer(svg, e) {
    const r = svg.getBoundingClientRect(), vb = svg.viewBox.baseVal;
    const dx = (e.clientX - r.left) * vb.width / r.width - num(svg, 'cx');
    const dy = (e.clientY - r.top) * vb.height / r.height - num(svg, 'cy');
    let rel = ((Math.atan2(dx, -dy) / RAD) - START + 720) % 360;
    if (rel > SWEEP) rel = rel > SWEEP + (360 - SWEEP) / 2 ? 0 : SWEEP; // bottom gap: snap to the nearer end
    const min = num(svg, 'min'), max = num(svg, 'max'), step = num(svg, 'step');
    const v = Math.round((min + rel / SWEEP * (max - min)) / step) * step;
    return Math.min(max, Math.max(min, v));
  }

  // set moves a handle, pushing the other if they would come closer than the
  // unit's minimum auto gap (the controller does the same server-side).
  function set(svg, h, v) {
    const hs = handles(svg), min = num(svg, 'min'), max = num(svg, 'max'), gap = num(svg, 'gap');
    if (hs.length === 2) {
      const [lo, hi] = hs;
      if (h === lo) v = Math.min(v, max - gap); else v = Math.max(v, min + gap);
      h.dataset.v = v;
      if (h === lo && val(hi) < v + gap) hi.dataset.v = v + gap;
      if (h === hi && val(lo) > v - gap) lo.dataset.v = v - gap;
    } else h.dataset.v = v;
    render(svg);
  }

  const commit = (svg, h) => svg.dispatchEvent(new CustomEvent('tempcommit', { detail: { path: h.dataset.path, v: val(h) } }));

  document.addEventListener('pointerdown', (e) => {
    const svg = e.target.closest && e.target.closest('svg.tdial');
    if (!svg || e.button > 0) return;
    const hs = handles(svg), v = fromPointer(svg, e);
    const h = hs.reduce((a, b) => (Math.abs(val(b) - v) < Math.abs(val(a) - v) ? b : a));
    svg.setPointerCapture(e.pointerId);
    svg._drag = h;
    set(svg, h, v);
    e.preventDefault();
  });
  document.addEventListener('pointermove', (e) => {
    const svg = e.target.closest && e.target.closest('svg.tdial');
    if (svg && svg._drag) set(svg, svg._drag, fromPointer(svg, e));
  });
  const end = (e) => {
    const svg = e.target.closest && e.target.closest('svg.tdial');
    if (!svg || !svg._drag) return;
    const h = svg._drag;
    svg._drag = null;
    commit(svg, h);
  };
  document.addEventListener('pointerup', end);
  document.addEventListener('pointercancel', (e) => {
    const svg = e.target.closest && e.target.closest('svg.tdial');
    if (svg) svg._drag = null;
  });
  document.addEventListener('keydown', (e) => {
    const h = e.target.closest && e.target.closest('[data-handle]');
    if (!h) return;
    const svg = h.closest('svg.tdial'), step = num(svg, 'step');
    const d = { ArrowUp: 1, ArrowRight: 1, ArrowDown: -1, ArrowLeft: -1 }[e.key];
    if (!d) return;
    e.preventDefault();
    set(svg, h, Math.min(num(svg, 'max'), Math.max(num(svg, 'min'), val(h) + d * step)));
    commit(svg, h);
  });
})();
