/**
 * Tap-to-categorize. Any element with data-cat="tx|staging" opens a category
 * picker for the ids in data-ids; data-cat-quick applies a category directly.
 */
(function () {
  if (window.__categorize) return;
  window.__categorize = true;

  let opts = null;
  let ctx = null;
  let sheet, backdrop, els;
  let personTouched = false;
  let similarCount = 0;
  let filedCount = 0;
  let similarTimer = 0;
  const settled = new Set(); // review transactions handled on this page

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

  function h(tag, cls, text) {
    const el = document.createElement(tag);
    if (cls) el.className = cls;
    if (text != null) el.textContent = text;
    return el;
  }

  async function loadOptions() {
    if (!opts) {
      const res = await fetch('/categorize/options', { headers: { Accept: 'application/json' } });
      if (!res.ok) throw new Error('Could not load categories');
      opts = await res.json();
    }
    return opts;
  }

  function build() {
    backdrop = h('div', 'sheet-backdrop');
    sheet = h('div', 'sheet cat-sheet');
    sheet.setAttribute('role', 'dialog');
    sheet.setAttribute('aria-label', 'Choose category');
    sheet.innerHTML = `
      <div class="sheet-handle"></div>
      <div class="spread" style="align-items:flex-start">
        <div style="min-width:0">
          <div class="cat-title"></div>
          <div class="tiny muted cat-sub"></div>
        </div>
        <button type="button" class="btn btn-sm btn-ghost" data-close>Close</button>
      </div>
      <div class="cat-suggest-wrap">
        <div class="cat-section-label">Suggested</div>
        <div class="cat-opts cat-suggest"></div>
      </div>
      <input class="cat-search" type="search" placeholder="Search categories…" autocomplete="off" aria-label="Search categories">
      <div class="cat-grid"></div>
      <div class="cat-options">
        <div class="tiny cat-rule-note" hidden></div>
        <div class="cat-section-label">Payee contains</div>
        <input class="cat-match" aria-label="Payee contains" spellcheck="false">
        <label class="check cat-similar-row"><input type="checkbox" class="cat-similar">
          <span>Also apply to <b class="cat-similar-n">0</b> other uncategorized transaction<span class="cat-similar-s">s</span> that match</span></label>
        <label class="check cat-filed-row"><input type="checkbox" class="cat-filed">
          <span>Also re-file <b class="cat-filed-n">0</b> already-categorized transaction<span class="cat-filed-s">s</span> that match</span></label>
        <label class="check"><input type="checkbox" class="cat-remember">
          <span class="cat-remember-label">Remember for future imports</span></label>
      </div>
      <details class="cat-more">
        <summary class="tiny muted">Who it's for · rename payee</summary>
        <div class="stack" style="margin-top:.6rem">
          <div class="seg cat-person"></div>
          <div class="field cat-payee-field"><label>Payee</label><input class="cat-payee"></div>
          <p class="tiny muted" style="margin:0">Changes here are saved when you pick a category.</p>
        </div>
      </details>
      <div class="alert alert-error cat-error" hidden></div>`;
    document.body.append(backdrop, sheet);
    els = {
      title: sheet.querySelector('.cat-title'),
      sub: sheet.querySelector('.cat-sub'),
      suggestWrap: sheet.querySelector('.cat-suggest-wrap'),
      suggest: sheet.querySelector('.cat-suggest'),
      search: sheet.querySelector('.cat-search'),
      grid: sheet.querySelector('.cat-grid'),
      similarRow: sheet.querySelector('.cat-similar-row'),
      similar: sheet.querySelector('.cat-similar'),
      similarN: sheet.querySelector('.cat-similar-n'),
      similarS: sheet.querySelector('.cat-similar-s'),
      filedRow: sheet.querySelector('.cat-filed-row'),
      filed: sheet.querySelector('.cat-filed'),
      filedN: sheet.querySelector('.cat-filed-n'),
      filedS: sheet.querySelector('.cat-filed-s'),
      ruleNote: sheet.querySelector('.cat-rule-note'),
      options: sheet.querySelector('.cat-options'),
      match: sheet.querySelector('.cat-match'),
      remember: sheet.querySelector('.cat-remember'),
      rememberLabel: sheet.querySelector('.cat-remember-label'),
      more: sheet.querySelector('.cat-more'),
      person: sheet.querySelector('.cat-person'),
      payeeField: sheet.querySelector('.cat-payee-field'),
      payee: sheet.querySelector('.cat-payee'),
      error: sheet.querySelector('.cat-error'),
    };
    backdrop.addEventListener('click', close);
    sheet.querySelector('[data-close]').addEventListener('click', close);
    els.search.addEventListener('input', filterGrid);
    els.search.addEventListener('keydown', function (e) {
      if (e.key !== 'Enter') return;
      e.preventDefault();
      const first = els.grid.querySelector('.cat-opt:not([hidden])');
      if (first) pick(first.dataset.id);
    });
    els.match.addEventListener('input', function () {
      renderRememberLabel();
      clearTimeout(similarTimer);
      similarTimer = setTimeout(function () { refreshSimilar(false); }, 250);
    });
    sheet.addEventListener('click', function (e) {
      const opt = e.target.closest('.cat-opt');
      if (opt) pick(opt.dataset.id);
    });
    document.addEventListener('keydown', function (e) {
      if (e.key === 'Escape' && sheet.classList.contains('open')) close();
    });
  }

  function catButton(c, current) {
    const b = h('button', 'cat-opt', c.name);
    b.type = 'button';
    if (c.color) b.style.setProperty('--cat-color', c.color);
    b.dataset.id = c.id;
    b.dataset.search = ((c.parent || '') + ' ' + c.name).toLowerCase();
    if (String(c.id) === String(current)) b.classList.add('is-current');
    return b;
  }

  function renderGrid() {
    const cats = opts.categories;
    const parents = cats.filter(function (c) { return !c.parent; });
    const kids = {};
    cats.forEach(function (c) {
      if (c.parent) (kids[c.parent] = kids[c.parent] || []).push(c);
    });
    const order = ctx.amount > 0 ? ['income', 'expense', 'transfer'] : ['expense', 'income', 'transfer'];
    parents.sort(function (a, b) { return order.indexOf(a.kind) - order.indexOf(b.kind); });
    els.grid.replaceChildren();
    parents.forEach(function (p) {
      const sec = h('div', 'cat-group');
      const label = p.kind === 'transfer' ? 'Not spending or income' : p.name;
      sec.append(h('div', 'cat-section-label', label));
      const row = h('div', 'cat-opts');
      const top = catButton(p, ctx.current);
      top.classList.add('is-parent');
      if (p.kind === 'transfer') top.textContent = p.name + ' (between accounts, card payments)';
      row.append(top);
      (kids[p.name] || []).forEach(function (c) { row.append(catButton(c, ctx.current)); });
      sec.append(row);
      els.grid.append(sec);
    });
  }

  function filterGrid() {
    const q = els.search.value.trim().toLowerCase();
    els.grid.querySelectorAll('.cat-group').forEach(function (sec) {
      let any = false;
      sec.querySelectorAll('.cat-opt').forEach(function (b) {
        const hit = !q || b.dataset.search.indexOf(q) >= 0;
        b.hidden = !hit;
        any = any || hit;
      });
      sec.hidden = !any;
    });
  }

  function renderPeople() {
    els.person.replaceChildren();
    opts.people.forEach(function (p) {
      const b = h('button', p.slot === ctx.person ? 'active' : '', p.name || p.slot);
      b.type = 'button';
      b.addEventListener('click', function () {
        personTouched = true;
        ctx.person = p.slot;
        els.person.querySelectorAll('button').forEach(function (x) { x.classList.toggle('active', x === b); });
      });
      els.person.append(b);
    });
  }

  function renderSuggest(list) {
    els.suggest.replaceChildren();
    (list || []).forEach(function (c) {
      const b = catButton(c, ctx.current);
      b.classList.add('is-suggest');
      els.suggest.append(b);
    });
    els.suggestWrap.hidden = !els.suggest.children.length;
  }

  async function refreshSimilar(withSuggest) {
    const q = new URLSearchParams({ target: ctx.target, match: els.match.value, exclude: ctx.ids.join(' ') });
    if (ctx.batch) q.set('batch', ctx.batch);
    if (withSuggest && ctx.payee) {
      q.set('payee', ctx.payee);
      q.set('amount', ctx.amount);
      q.set('account', ctx.account || '');
    }
    try {
      const res = await fetch('/categorize/similar?' + q, { headers: { Accept: 'application/json' } });
      const data = await res.json();
      if (!ctx) return;
      similarCount = data.count || 0;
      filedCount = data.filed || 0;
      els.similarN.textContent = similarCount;
      els.similarS.hidden = similarCount === 1;
      els.similarRow.hidden = similarCount === 0;
      els.filedN.textContent = filedCount;
      els.filedS.hidden = filedCount === 1;
      els.filedRow.hidden = filedCount === 0;
      if (withSuggest) {
        els.similar.checked = similarCount > 0 && ctx.eager;
        renderSuggest(data.suggest);
        ctx.rule = ctx.target === 'tx' ? data.rule || null : null;
        renderRuleNote();
      }
      renderRememberLabel();
    } catch (_) {}
  }

  function normalize(s) {
    return (s || '').toUpperCase().replace(/\s+/g, ' ').trim();
  }

  function renderRuleNote() {
    const r = ctx.rule;
    els.ruleNote.replaceChildren();
    els.ruleNote.hidden = !r;
    if (!r) return;
    els.ruleNote.append('Rule “' + r.value + '” files this payee as ' + (r.category || 'no category') + ' · ');
    const a = h('a', '', 'Edit rule');
    a.href = '/rules?q=' + encodeURIComponent(r.value) + '#rule-' + r.id;
    els.ruleNote.append(a);
  }

  function renderRememberLabel() {
    const r = ctx && ctx.rule;
    const same = r && r.match === 'contains' && r.value === normalize(els.match.value);
    els.rememberLabel.textContent = same ? 'Update the “' + r.value + '” rule for future imports' : 'Remember for future imports';
  }

  async function open(source) {
    if (!sheet) build();
    ctx = {
      target: source.dataset.cat || 'tx',
      ids: source._ids || idsOf(source),
      batch: source.dataset.batch || '',
      payee: source.dataset.payee || '',
      amount: parseInt(source.dataset.amount || '0', 10),
      account: source.dataset.account || '',
      current: source.dataset.current && source.dataset.current !== '0' ? source.dataset.current : '',
      person: source.dataset.person || '',
      key: source.dataset.key || '',
      title: source.dataset.title || '',
    };
    // Default to "apply widely" for uncategorized items and review/import groups,
    // but not when re-filing one already-categorized transaction.
    ctx.eager = source.dataset.remember === '1' || (!ctx.current && source.dataset.remember !== 'false');
    personTouched = false;
    els.error.hidden = true;
    try {
      await loadOptions();
    } catch (err) {
      showError(err.message);
      return;
    }
    const n = ctx.ids.length;
    const isRule = ctx.target === 'rule' || ctx.target === 'merge';
    els.title.textContent = ctx.title || ctx.payee || '(no payee)';
    if (ctx.target === 'merge') {
      const count = parseInt(source.dataset.count || '0', 10);
      els.sub.textContent = (count ? 'Moves its ' + count + ' transaction' + (count === 1 ? '' : 's') + ', ' : 'Moves its ') +
        'payee rules and budgets into the category you pick, then archives it. You can undo.';
    } else {
      els.sub.textContent = isRule ? 'Future imports matching this rule are filed in the category you pick.'
        : (ctx.amount ? money(ctx.amount) : '') + (n > 1 ? ' · ' + n + ' transactions' : '');
    }
    els.match.value = isRule ? '' : ctx.key;
    els.remember.checked = ctx.eager && !!ctx.key;
    els.similar.checked = false;
    els.similarRow.hidden = true;
    els.filed.checked = false;
    els.filedRow.hidden = true;
    ctx.rule = null;
    els.ruleNote.hidden = true;
    els.options.hidden = isRule;
    els.more.hidden = isRule;
    els.search.value = '';
    els.payee.value = ctx.payee;
    els.payeeField.hidden = !(ctx.target === 'tx' && n === 1);
    els.more.open = false;
    els.suggestWrap.hidden = true;
    renderRememberLabel();
    renderGrid();
    if (ctx.target === 'merge') {
      els.grid.querySelectorAll('.cat-opt[data-id="' + ctx.current + '"]').forEach(function (b) { b.remove(); });
    }
    renderPeople();
    backdrop.classList.add('open');
    sheet.classList.add('open');
    if (window.matchMedia('(min-width: 800px)').matches) els.search.focus();
    refreshSimilar(true);
  }

  function close() {
    if (!sheet) return;
    sheet.classList.remove('open');
    backdrop.classList.remove('open');
    ctx = null;
  }

  function showError(msg) {
    if (!els) return alert(msg);
    els.error.textContent = msg;
    els.error.hidden = false;
  }

  async function post(url, params) {
    const res = await fetch(url, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/x-www-form-urlencoded',
        'X-CSRF-Token': csrf(),
        Accept: 'application/json',
      },
      body: new URLSearchParams(params),
    });
    const data = await res.json().catch(function () { return {}; });
    if (!res.ok) throw new Error(data.error || 'Something went wrong');
    return data;
  }

  async function pick(categoryId) {
    if (!ctx) return;
    if (ctx.target === 'rule') return pickForRule(ctx.ids[0], categoryId);
    if (ctx.target === 'merge') return mergeInto(ctx.ids[0], categoryId);
    const params = {
      target: ctx.target,
      batch: ctx.batch,
      ids: ctx.ids.join(' '),
      category_id: categoryId,
      match: els.match.value.trim(),
      similar: els.similar.checked && similarCount > 0 ? '1' : '',
      similar_filed: els.filed.checked && filedCount > 0 ? '1' : '',
      remember: els.remember.checked ? '1' : '',
    };
    if (personTouched) params.person = ctx.person;
    if (!els.payeeField.hidden && els.payee.value.trim() && els.payee.value.trim() !== ctx.payee) {
      params.payee = els.payee.value.trim();
    }
    const target = ctx.target;
    try {
      const res = await post('/categorize', params);
      close();
      res.remember = params.remember === '1';
      res.match = params.match;
      applyResult(target, res, res.category.name);
      toast(res.message, res.undoId);
    } catch (err) {
      showError(err.message);
    }
  }

  async function pickForRule(ruleId, categoryId) {
    try {
      const res = await post('/rules/' + ruleId + '/category', { category_id: categoryId });
      close();
      const row = document.querySelector('[data-rule-row="' + ruleId + '"]');
      if (row) {
        const chip = row.querySelector('[data-cat="rule"]');
        chip.textContent = res.category.name;
        chip.dataset.current = res.category.id;
        chip.classList.remove('cat-none');
        setMisfiled(row, res.misfiled, res.category.name);
        const values = JSON.parse(row.dataset.rule || '{}');
        values.set_category_id = String(res.category.id);
        values.name = res.name;
        row.dataset.rule = JSON.stringify(values);
        row.querySelector('.rule-edit')?.remove();
      }
      const actions = [];
      if (res.misfiled > 0) {
        actions.push({
          label: 'Re-file ' + res.misfiled + ' saved',
          run: function () { return applyRule(ruleId); },
        });
      }
      toast(res.message, null, actions);
    } catch (err) {
      showError(err.message);
    }
  }

  async function mergeInto(categoryId, targetId) {
    try {
      const res = await post('/categories/' + categoryId + '/merge', { category_id: targetId });
      close();
      reloadWithToast(res.message, res.undoId, '#cat-' + res.id);
    } catch (err) {
      showError(err.message);
    }
  }

  // Shows a toast after the page reloads, for changes that reshape the page.
  function reloadWithToast(message, undoId, hash) {
    sessionStorage.setItem('cat-toast', JSON.stringify({ message: message, undoId: undoId || '' }));
    if (hash && location.hash !== hash) {
      location.hash = hash;
    }
    location.reload();
  }

  function setMisfiled(row, n, categoryName) {
    const btn = row.querySelector('[data-rule-apply]');
    if (!btn) return;
    if (categoryName != null) btn.querySelector('[data-rule-cat]').textContent = categoryName;
    btn.querySelector('[data-rule-misfiled]').textContent = n;
    btn.querySelector('[data-rule-s]').hidden = n === 1;
    btn.hidden = !n || row.classList.contains('is-off');
    btn.disabled = false;
  }

  async function applyRule(ruleId) {
    const row = document.querySelector('[data-rule-row="' + ruleId + '"]');
    const btn = row && row.querySelector('[data-rule-apply]');
    if (btn) btn.disabled = true;
    try {
      const res = await post('/rules/' + ruleId + '/apply', {});
      if (row) setMisfiled(row, 0);
      applyResult('tx', res, res.category.name);
      toast(res.message, res.undoId);
    } catch (err) {
      if (btn) btn.disabled = false;
      alert(err.message);
    }
  }

  async function quick(btn) {
    btn.disabled = true;
    try {
      const res = await post('/categorize', {
        target: btn.dataset.target || 'tx',
        batch: btn.dataset.batch || '',
        ids: idsOf(btn).join(' '),
        category_id: btn.dataset.category,
        match: btn.dataset.key || '',
        remember: btn.dataset.remember === '1' ? '1' : '',
      });
      res.remember = btn.dataset.remember === '1';
      res.match = btn.dataset.key || '';
      applyResult(btn.dataset.target || 'tx', res, res.category.name);
      toast(res.message, res.undoId);
    } catch (err) {
      btn.disabled = false;
      alert(err.message);
    }
  }

  async function markReviewed(btn) {
    btn.disabled = true;
    try {
      const res = await post('/review/confirm-bulk', { ids: idsOf(btn).join(' ') });
      applyResult('tx', res, null);
    } catch (err) {
      btn.disabled = false;
      alert(err.message);
    }
  }

  function coveredBy(el, done) {
    const ids = idsOf(el);
    return ids.length > 0 && ids.every(function (id) { return done.has(id); });
  }

  // Reflect a save everywhere on the page: chips, review groups, payee text, badges.
  function applyResult(target, res, categoryName) {
    const done = new Set((res.ids || []).map(String));
    if (categoryName) {
      document.querySelectorAll('[data-cat="' + target + '"]').forEach(function (chip) {
        if (!coveredBy(chip, done)) return;
        if (!chip.dataset.label) chip.textContent = categoryName;
        chip.classList.remove('cat-none');
        chip.dataset.current = res.category.id;
      });
    }
    if (target === 'tx') {
      done.forEach(function (id) {
        document.querySelectorAll('[data-review-flag="' + id + '"]').forEach(function (el) { el.remove(); });
        if (res.payee) {
          document.querySelectorAll('[data-tx-row="' + id + '"] .tx-payee').forEach(function (el) { el.textContent = res.payee; });
          document.querySelectorAll('[data-cat="tx"][data-ids="' + id + '"]').forEach(function (el) { el.dataset.payee = res.payee; });
        }
      });
    } else {
      done.forEach(function (id) {
        document.querySelectorAll('[data-auto-note="' + id + '"]').forEach(function (el) { el.remove(); });
      });
    }
    if (target === 'tx') done.forEach(function (id) { settled.add(id); });
    document.querySelectorAll('[data-done-ids]').forEach(function (box) {
      const ids = (box.dataset.doneIds || '').split(/\s+/).filter(Boolean);
      if (!ids.length || !ids.every(function (id) { return settled.has(id); })) return;
      if (box.classList.contains('is-done') && !ids.some(function (id) { return done.has(id); })) return;
      box.classList.add('is-done');
      renderDone(box, res, categoryName);
    });
    if (typeof res.reviewCount === 'number') {
      document.querySelectorAll('[data-review-count]').forEach(function (el) { el.textContent = res.reviewCount; });
      document.querySelectorAll('.nav-badge').forEach(function (el) {
        if (res.reviewCount > 0) el.textContent = res.reviewCount;
        else el.remove();
      });
    }
  }

  // A finished review group keeps its result visible with ways to fix it.
  function renderDone(box, res, categoryName) {
    const note = box.querySelector('[data-done-note]');
    if (!note) return;
    note.replaceChildren();
    let text = categoryName ? '✓ ' + categoryName : '✓ Reviewed';
    if (categoryName && res.remembered) text += ' · future imports too';
    note.append(h('span', '', text));
    const change = h('button', 'btn btn-sm btn-ghost', categoryName ? 'Change' : 'Choose category');
    change.type = 'button';
    change.dataset.cat = 'tx';
    change.dataset.label = '1';
    change.dataset.ids = ((res.ids && res.ids.length ? res.ids : null) || (box.dataset.doneIds || '').split(/\s+/)).join(' ');
    change.dataset.key = res.match || box.dataset.key || '';
    change.dataset.title = box.dataset.title || '';
    change.dataset.payee = box.dataset.payee || '';
    change.dataset.amount = box.dataset.amount || '0';
    change.dataset.account = box.dataset.account || '';
    change.dataset.current = res.category ? res.category.id : '';
    change.dataset.remember = res.remember || !categoryName ? '1' : 'false';
    note.append(change);
    if (res.undoId) {
      const undo = h('button', 'btn btn-sm btn-ghost', 'Undo');
      undo.type = 'button';
      undo.dataset.undo = res.undoId;
      note.append(undo);
    }
    if (res.ruleId) {
      const a = h('a', 'tiny', 'Rule');
      a.href = '/rules#rule-' + res.ruleId;
      note.append(a);
    }
  }

  async function undo(undoId, btn) {
    if (btn) btn.disabled = true;
    try {
      await post('/undo', { undo_id: undoId });
    } catch (_) {}
    location.reload();
  }

  function toast(message, undoId, actions) {
    let region = document.getElementById('cat-toasts');
    if (!region) {
      region = h('div', 'toast-region');
      region.id = 'cat-toasts';
      document.body.append(region);
    }
    const t = h('div', 'toast');
    t.append(h('span', '', message));
    (actions || []).forEach(function (act) {
      const b = h('button', '', act.label);
      b.type = 'button';
      b.addEventListener('click', function () {
        b.disabled = true;
        t.remove();
        act.run();
      });
      t.append(b);
    });
    if (undoId) {
      const b = h('button', '', 'Undo');
      b.type = 'button';
      b.addEventListener('click', function () { undo(undoId, b); });
      t.append(b);
    }
    region.replaceChildren(t);
    setTimeout(function () { t.remove(); }, actions && actions.length ? 15000 : 8000);
  }

  function filterRules(input) {
    const q = input.value.trim().toLowerCase();
    let shown = 0;
    document.querySelectorAll('[data-rule-row]').forEach(function (row) {
      const hit = !q || row.dataset.search.indexOf(q) >= 0;
      row.hidden = !hit;
      if (hit) shown++;
    });
    const none = document.querySelector('[data-rule-none]');
    if (none) none.hidden = shown > 0 || !document.querySelector('[data-rule-row]');
  }

  async function toggleRule(input) {
    input.disabled = true;
    try {
      await post('/rules/' + input.dataset.ruleToggle + '/enabled', { enabled: input.checked ? '1' : '' });
      location.reload();
    } catch (err) {
      input.checked = !input.checked;
      input.disabled = false;
      alert(err.message);
    }
  }

  async function deleteRule(btn) {
    const id = btn.dataset.ruleDelete;
    btn.disabled = true;
    try {
      const res = await post('/rules/' + id + '/delete', {});
      const row = document.querySelector('[data-rule-row="' + id + '"]');
      if (row) row.remove();
      toast(res.message, res.undoId);
    } catch (err) {
      btn.disabled = false;
      alert(err.message);
    }
  }

  // Opens (or closes) an edit form cloned from a <template>, filled from JSON on the row.
  function toggleEditForm(row, tplId, action, values) {
    const tpl = document.getElementById(tplId);
    if (!row || !tpl) return null;
    const open = row.querySelector('.rule-edit');
    if (open) {
      open.remove();
      return null;
    }
    const form = tpl.content.firstElementChild.cloneNode(true);
    form.action = action;
    Object.keys(values).forEach(function (name) {
      const field = form.elements[name];
      if (!field || field instanceof RadioNodeList) return;
      if (field.type === 'checkbox') field.checked = !!values[name];
      else field.value = values[name];
    });
    const cancel = form.querySelector('[data-rule-cancel], [data-form-cancel]');
    if (cancel) cancel.addEventListener('click', function () { form.remove(); });
    row.append(form);
    return form;
  }

  function toggleRuleForm(id) {
    const row = document.querySelector('[data-rule-row="' + id + '"]');
    const values = row ? JSON.parse(row.dataset.rule || '{}') : {};
    const form = toggleEditForm(row, 'rule-form-tpl', '/rules/' + id, values);
    if (!form) return;
    const advanced = form.querySelector('.rule-advanced');
    if (advanced) {
      advanced.open = ['account_id', 'notes_contains', 'amount_min', 'amount_max', 'set_tags']
        .some(function (k) { return values[k]; });
    }
    form.elements.payee_value.focus();
  }

  function toggleCategoryForm(id) {
    const row = document.querySelector('[data-cat-row="' + id + '"]');
    const values = row ? JSON.parse(row.dataset.category || '{}') : {};
    const form = toggleEditForm(row, 'cat-form-tpl', '/categories/' + id, values);
    if (!form) return;
    const parent = form.elements.parent_id;
    Array.from(parent.options).forEach(function (o) {
      if (o.value && (o.dataset.kind !== values.kind || o.value === String(id))) o.remove();
    });
    parent.value = values.parent_id || '';
    if (values.has_children) {
      parent.disabled = true;
      form.querySelector('[data-parent-hint]').hidden = false;
      const kids = form.querySelector('[data-color-children]');
      if (kids) kids.hidden = false;
    }
    setColorChoice(form, values.color || '');
    form.elements.name.focus();
    form.elements.name.select();
  }

  function setColorChoice(form, color) {
    const radios = Array.from(form.querySelectorAll('input[type="radio"][name="color"]'));
    const preset = radios.find(function (r) { return r.value.toUpperCase() === color.toUpperCase(); });
    if (preset) {
      preset.checked = true;
      return;
    }
    const custom = radios.find(function (r) { return r.value === 'custom'; });
    if (custom) custom.checked = true;
    if (form.elements.color_custom) form.elements.color_custom.value = color.toLowerCase();
  }

  function filterCategories(input) {
    const q = input.value.trim().toLowerCase();
    let shown = 0;
    document.querySelectorAll('[data-cat-block]').forEach(function (block) {
      const rows = Array.from(block.querySelectorAll('[data-cat-row]'));
      const parentHit = !q || rows[0].dataset.search.indexOf(q) >= 0;
      let any = parentHit;
      rows.slice(1).forEach(function (row) {
        const hit = parentHit || row.dataset.search.indexOf(q) >= 0;
        row.hidden = !hit;
        any = any || hit;
      });
      const add = block.querySelector('[data-cat-add]');
      if (add) add.hidden = !!q;
      block.hidden = !any;
      if (any) shown++;
    });
    document.querySelectorAll('[data-cat-kind]').forEach(function (sec) {
      sec.hidden = !sec.querySelector('[data-cat-block]:not([hidden])');
    });
    const none = document.querySelector('[data-cat-none]');
    if (none) none.hidden = shown > 0;
  }

  async function archiveCategory(btn) {
    btn.disabled = true;
    try {
      const res = await post('/categories/' + btn.dataset.catArchive + '/archive', {});
      reloadWithToast(res.message, res.undoId);
    } catch (err) {
      btn.disabled = false;
      alert(err.message);
    }
  }

  document.addEventListener('input', function (e) {
    if (e.target.matches('[data-rule-search]')) filterRules(e.target);
    if (e.target.matches('[data-cat-search]')) filterCategories(e.target);
    if (e.target.matches('[data-color-custom]')) {
      const radio = e.target.closest('.swatch').querySelector('input[type="radio"]');
      if (radio) radio.checked = true;
    }
  });

  document.addEventListener('change', function (e) {
    if (e.target.matches('[data-rule-toggle]')) toggleRule(e.target);
  });

  document.addEventListener('DOMContentLoaded', function () {
    const search = document.querySelector('[data-rule-search]');
    if (search && search.value) filterRules(search);
    const catSearch = document.querySelector('[data-cat-search]');
    if (catSearch && catSearch.value) filterCategories(catSearch);
    const pending = sessionStorage.getItem('cat-toast');
    if (pending) {
      sessionStorage.removeItem('cat-toast');
      try {
        const t = JSON.parse(pending);
        toast(t.message, t.undoId);
      } catch (_) {}
    }
  });

  document.addEventListener('click', function (e) {
    if (sheet && sheet.contains(e.target)) return;
    const undoBtn = e.target.closest('[data-undo]');
    if (undoBtn) {
      e.preventDefault();
      undo(undoBtn.dataset.undo, undoBtn);
      return;
    }
    const applyBtn = e.target.closest('[data-rule-apply]');
    if (applyBtn) {
      e.preventDefault();
      applyRule(applyBtn.dataset.ruleApply);
      return;
    }
    const editBtn = e.target.closest('[data-rule-edit]');
    if (editBtn) {
      e.preventDefault();
      toggleRuleForm(editBtn.dataset.ruleEdit);
      return;
    }
    const catEdit = e.target.closest('[data-cat-edit]');
    if (catEdit) {
      e.preventDefault();
      toggleCategoryForm(catEdit.dataset.catEdit);
      return;
    }
    const catArchive = e.target.closest('[data-cat-archive]');
    if (catArchive) {
      e.preventDefault();
      archiveCategory(catArchive);
      return;
    }
    const delBtn = e.target.closest('[data-rule-delete]');
    if (delBtn) {
      e.preventDefault();
      deleteRule(delBtn);
      return;
    }
    const quickBtn = e.target.closest('[data-cat-quick]');
    if (quickBtn) {
      e.preventDefault();
      quick(quickBtn);
      return;
    }
    const skip = e.target.closest('[data-review-skip]');
    if (skip) {
      e.preventDefault();
      markReviewed(skip);
      return;
    }
    const sel = e.target.closest('[data-cat-selected]');
    if (sel) {
      e.preventDefault();
      const form = sel.closest('form') || document;
      const ids = Array.from(form.querySelectorAll('input[name="row_id"]:checked')).map(function (c) { return c.value; });
      if (!ids.length) {
        alert('Tick the rows you want to categorize first.');
        return;
      }
      const proxy = h('span');
      proxy.dataset.cat = 'staging';
      proxy.dataset.batch = sel.dataset.batch || '';
      proxy.dataset.title = ids.length + ' selected row' + (ids.length === 1 ? '' : 's');
      proxy.dataset.remember = 'false';
      proxy._ids = ids;
      open(proxy);
      return;
    }
    const chip = e.target.closest('[data-cat]');
    if (chip) {
      e.preventDefault();
      open(chip);
    }
  });
})();
