/**
 * Tags. A row's "+ tag" (data-tag-open) opens a tag sheet for data-ids;
 * "Select to tag" (data-tag-select) tags many rows, or every transaction
 * matching the Activity filter, at once. Also drives the /tags manager.
 */
(function () {
  if (window.__tags) return;
  window.__tags = true;

  let sheet, backdrop, els;
  let ctx = null;
  let selecting = false;
  let bar = null;
  const picked = new Set();

  function csrf() {
    const el = document.querySelector('input[name="csrf"]');
    return el ? el.value : '';
  }

  function idsOf(el) {
    return (el.dataset.ids || '').split(/[\s,]+/).filter(Boolean);
  }

  function money(cents) {
    return (cents / 100).toLocaleString(undefined, { style: 'currency', currency: 'USD' });
  }

  function plural(n, word) {
    return n + ' ' + word + (n === 1 ? '' : 's');
  }

  function h(tag, cls, text) {
    const el = document.createElement(tag);
    if (cls) el.className = cls;
    if (text != null) el.textContent = text;
    return el;
  }

  async function getJSON(url) {
    const res = await fetch(url, { headers: { Accept: 'application/json' } });
    const data = await res.json().catch(function () { return {}; });
    if (!res.ok) throw new Error(data.error || 'Something went wrong');
    return data;
  }

  async function post(url, params) {
    const res = await fetch(url, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/x-www-form-urlencoded',
        'X-CSRF-Token': csrf(),
        Accept: 'application/json',
      },
      body: params instanceof URLSearchParams ? params : new URLSearchParams(params),
    });
    const data = await res.json().catch(function () { return {}; });
    if (!res.ok) throw new Error(data.error || 'Something went wrong');
    return data;
  }

  function scope() {
    return document.querySelector('[data-tag-scope]');
  }

  function build() {
    backdrop = h('div', 'sheet-backdrop');
    sheet = h('div', 'sheet cat-sheet tag-sheet');
    sheet.setAttribute('role', 'dialog');
    sheet.setAttribute('aria-label', 'Tags');
    sheet.innerHTML = `
      <div class="sheet-handle"></div>
      <div class="spread" style="align-items:flex-start">
        <div style="min-width:0">
          <div class="cat-title tag-title"></div>
          <div class="tiny muted tag-sub"></div>
        </div>
        <button type="button" class="btn btn-sm btn-ghost" data-close>Close</button>
      </div>
      <input class="cat-search tag-input" type="search" placeholder="Find or create a tag…" autocomplete="off" maxlength="40" aria-label="Find or create a tag">
      <div class="cat-opts tag-opts"></div>
      <p class="tiny muted tag-hint" style="margin:0"></p>
      <label class="check tag-similar-row" hidden><input type="checkbox" class="tag-similar">
        <span>Also apply to <b class="tag-similar-n">0</b> other <span class="tag-similar-key"></span> transaction<span class="tag-similar-s">s</span> in this list</span></label>
      <div class="alert alert-error tag-error" hidden></div>
      <div class="row tag-actions">
        <button type="button" class="btn btn-ghost btn-sm" data-tag-more hidden>Select more rows…</button>
        <span class="grow"></span>
        <button type="button" class="btn btn-primary" data-save disabled>Save</button>
      </div>`;
    document.body.append(backdrop, sheet);
    els = {
      title: sheet.querySelector('.tag-title'),
      sub: sheet.querySelector('.tag-sub'),
      input: sheet.querySelector('.tag-input'),
      opts: sheet.querySelector('.tag-opts'),
      hint: sheet.querySelector('.tag-hint'),
      similarRow: sheet.querySelector('.tag-similar-row'),
      similar: sheet.querySelector('.tag-similar'),
      similarN: sheet.querySelector('.tag-similar-n'),
      similarKey: sheet.querySelector('.tag-similar-key'),
      similarS: sheet.querySelector('.tag-similar-s'),
      error: sheet.querySelector('.tag-error'),
      more: sheet.querySelector('[data-tag-more]'),
      save: sheet.querySelector('[data-save]'),
    };
    backdrop.addEventListener('click', close);
    sheet.querySelector('[data-close]').addEventListener('click', close);
    els.save.addEventListener('click', save);
    els.input.addEventListener('input', render);
    els.input.addEventListener('keydown', function (e) {
      if (e.key !== 'Enter') return;
      e.preventDefault();
      enter();
    });
    els.more.addEventListener('click', function () {
      const ids = ctx ? ctx.ids.slice() : [];
      close();
      enterSelect(ids);
    });
    sheet.addEventListener('click', function (e) {
      const opt = e.target.closest('.tag-opt');
      if (opt) {
        toggle(opt.dataset.id);
        return;
      }
      const create = e.target.closest('.tag-create');
      if (create) {
        addCreate(create.dataset.name);
        return;
      }
      const pending = e.target.closest('.tag-new');
      if (pending) {
        ctx.creates = ctx.creates.filter(function (n) { return n !== pending.dataset.name; });
        render();
      }
    });
    document.addEventListener('keydown', function (e) {
      if (e.key === 'Escape' && sheet.classList.contains('open')) close();
    });
  }

  function initial(t) {
    if (!t.have) return 'off';
    return t.have >= ctx.total ? 'on' : 'mixed';
  }

  function stateOf(t) {
    return ctx.changes.get(String(t.id)) || initial(t);
  }

  function toggle(id) {
    const t = ctx.tags.find(function (x) { return String(x.id) === String(id); });
    if (!t) return;
    const next = stateOf(t) === 'on' ? 'off' : 'on';
    if (next === initial(t)) ctx.changes.delete(String(t.id));
    else ctx.changes.set(String(t.id), next);
    render();
  }

  function cleanName(s) {
    return s.replace(/^#+/, '').replace(/\s+/g, ' ').trim();
  }

  function findByName(name) {
    const low = name.toLowerCase();
    return ctx.tags.find(function (t) { return t.name.toLowerCase() === low; });
  }

  function addCreate(name) {
    name = cleanName(name || '');
    if (!name) return;
    const existing = findByName(name);
    if (existing) {
      if (stateOf(existing) !== 'on') toggle(existing.id);
    } else if (!ctx.creates.some(function (n) { return n.toLowerCase() === name.toLowerCase(); })) {
      ctx.creates.push(name);
    }
    els.input.value = '';
    render();
  }

  // Enter applies the first matching tag (or creates the typed one) and saves.
  function enter() {
    const q = cleanName(els.input.value);
    if (q) {
      const exact = findByName(q);
      const first = els.opts.querySelector('.tag-opt');
      if (exact) {
        if (stateOf(exact) !== 'on') toggle(exact.id);
      } else if (first) {
        const t = ctx.tags.find(function (x) { return String(x.id) === first.dataset.id; });
        if (t && stateOf(t) !== 'on') toggle(t.id);
      } else {
        addCreate(q);
      }
      els.input.value = '';
      render();
    }
    if (dirty()) save();
    else if (q) close();
  }

  function dirty() {
    return ctx && (ctx.changes.size > 0 || ctx.creates.length > 0);
  }

  function render() {
    if (!ctx) return;
    const q = cleanName(els.input.value).toLowerCase();
    els.opts.replaceChildren();
    ctx.creates.forEach(function (name) {
      const b = h('button', 'cat-opt tag-new is-on', '#' + name);
      b.type = 'button';
      b.dataset.name = name;
      b.title = 'New tag, tap to drop';
      els.opts.append(b);
    });
    let shown = 0;
    ctx.tags.forEach(function (t) {
      if (q && t.name.toLowerCase().indexOf(q) < 0) return;
      const st = stateOf(t);
      const b = h('button', 'cat-opt tag-opt is-' + st, '#' + t.name);
      b.type = 'button';
      b.dataset.id = t.id;
      b.setAttribute('aria-pressed', st === 'on' ? 'true' : st === 'mixed' ? 'mixed' : 'false');
      if (st === 'mixed') b.title = 'On ' + t.have + ' of ' + ctx.total;
      els.opts.append(b);
      shown++;
    });
    if (q && !findByName(q) && !ctx.creates.some(function (n) { return n.toLowerCase() === q; })) {
      const c = h('button', 'cat-opt tag-create', '+ Create “' + cleanName(els.input.value) + '”');
      c.type = 'button';
      c.dataset.name = cleanName(els.input.value);
      els.opts.append(c);
    }
    if (!ctx.tags.length && !ctx.creates.length && !q) {
      els.hint.textContent = 'No tags yet. Type a name like “Hawaii trip 2026” and press Enter.';
    } else if (q && !shown) {
      els.hint.textContent = 'Press Enter to create it and save.';
    } else {
      els.hint.textContent = ctx.total > 1 ? 'Tap to add to all, tap again to remove from all.' : '';
    }
    els.hint.hidden = !els.hint.textContent;
    const n = ctx.changes.size + ctx.creates.length;
    els.save.disabled = n === 0;
    els.save.textContent = ctx.total > 1 ? 'Apply to ' + ctx.total : 'Save';
  }

  async function open(opts) {
    if (!sheet) build();
    const sc = scope();
    ctx = {
      ids: opts.ids || [],
      all: !!opts.all,
      filter: sc ? sc.dataset.filter || '' : '',
      key: opts.key || '',
      title: opts.title || '',
      sub: opts.sub || '',
      tags: [],
      total: 0,
      changes: new Map(),
      creates: [],
    };
    const q = new URLSearchParams();
    if (ctx.all) q.set('all', '1');
    else q.set('ids', ctx.ids.join(' '));
    if (ctx.filter) q.set('filter', ctx.filter);
    if (ctx.key && !ctx.all) q.set('match', ctx.key);
    els.error.hidden = true;
    els.input.value = '';
    els.similar.checked = false;
    els.similarRow.hidden = true;
    let data;
    try {
      data = await getJSON('/tags/options?' + q);
    } catch (err) {
      alert(err.message);
      ctx = null;
      return;
    }
    ctx.tags = data.tags || [];
    ctx.total = data.total || 0;
    els.title.textContent = ctx.title;
    els.sub.textContent = ctx.sub;
    if (data.similar > 0) {
      els.similarN.textContent = data.similar;
      els.similarS.hidden = data.similar === 1;
      els.similarKey.textContent = ctx.key;
      els.similarRow.hidden = false;
    }
    els.more.hidden = !(ctx.ids.length === 1 && !ctx.all && document.querySelector('[data-tag-select]'));
    render();
    backdrop.classList.add('open');
    sheet.classList.add('open');
    if (window.matchMedia('(min-width: 800px)').matches || !ctx.tags.length) els.input.focus();
  }

  function close() {
    if (!sheet) return;
    sheet.classList.remove('open');
    backdrop.classList.remove('open');
    ctx = null;
  }

  async function save() {
    if (!dirty()) return;
    const p = new URLSearchParams();
    if (ctx.all) {
      p.set('all', '1');
    } else {
      p.set('ids', ctx.ids.join(' '));
    }
    if (ctx.filter) p.set('filter', ctx.filter);
    if (!ctx.all && els.similar.checked && !els.similarRow.hidden) {
      p.set('similar', '1');
      p.set('match', ctx.key);
    }
    const add = [], remove = [];
    ctx.changes.forEach(function (st, id) { (st === 'on' ? add : remove).push(id); });
    p.set('add', add.join(' '));
    p.set('remove', remove.join(' '));
    ctx.creates.forEach(function (n) { p.append('create', n); });
    els.save.disabled = true;
    try {
      const res = await post('/tags/apply', p);
      close();
      applyRows(res.rows || {});
      if (selecting) exitSelect();
      toast(res.message, res.undoId);
    } catch (err) {
      els.error.textContent = err.message;
      els.error.hidden = false;
      els.save.disabled = false;
    }
  }

  function chip(t) {
    const a = h('a', 'tag-chip', '#' + t.name);
    a.href = '/transactions?tag=' + t.id + '&preset=all';
    a.title = 'All #' + t.name + ' transactions';
    return a;
  }

  function applyRows(rows) {
    Object.keys(rows).forEach(function (id) {
      document.querySelectorAll('[data-tag-row="' + id + '"]').forEach(function (box) {
        box.querySelectorAll('.tag-chip').forEach(function (c) { c.remove(); });
        const add = box.querySelector('[data-tag-open]');
        rows[id].forEach(function (t) { box.insertBefore(chip(t), add); });
        if (add) add.textContent = rows[id].length ? '+' : '+ tag';
      });
    });
  }

  async function undo(undoId, btn) {
    if (btn) btn.disabled = true;
    try {
      await post('/undo', { undo_id: undoId });
    } catch (_) {}
    location.reload();
  }

  function toast(message, undoId) {
    let region = document.getElementById('cat-toasts');
    if (!region) {
      region = h('div', 'toast-region');
      region.id = 'cat-toasts';
      document.body.append(region);
    }
    const t = h('div', 'toast');
    t.append(h('span', '', message));
    if (undoId) {
      const b = h('button', '', 'Undo');
      b.type = 'button';
      b.addEventListener('click', function () { undo(undoId, b); });
      t.append(b);
    }
    region.replaceChildren(t);
    setTimeout(function () { t.remove(); }, 8000);
  }

  function reloadWithToast(message, undoId, hash) {
    sessionStorage.setItem('tag-toast', JSON.stringify({ message: message, undoId: undoId || '' }));
    if (hash && location.hash !== hash) location.hash = hash;
    location.reload();
  }

  // Select mode: checkboxes on every row plus a bar to tag the picked rows.
  function rows() {
    return Array.from(document.querySelectorAll('article[data-tx-row]'));
  }

  function buildBar() {
    bar = h('div', 'tag-bar');
    bar.setAttribute('role', 'toolbar');
    bar.setAttribute('aria-label', 'Tag selected transactions');
    bar.innerHTML = `
      <span class="tag-bar-count">0 selected</span>
      <button type="button" class="btn btn-sm btn-ghost" data-bar-all>Select all shown</button>
      <button type="button" class="btn btn-sm btn-ghost" data-bar-everything hidden></button>
      <button type="button" class="btn btn-sm btn-primary" data-bar-tag disabled>Tag…</button>
      <button type="button" class="btn btn-sm btn-ghost" data-bar-done>Done</button>`;
    document.body.append(bar);
    bar.querySelector('[data-bar-all]').addEventListener('click', function () {
      const all = rows().every(function (r) { return picked.has(r.dataset.txRow); });
      rows().forEach(function (r) { setPicked(r, !all); });
      updateBar();
    });
    bar.querySelector('[data-bar-everything]').addEventListener('click', function () {
      const n = parseInt(scope().dataset.filterCount || '0', 10);
      open({ all: true, title: 'All ' + plural(n, 'matching transaction'), sub: 'Everything that matches the current filter, not just the rows shown.' });
    });
    bar.querySelector('[data-bar-tag]').addEventListener('click', function () {
      const ids = Array.from(picked);
      open({ ids: ids, title: plural(ids.length, 'selected transaction'), sub: selectedTotal(ids) });
    });
    bar.querySelector('[data-bar-done]').addEventListener('click', exitSelect);
  }

  function selectedTotal(ids) {
    let sum = 0;
    ids.forEach(function (id) {
      const btn = document.querySelector('[data-tag-open][data-ids="' + id + '"]');
      if (btn) sum += parseInt(btn.dataset.amount || '0', 10);
    });
    return money(sum) + ' total';
  }

  function setPicked(row, on) {
    const id = row.dataset.txRow;
    const box = row.querySelector('.tag-pick');
    if (on) picked.add(id);
    else picked.delete(id);
    if (box) box.checked = on;
    row.classList.toggle('is-picked', on);
  }

  function updateBar() {
    if (!bar) return;
    const n = picked.size;
    bar.querySelector('.tag-bar-count').textContent = n + ' selected';
    const tagBtn = bar.querySelector('[data-bar-tag]');
    tagBtn.disabled = n === 0;
    tagBtn.textContent = n ? 'Tag ' + n + '…' : 'Tag…';
    const all = rows().length > 0 && rows().every(function (r) { return picked.has(r.dataset.txRow); });
    bar.querySelector('[data-bar-all]').textContent = all ? 'Clear' : 'Select all shown';
    const sc = scope();
    const total = sc ? parseInt(sc.dataset.filterCount || '0', 10) : 0;
    const every = bar.querySelector('[data-bar-everything]');
    every.hidden = !(total > rows().length);
    every.textContent = 'Tag all ' + total + ' matching…';
  }

  function enterSelect(initialIds) {
    if (!bar) buildBar();
    selecting = true;
    document.body.classList.add('tag-selecting');
    rows().forEach(function (row) {
      if (!row.querySelector('.tag-pick')) {
        const box = h('input', 'tag-pick');
        box.type = 'checkbox';
        box.setAttribute('aria-label', 'Select ' + (row.querySelector('.tx-payee') || {}).textContent);
        row.prepend(box);
      }
    });
    (initialIds || []).forEach(function (id) {
      const row = document.querySelector('article[data-tx-row="' + id + '"]');
      if (row) setPicked(row, true);
    });
    bar.hidden = false;
    document.querySelectorAll('[data-tag-select]').forEach(function (b) { b.textContent = 'Done selecting'; });
    updateBar();
  }

  function exitSelect() {
    selecting = false;
    document.body.classList.remove('tag-selecting');
    rows().forEach(function (row) {
      row.querySelector('.tag-pick')?.remove();
      row.classList.remove('is-picked');
    });
    picked.clear();
    if (bar) bar.hidden = true;
    document.querySelectorAll('[data-tag-select]').forEach(function (b) { b.textContent = 'Select to tag'; });
  }

  // Tag manager (/tags)
  function filterTags(input) {
    const q = input.value.trim().toLowerCase().replace(/^#/, '');
    let shown = 0;
    document.querySelectorAll('[data-tag-manage]').forEach(function (row) {
      const hit = !q || row.dataset.search.indexOf(q) >= 0;
      row.hidden = !hit;
      if (hit) shown++;
    });
    const none = document.querySelector('[data-tag-none]');
    if (none) none.hidden = shown > 0 || !document.querySelector('[data-tag-manage]');
  }

  function closeForms(row) {
    row.querySelector('[data-tag-rename-form]').hidden = true;
    row.querySelector('[data-tag-merge-form]')?.remove();
  }

  function toggleRename(id) {
    const row = document.querySelector('[data-tag-manage="' + id + '"]');
    const form = row && row.querySelector('[data-tag-rename-form]');
    if (!form) return;
    const show = form.hidden;
    closeForms(row);
    form.hidden = !show;
    if (show) {
      form.elements.name.focus();
      form.elements.name.select();
    }
  }

  function toggleMerge(btn) {
    const id = btn.dataset.tagMerge;
    const row = document.querySelector('[data-tag-manage="' + id + '"]');
    const tpl = document.getElementById('tag-merge-tpl');
    if (!row || !tpl) return;
    const open = row.querySelector('[data-tag-merge-form]');
    closeForms(row);
    if (open) return;
    const form = tpl.content.firstElementChild.cloneNode(true);
    const sel = form.elements.tag_id;
    Array.from(sel.options).forEach(function (o) { if (o.value === id) o.remove(); });
    if (!sel.options.length) {
      alert('Add another tag first, then merge into it.');
      return;
    }
    const n = parseInt(btn.dataset.count || '0', 10);
    form.querySelector('[data-merge-note]').textContent = (n ? 'Moves its ' + plural(n, 'transaction') : 'Moves it') +
      ' (and payee rules that add it) onto the tag you pick, then archives #' + btn.dataset.name + '. You can undo.';
    form.addEventListener('submit', async function (e) {
      e.preventDefault();
      const submit = form.querySelector('[type="submit"]');
      submit.disabled = true;
      try {
        const res = await post('/tags/' + id + '/merge', { tag_id: sel.value });
        reloadWithToast(res.message, res.undoId, '#tag-' + res.id);
      } catch (err) {
        submit.disabled = false;
        alert(err.message);
      }
    });
    row.append(form);
    sel.focus();
  }

  async function manageAction(btn, action) {
    btn.disabled = true;
    try {
      const res = await post('/tags/' + btn.dataset['tag' + action[0].toUpperCase() + action.slice(1)] + '/' + action, {});
      reloadWithToast(res.message, res.undoId);
    } catch (err) {
      btn.disabled = false;
      alert(err.message);
    }
  }

  document.addEventListener('input', function (e) {
    if (e.target.matches('[data-tag-search]')) filterTags(e.target);
  });

  document.addEventListener('DOMContentLoaded', function () {
    const search = document.querySelector('[data-tag-search]');
    if (search && search.value) filterTags(search);
    const pending = sessionStorage.getItem('tag-toast');
    if (pending) {
      sessionStorage.removeItem('tag-toast');
      try {
        const t = JSON.parse(pending);
        toast(t.message, t.undoId);
      } catch (_) {}
    }
  });

  document.addEventListener('click', function (e) {
    if (sheet && sheet.contains(e.target)) return;
    if (bar && bar.contains(e.target)) return;
    const openBtn = e.target.closest('[data-tag-open]');
    if (openBtn) {
      e.preventDefault();
      if (selecting) {
        const row = openBtn.closest('article[data-tx-row]');
        if (row) {
          setPicked(row, !picked.has(row.dataset.txRow));
          updateBar();
        }
        return;
      }
      const amount = parseInt(openBtn.dataset.amount || '0', 10);
      open({
        ids: idsOf(openBtn),
        key: openBtn.dataset.key || '',
        title: openBtn.dataset.payee || '(no payee)',
        sub: amount ? money(amount) : '',
      });
      return;
    }
    const sel = e.target.closest('[data-tag-select]');
    if (sel) {
      e.preventDefault();
      if (selecting) exitSelect();
      else enterSelect([]);
      return;
    }
    if (selecting) {
      const row = e.target.closest('article[data-tx-row]');
      if (row && !e.target.closest('a, button, form, select, textarea')) {
        if (!e.target.matches('.tag-pick')) e.preventDefault();
        setPicked(row, !picked.has(row.dataset.txRow));
        updateBar();
        return;
      }
    }
    const rename = e.target.closest('[data-tag-rename]');
    if (rename) {
      e.preventDefault();
      toggleRename(rename.dataset.tagRename);
      return;
    }
    const merge = e.target.closest('[data-tag-merge]');
    if (merge) {
      e.preventDefault();
      toggleMerge(merge);
      return;
    }
    const archive = e.target.closest('[data-tag-archive]');
    if (archive) {
      e.preventDefault();
      manageAction(archive, 'archive');
      return;
    }
    const del = e.target.closest('[data-tag-delete]');
    if (del) {
      e.preventDefault();
      manageAction(del, 'delete');
      return;
    }
    const cancel = e.target.closest('[data-tag-manage] [data-form-cancel]');
    if (cancel) {
      e.preventDefault();
      closeForms(cancel.closest('[data-tag-manage]'));
    }
  });
})();
