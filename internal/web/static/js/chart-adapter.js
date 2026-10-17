/**
 * Chart.js lifecycle adapter for Datastar patches.
 * Finds [data-chart] nodes with JSON config in <script type="application/json">
 * or data-chart-config attribute; destroys previous Chart instance before recreate.
 * Resolves var:* color tokens from the page theme and formats currency or percent.
 */
(function () {
  const registry = new WeakMap();

  const tokens = {
    income: ['--accent', 0.85],
    'income-dim': ['--accent', 0.38],
    expense: ['--danger', 0.8],
    'expense-dim': ['--danger', 0.38],
    net: ['--accent', 1],
    'net-fill': ['--accent', 0.16],
    compare: ['--ink-faint', 0.95],
    budget: ['--accent', 0.45],
    danger: ['--danger', 0.88],
    muted: ['--ink-muted', 0.85],
  };

  function cssVar(name) {
    return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
  }

  function hexToRgba(hex, alpha) {
    let h = hex.replace('#', '');
    if (h.length === 3) {
      h = h.split('').map(function (c) { return c + c; }).join('');
    }
    const n = parseInt(h, 16);
    if (!isFinite(n)) return hex;
    const r = (n >> 16) & 255;
    const g = (n >> 8) & 255;
    const b = n & 255;
    return 'rgba(' + r + ', ' + g + ', ' + b + ', ' + alpha + ')';
  }

  function resolveToken(token) {
    const spec = tokens[token.slice(4)];
    if (!spec) return token;
    const raw = cssVar(spec[0]);
    if (/^#([0-9a-f]{3}|[0-9a-f]{6})$/i.test(raw)) return hexToRgba(raw, spec[1]);
    return raw || token;
  }

  function mapColor(value) {
    if (Array.isArray(value)) return value.map(mapColor);
    if (typeof value === 'string' && value.indexOf('var:') === 0) return resolveToken(value);
    return value;
  }

  function paintDatasets(cfg) {
    const sets = cfg.data && cfg.data.datasets;
    if (!sets) return;
    sets.forEach(function (ds) {
      ['backgroundColor', 'borderColor', 'pointBackgroundColor', 'pointBorderColor'].forEach(function (key) {
        if (ds[key] != null) ds[key] = mapColor(ds[key]);
      });
    });
  }

  function money(v, precise) {
    const n = Number(v);
    if (!isFinite(n)) return '';
    const abs = Math.abs(n);
    const digits = precise || abs < 100 ? 2 : 0;
    const body = abs.toLocaleString(undefined, {
      minimumFractionDigits: digits,
      maximumFractionDigits: digits,
    });
    return (n < 0 ? '-$' : '$') + body;
  }

  function percent(v) {
    const n = Number(v);
    if (!isFinite(n)) return '';
    return n.toLocaleString(undefined, { maximumFractionDigits: 1 }) + '%';
  }

  function formatValue(v, format, precise) {
    if (format === 'percent') return percent(v);
    return money(v, precise);
  }

  function parsedNumber(ctx) {
    const p = ctx.parsed || {};
    const axis = (ctx.chart.options.indexAxis || 'x') === 'y' ? 'x' : 'y';
    const v = p[axis];
    return typeof v === 'number' ? v : Number(v);
  }

  function applyTheme(cfg) {
    const finance = cfg.finance || {};
    delete cfg.finance;
    const format = finance.format || 'currency';
    const share = !!finance.share;
    paintDatasets(cfg);

    cfg.options = cfg.options || {};
    cfg.options.plugins = cfg.options.plugins || {};
    const ink = cssVar('--ink-muted') || '#5c574e';
    const line = cssVar('--line') || '#d4cfc3';
    const font = cssVar('--font') || 'sans-serif';
    const grid = /^#/.test(line) ? hexToRgba(line, 0.9) : line;
    cfg.options.color = ink;
    cfg.options.font = { family: font };

    const indexAxis = cfg.options.indexAxis || 'x';
    const valueAxis = indexAxis === 'y' ? 'x' : 'y';
    cfg.options.scales = cfg.options.scales || {};
    Object.keys(cfg.options.scales).forEach(function (key) {
      const scale = cfg.options.scales[key] || {};
      cfg.options.scales[key] = scale;
      scale.ticks = Object.assign({ color: ink, font: { family: font } }, scale.ticks || {});
      scale.grid = Object.assign({ color: grid }, scale.grid || {});
      scale.border = Object.assign({ color: grid }, scale.border || {});
      if (key === valueAxis || key === 'y1') {
        const prior = scale.ticks.callback;
        if (!prior) {
          scale.ticks.callback = function (value) {
            return formatValue(value, format, false);
          };
        }
      }
    });

    const legend = cfg.options.plugins.legend || {};
    cfg.options.plugins.legend = legend;
    legend.labels = Object.assign({
      color: ink,
      font: { family: font },
      boxWidth: 12,
    }, legend.labels || {});

    cfg.options.plugins.tooltip = cfg.options.plugins.tooltip || {};
    cfg.options.plugins.tooltip.callbacks = {
      label: function (ctx) {
        const n = parsedNumber(ctx);
        const name = ctx.dataset && ctx.dataset.label ? ctx.dataset.label + ': ' : '';
        if (!isFinite(n)) return name.replace(/:\s$/, '');
        let text = name + formatValue(n, format, true);
        if (share && ctx.dataset && Array.isArray(ctx.dataset.data)) {
          let sum = 0;
          ctx.dataset.data.forEach(function (v) {
            const num = Number(v);
            if (isFinite(num)) sum += Math.abs(num);
          });
          if (sum > 0) text += ' (' + Math.round(Math.abs(n) / sum * 100) + '%)';
        }
        return text;
      },
    };
    return cfg;
  }

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
    applyTheme(cfg);

    const prev = registry.get(el);
    if (prev) {
      try { prev.destroy(); } catch (_) {}
      registry.delete(el);
    }
    const existing = Chart.getChart(canvas);
    if (existing) existing.destroy();

    const chart = new Chart(canvas.getContext('2d'), cfg);
    registry.set(el, chart);
  }

  function scan(root) {
    (root || document).querySelectorAll('[data-chart]').forEach(mount);
  }

  document.addEventListener('DOMContentLoaded', function () { scan(document); });

  document.addEventListener('datastar-fetch', function () {
    requestAnimationFrame(function () { scan(document); });
  });
  const mo = new MutationObserver(function (mutations) {
    for (const m of mutations) {
      m.addedNodes.forEach(function (n) {
        if (n.nodeType !== 1) return;
        if (n.matches && n.matches('[data-chart]')) mount(n);
        else if (n.querySelectorAll) scan(n);
      });
    }
  });
  mo.observe(document.documentElement, { childList: true, subtree: true });

  window.FinanceCharts = { scan, mount };
})();
