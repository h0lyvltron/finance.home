/**
 * Home dashboard: keep scroll across edits, and drag widgets while editing.
 */
(function () {
  const scrollKey = 'finance-home-scroll';

  function here() {
    return location.pathname + location.search;
  }

  function rememberScroll(url) {
    try {
      sessionStorage.setItem(scrollKey, JSON.stringify({ url: url || here(), y: window.scrollY }));
    } catch (_) {}
  }

  try {
    const raw = sessionStorage.getItem(scrollKey);
    if (raw) {
      sessionStorage.removeItem(scrollKey);
      const saved = JSON.parse(raw);
      if (saved && saved.url === here()) {
        const y = saved.y || 0;
        const apply = function () { window.scrollTo(0, y); };
        apply();
        requestAnimationFrame(apply);
        window.addEventListener('load', apply, { once: true });
      }
    }
  } catch (_) {}

  document.addEventListener('submit', function (event) {
    const form = event.target;
    if (!form || !form.getAttribute) return;
    if ((form.getAttribute('method') || 'get').toLowerCase() !== 'post') return;
    const ret = form.querySelector('input[name="return"]');
    let dest = ret && ret.value ? ret.value : here();
    dest = dest.split('#')[0];
    if (dest !== '/' && dest.indexOf('/?') !== 0) return;
    rememberScroll(dest);
  }, true);

  const root = document.querySelector('.dash-widgets.is-editing');
  if (!root) return;

  let drag = null;

  function widgetIds() {
    return [...root.querySelectorAll('.dash-widget')].map(function (el) {
      return el.getAttribute('data-widget-id');
    });
  }

  function clearGhost(article) {
    article.classList.remove('is-dragging');
    article.style.position = '';
    article.style.left = '';
    article.style.top = '';
    article.style.width = '';
    article.style.height = '';
    article.style.zIndex = '';
  }

  function dropBefore(widget, x, y) {
    const rect = widget.getBoundingClientRect();
    const stacked = rect.width >= root.clientWidth * 0.9;
    if (stacked) {
      return y < rect.top + rect.height / 2;
    }
    const relY = rect.height ? (y - rect.top) / rect.height : 0;
    if (relY < 0.35) return true;
    if (relY > 0.65) return false;
    return x < rect.left + rect.width / 2;
  }

  function widgetUnder(x, y, article) {
    const stack = document.elementsFromPoint(x, y);
    for (let i = 0; i < stack.length; i++) {
      const el = stack[i];
      if (el === article || article.contains(el)) continue;
      if (!el.closest) continue;
      const widget = el.closest('.dash-widget');
      if (widget && widget !== article && root.contains(widget)) return widget;
    }
    return null;
  }

  function place(article, placeholder, x, y) {
    const target = widgetUnder(x, y, article);
    if (!target) return;
    const before = dropBefore(target, x, y);
    if (before) {
      if (target.previousSibling !== placeholder) root.insertBefore(placeholder, target);
    } else if (target.nextSibling !== placeholder) {
      root.insertBefore(placeholder, target.nextSibling);
    }
  }

  function syncMoveButtons() {
    const widgets = [...root.querySelectorAll('.dash-widget')];
    widgets.forEach(function (el, i) {
      const up = el.querySelector('button[aria-label="Move up"]');
      const down = el.querySelector('button[aria-label="Move down"]');
      if (up) up.disabled = i === 0;
      if (down) down.disabled = i === widgets.length - 1;
    });
  }

  function resizeCharts() {
    if (typeof Chart === 'undefined') return;
    root.querySelectorAll('canvas').forEach(function (canvas) {
      const chart = Chart.getChart(canvas);
      if (chart) chart.resize();
    });
  }

  function persist(order) {
    const csrfEl = document.querySelector('input[name="csrf"]');
    const body = new URLSearchParams();
    if (csrfEl) body.set('csrf', csrfEl.value);
    body.set('order', order.join(','));
    return fetch('/dashboard/widgets/reorder', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/x-www-form-urlencoded',
        'X-Requested-With': 'fetch',
      },
      body: body,
    }).then(function (res) {
      if (res.ok) {
        syncMoveButtons();
        resizeCharts();
        return;
      }
      rememberScroll(here());
      location.reload();
    });
  }

  root.addEventListener('pointerdown', function (event) {
    const grip = event.target.closest ? event.target.closest('.dash-drag-grip') : null;
    if (!grip || !root.contains(grip)) return;
    const article = grip.closest('.dash-widget');
    if (!article) return;
    event.preventDefault();
    const rect = article.getBoundingClientRect();
    const placeholder = document.createElement('div');
    const spans = [...article.classList].filter(function (c) {
      return c.indexOf('width-') === 0 || c === 'dash-widget-hero';
    });
    placeholder.className = 'dash-drop-placeholder ' + spans.join(' ');
    placeholder.style.minHeight = rect.height + 'px';
    drag = {
      article: article,
      grip: grip,
      placeholder: placeholder,
      pointerId: event.pointerId,
      dx: event.clientX - rect.left,
      dy: event.clientY - rect.top,
      startX: event.clientX,
      startY: event.clientY,
      origin: widgetIds(),
      active: false,
      width: rect.width,
      height: rect.height,
    };
    grip.setPointerCapture(event.pointerId);
  });

  root.addEventListener('pointermove', function (event) {
    if (!drag || event.pointerId !== drag.pointerId) return;
    if (!drag.active) {
      const moved = Math.hypot(event.clientX - drag.startX, event.clientY - drag.startY);
      if (moved < 4) return;
      drag.active = true;
      root.insertBefore(drag.placeholder, drag.article);
      drag.article.classList.add('is-dragging');
      drag.article.style.position = 'fixed';
      drag.article.style.width = drag.width + 'px';
      drag.article.style.height = drag.height + 'px';
      drag.article.style.zIndex = '80';
    }
    drag.article.style.left = (event.clientX - drag.dx) + 'px';
    drag.article.style.top = (event.clientY - drag.dy) + 'px';
    place(drag.article, drag.placeholder, event.clientX, event.clientY);
  });

  function finish(event) {
    if (!drag || event.pointerId !== drag.pointerId) return;
    const state = drag;
    drag = null;
    try { state.grip.releasePointerCapture(event.pointerId); } catch (_) {}
    if (!state.active) return;
    state.article.parentNode.insertBefore(state.article, state.placeholder);
    state.placeholder.remove();
    clearGhost(state.article);
    const next = widgetIds();
    if (next.join(',') === state.origin.join(',')) return;
    persist(next).catch(function () { location.reload(); });
  }

  function cancel(event) {
    if (!drag || (event && event.pointerId !== undefined && event.pointerId !== drag.pointerId)) return;
    const state = drag;
    drag = null;
    if (state.placeholder.parentNode) state.placeholder.remove();
    clearGhost(state.article);
  }

  root.addEventListener('pointerup', finish);
  root.addEventListener('pointercancel', cancel);
  document.addEventListener('keydown', function (event) {
    if (event.key === 'Escape') cancel();
  });
})();
