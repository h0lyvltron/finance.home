/**
 * Home dashboard: keep scroll across edits; in edit mode, place widgets on the
 * 12-column cell grid by dragging, corner-resizing, arrow keys or the tap pad.
 * The server resolves overlaps (placed widget wins, others slide down).
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

  const COLS = 12, MIN_W = 2, MIN_H = 1, MAX_H = 16;
  const section = root.closest('.home-dashboard');
  const pad = document.querySelector('.dash-pad');
  let selected = null;
  let drag = null;
  let cellRows = 0;
  let queue = Promise.resolve();
  let seq = 0;

  function widgets() {
    return [...root.querySelectorAll('.dash-widget')];
  }

  function cellsOf(el) {
    return { col: +el.dataset.col, row: +el.dataset.row, w: +el.dataset.w, h: +el.dataset.h };
  }

  function setVars(el, c) {
    el.style.setProperty('--c', c.col);
    el.style.setProperty('--r', c.row);
    el.style.setProperty('--w', c.w);
    el.style.setProperty('--h', c.h);
  }

  function apply(el, c) {
    el.dataset.col = c.col;
    el.dataset.row = c.row;
    el.dataset.w = c.w;
    el.dataset.h = c.h;
    setVars(el, c);
    const size = el.querySelector('.dash-widget-size');
    if (size) size.textContent = c.w + '×' + c.h;
  }

  function clamp(c) {
    const w = Math.min(COLS, Math.max(MIN_W, c.w));
    const h = Math.min(MAX_H, Math.max(MIN_H, c.h));
    return { col: Math.min(COLS - w + 1, Math.max(1, c.col)), row: Math.max(1, c.row), w: w, h: h };
  }

  function same(a, b) {
    return a.col === b.col && a.row === b.row && a.w === b.w && a.h === b.h;
  }

  function overlaps(a, b) {
    return a.col < b.col + b.w && b.col < a.col + a.w && a.row < b.row + b.h && b.row < a.row + a.h;
  }

  function bottom() {
    return widgets().reduce(function (max, el) {
      const c = cellsOf(el);
      return Math.max(max, c.row + c.h - 1);
    }, 0);
  }

  function drawCells(minRows) {
    const rows = Math.max(bottom(), minRows || 0) + 3;
    if (rows > cellRows) {
      const frag = document.createDocumentFragment();
      for (let r = cellRows + 1; r <= rows; r++) {
        for (let c = 1; c <= COLS; c++) {
          const cell = document.createElement('div');
          cell.className = 'dash-cell';
          cell.setAttribute('aria-hidden', 'true');
          cell.dataset.row = r;
          setVars(cell, { col: c, row: r, w: 1, h: 1 });
          frag.appendChild(cell);
        }
      }
      root.insertBefore(frag, root.firstChild);
    } else if (rows < cellRows) {
      root.querySelectorAll('.dash-cell').forEach(function (cell) {
        if (+cell.dataset.row > rows) cell.remove();
      });
    }
    cellRows = rows;
  }

  function metrics() {
    const first = root.querySelector('.dash-cell');
    const box = first.getBoundingClientRect();
    const style = getComputedStyle(root);
    const colGap = parseFloat(style.columnGap) || 0;
    const rowGap = parseFloat(style.rowGap) || 0;
    const rect = root.getBoundingClientRect();
    return { left: rect.left, top: rect.top, colStep: box.width + colGap, rowStep: box.height + rowGap, colGap: colGap, rowGap: rowGap };
  }

  function csrfToken() {
    const el = document.querySelector('input[name="csrf"]');
    return el ? el.value : '';
  }

  function applyLayout(list) {
    const byId = new Map(widgets().map(function (el) { return [el.dataset.widgetId, el]; }));
    const focus = document.activeElement;
    const before = widgets().map(function (el) { return el.dataset.widgetId; }).join(',');
    list.forEach(function (c) {
      const el = byId.get(String(c.id));
      if (el) apply(el, c);
    });
    const after = list.map(function (c) { return String(c.id); }).join(',');
    if (after !== before) {
      list.forEach(function (c) {
        const el = byId.get(String(c.id));
        if (el) root.appendChild(el);
      });
      if (focus && root.contains(focus)) focus.focus({ preventScroll: true });
    }
    drawCells();
    syncPad();
  }

  function save(article, c) {
    apply(article, c);
    drawCells();
    syncPad();
    const id = article.dataset.widgetId;
    const mine = ++seq;
    queue = queue.then(function () {
      const body = new URLSearchParams();
      body.set('csrf', csrfToken());
      body.set('col', c.col);
      body.set('row', c.row);
      body.set('w', c.w);
      body.set('h', c.h);
      return fetch('/dashboard/widgets/' + id + '/place', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/x-www-form-urlencoded',
          'X-Requested-With': 'fetch',
          'X-CSRF-Token': csrfToken(),
        },
        body: body,
      }).then(function (res) {
        if (!res.ok) throw new Error('place ' + res.status);
        return res.json();
      }).then(function (data) {
        if (mine === seq) applyLayout(data.widgets || []);
      });
    }).catch(function () {
      rememberScroll(here());
      location.reload();
    });
  }

  function syncPad() {
    if (!pad) return;
    if (!selected || !root.contains(selected)) {
      pad.hidden = true;
      if (section) section.classList.remove('has-pad');
      return;
    }
    const c = cellsOf(selected);
    pad.hidden = false;
    if (section) section.classList.add('has-pad');
    pad.querySelector('.dash-pad-name').textContent = selected.dataset.label || 'Widget';
    pad.querySelector('.dash-pad-pos').textContent = 'col ' + c.col + ' · row ' + c.row + ' · ' + c.w + '×' + c.h;
    pad.querySelector('.dash-pad-remove').action = '/dashboard/widgets/' + selected.dataset.widgetId + '/delete';
  }

  function select(article) {
    if (selected === article) return;
    if (selected) selected.classList.remove('is-selected');
    selected = article;
    if (selected) selected.classList.add('is-selected');
    syncPad();
  }

  function nudge(article, mode, dx, dy) {
    const c = cellsOf(article);
    const next = mode === 'size'
      ? clamp({ col: c.col, row: c.row, w: c.w + dx, h: c.h + dy })
      : clamp({ col: c.col + dx, row: c.row + dy, w: c.w, h: c.h });
    if (!same(c, next)) save(article, next);
  }

  function markCollisions(target, article) {
    widgets().forEach(function (el) {
      el.classList.toggle('will-move', el !== article && overlaps(target, cellsOf(el)));
    });
  }

  function targetFor(event) {
    const m = metrics();
    const s = drag.start;
    if (drag.mode === 'resize') {
      const left = m.left + (s.col - 1) * m.colStep;
      const top = m.top + (s.row - 1) * m.rowStep;
      return clamp({
        col: s.col,
        row: s.row,
        w: Math.round((event.clientX - left + m.colGap) / m.colStep),
        h: Math.round((event.clientY - top + m.rowGap) / m.rowStep),
      });
    }
    const x = event.clientX - drag.grabX - m.left;
    const y = event.clientY - drag.grabY - m.top;
    const target = clamp({ col: Math.round(x / m.colStep) + 1, row: Math.round(y / m.rowStep) + 1, w: s.w, h: s.h });
    const homeLeft = m.left + (s.col - 1) * m.colStep;
    const homeTop = m.top + (s.row - 1) * m.rowStep;
    drag.article.style.transform = 'translate(' + (event.clientX - drag.grabX - homeLeft) + 'px,' + (event.clientY - drag.grabY - homeTop) + 'px)';
    return target;
  }

  function update(event) {
    drag.last = { clientX: event.clientX, clientY: event.clientY };
    const target = targetFor(event);
    drawCells(target.row + target.h - 1);
    if (!same(target, drag.target)) {
      drag.target = target;
      setVars(drag.placeholder, target);
      markCollisions(target, drag.article);
    }
  }

  function autoScroll() {
    if (!drag || !drag.active) return;
    const y = drag.last.clientY;
    const edge = 56;
    let dy = 0;
    if (y > window.innerHeight - edge) dy = 12;
    else if (y < edge) dy = -12;
    if (dy) {
      window.scrollBy(0, dy);
      update(drag.last);
    }
    drag.raf = requestAnimationFrame(autoScroll);
  }

  function endDrag() {
    const state = drag;
    drag = null;
    if (state.raf) cancelAnimationFrame(state.raf);
    try { state.handle.releasePointerCapture(state.pointerId); } catch (_) {}
    state.placeholder.remove();
    if (section) section.classList.remove('is-dragging');
    state.article.classList.remove('is-dragging');
    state.article.style.transform = '';
    widgets().forEach(function (el) { el.classList.remove('will-move'); });
    return state;
  }

  root.addEventListener('pointerdown', function (event) {
    const handle = event.target.closest ? event.target.closest('.dash-drag-grip, .dash-resize') : null;
    if (!handle || !root.contains(handle) || event.button > 0) return;
    const article = handle.closest('.dash-widget');
    if (!article) return;
    event.preventDefault();
    select(article);
    const rect = article.getBoundingClientRect();
    const start = cellsOf(article);
    const placeholder = document.createElement('div');
    placeholder.className = 'dash-drop-placeholder';
    setVars(placeholder, start);
    drag = {
      article: article,
      handle: handle,
      placeholder: placeholder,
      pointerId: event.pointerId,
      mode: handle.classList.contains('dash-resize') ? 'resize' : 'move',
      start: start,
      target: start,
      grabX: event.clientX - rect.left,
      grabY: event.clientY - rect.top,
      startX: event.clientX,
      startY: event.clientY,
      last: { clientX: event.clientX, clientY: event.clientY },
      active: false,
      raf: 0,
    };
    handle.setPointerCapture(event.pointerId);
  });

  root.addEventListener('pointermove', function (event) {
    if (!drag || event.pointerId !== drag.pointerId) return;
    if (!drag.active) {
      if (Math.hypot(event.clientX - drag.startX, event.clientY - drag.startY) < 4) return;
      drag.active = true;
      root.appendChild(drag.placeholder);
      if (section) section.classList.add('is-dragging');
      if (drag.mode === 'move') drag.article.classList.add('is-dragging');
      drag.raf = requestAnimationFrame(autoScroll);
    }
    update(event);
  });

  root.addEventListener('pointerup', function (event) {
    if (!drag || event.pointerId !== drag.pointerId) return;
    const state = endDrag();
    if (state.active && !same(state.start, state.target)) save(state.article, state.target);
  });

  root.addEventListener('pointercancel', function (event) {
    if (drag && event.pointerId === drag.pointerId) endDrag();
  });

  root.addEventListener('click', function (event) {
    const article = event.target.closest ? event.target.closest('.dash-widget') : null;
    if (article && root.contains(article)) select(article);
  });

  root.addEventListener('focusin', function (event) {
    const article = event.target.closest ? event.target.closest('.dash-widget') : null;
    if (article) select(article);
  });

  root.addEventListener('keydown', function (event) {
    const handle = event.target.closest ? event.target.closest('.dash-drag-grip, .dash-resize') : null;
    if (!handle) return;
    const dirs = { ArrowLeft: [-1, 0], ArrowRight: [1, 0], ArrowUp: [0, -1], ArrowDown: [0, 1] };
    const d = dirs[event.key];
    if (!d) return;
    event.preventDefault();
    const mode = handle.classList.contains('dash-resize') || event.shiftKey ? 'size' : 'move';
    nudge(handle.closest('.dash-widget'), mode, d[0], d[1]);
  });

  if (pad) {
    pad.addEventListener('click', function (event) {
      const btn = event.target.closest ? event.target.closest('[data-pad]') : null;
      if (!btn) return;
      if (btn.dataset.pad === 'close') {
        select(null);
        return;
      }
      if (selected) nudge(selected, btn.dataset.pad, +btn.dataset.dx, +btn.dataset.dy);
    });
  }

  document.addEventListener('keydown', function (event) {
    if (event.key !== 'Escape') return;
    if (drag) endDrag();
    else select(null);
  });

  drawCells();
})();
