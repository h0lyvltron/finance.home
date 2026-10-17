/**
 * Chart.js lifecycle adapter for Datastar patches.
 * Finds [data-chart] nodes with JSON config in <script type="application/json">
 * or data-chart-config attribute; destroys previous Chart instance before recreate.
 */
(function () {
  const registry = new WeakMap();

  function parseConfig(el) {
    const script = el.querySelector('script[type="application/json"]');
    if (script) {
      try { return JSON.parse(script.textContent); } catch (e) { console.warn('chart config', e); return null; }
    }
    const raw = el.getAttribute('data-chart-config');
    if (raw) {
      try { return JSON.parse(raw); } catch (e) { console.warn('chart config', e); return null; }
    }
    return null;
  }

  function mount(el) {
    if (typeof Chart === 'undefined') return;
    const canvas = el.querySelector('canvas');
    if (!canvas) return;
    const cfg = parseConfig(el);
    if (!cfg) return;

    const prev = registry.get(el);
    if (prev) {
      try { prev.destroy(); } catch (_) {}
      registry.delete(el);
    }
    // Also clear Chart.js global registry if canvas was remounted
    const existing = Chart.getChart(canvas);
    if (existing) existing.destroy();

    const chart = new Chart(canvas.getContext('2d'), cfg);
    registry.set(el, chart);
  }

  function scan(root) {
    (root || document).querySelectorAll('[data-chart]').forEach(mount);
  }

  document.addEventListener('DOMContentLoaded', () => scan(document));

  // Datastar morph / patch hooks
  document.addEventListener('datastar-fetch', () => {
    requestAnimationFrame(() => scan(document));
  });
  const mo = new MutationObserver((mutations) => {
    for (const m of mutations) {
      m.addedNodes.forEach((n) => {
        if (n.nodeType !== 1) return;
        if (n.matches && n.matches('[data-chart]')) mount(n);
        else if (n.querySelectorAll) scan(n);
      });
    }
  });
  mo.observe(document.documentElement, { childList: true, subtree: true });

  window.FinanceCharts = { scan, mount };
})();
