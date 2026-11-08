// SPDX-License-Identifier: GPL-3.0-or-later
//
// Drag a unit's heading to reorder the cards; the new order is saved on the
// server. Delegated from document so it survives datastar patching the cards.
(() => {
  let dragged = null;
  const units = () => document.getElementById('units');
  const handle = (e) => e.target.closest && e.target.closest('section.unit > h2');

  document.addEventListener('dragstart', (e) => {
    const h = handle(e);
    if (!h) return;
    dragged = h.parentElement.id;
    e.dataTransfer.effectAllowed = 'move';
    e.dataTransfer.setData('text/plain', dragged);
    h.parentElement.classList.add('dragging');
  });
  document.addEventListener('dragover', (e) => {
    if (!dragged) return;
    const over = e.target.closest && e.target.closest('section.unit');
    if (!over || over.id === dragged) return;
    e.preventDefault();
    const from = document.getElementById(dragged);
    const r = over.getBoundingClientRect();
    // Cards sit side by side or stacked: compare along the axis they differ on.
    const sameRow = Math.abs(over.offsetTop - from.offsetTop) < 4;
    const after = sameRow ? e.clientX > r.left + r.width / 2 : e.clientY > r.top + r.height / 2;
    over.parentElement.insertBefore(from, after ? over.nextSibling : over);
  });
  document.addEventListener('dragend', () => {
    if (!dragged) return;
    const el = document.getElementById(dragged);
    if (el) el.classList.remove('dragging');
    dragged = null;
    const order = [...units().querySelectorAll('section.unit')].map((s) => s.id.replace(/^unit-/, ''));
    fetch('/ui/order', {
      method: 'POST',
      headers: {'Datastar-Request': 'true', 'Content-Type': 'application/json'},
      body: JSON.stringify({order}),
    });
  });
})();
