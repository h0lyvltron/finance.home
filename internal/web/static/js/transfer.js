// Live preview of a transfer's effect on both accounts. Balances come from the
// option data attributes (signed cents; liabilities are negative when owed).
(function () {
  if (window.__transferPreview) return;
  window.__transferPreview = true;

  function cents(v) {
    v = String(v || "").replace(/[^0-9.\-]/g, "");
    var n = parseFloat(v);
    return isFinite(n) ? Math.round(Math.abs(n) * 100) : 0;
  }

  function formatter(form) {
    var cur = form.getAttribute("data-currency") || "USD";
    try {
      var nf = new Intl.NumberFormat(undefined, { style: "currency", currency: cur });
      return function (c) { return nf.format(c / 100); };
    } catch (e) {
      return function (c) { return (c / 100).toFixed(2); };
    }
  }

  function account(sel) {
    var o = sel && sel.selectedOptions && sel.selectedOptions[0];
    if (!o || !o.value) return null;
    return {
      id: o.value,
      name: o.getAttribute("data-name") || o.textContent,
      type: o.getAttribute("data-type"),
      bal: parseInt(o.getAttribute("data-balance"), 10) || 0,
    };
  }

  function isDebt(a) { return a.type === "credit" || a.type === "loan"; }

  function describe(a, delta, fmt) {
    if (isDebt(a)) {
      var before = -a.bal, after = -(a.bal + delta);
      var diff = after - before;
      return a.name + ": owed " + fmt(before) + " → " + fmt(after) +
        (diff === 0 ? "" : diff < 0 ? " (down " + fmt(-diff) + ")" : " (up " + fmt(diff) + ")");
    }
    return a.name + ": " + fmt(a.bal) + " → " + fmt(a.bal + delta) +
      " (" + (delta < 0 ? "−" : "+") + fmt(Math.abs(delta)) + ")";
  }

  function update(form) {
    var from = account(form.querySelector("[name=from_account_id]"));
    var to = account(form.querySelector("[name=to_account_id]"));
    var amt = cents((form.querySelector("[name=amount]") || {}).value);
    var interestField = form.querySelector("[data-interest-field]");
    var interestInput = form.querySelector("[name=interest]");
    var preview = form.querySelector("[data-transfer-preview]");
    var fmt = formatter(form);

    var showInterest = !!(to && isDebt(to));
    if (interestField) interestField.hidden = !showInterest;
    if (!showInterest && interestInput) interestInput.value = "";
    var interest = showInterest ? cents(interestInput && interestInput.value) : 0;

    if (!preview) return;
    preview.textContent = "";
    if (!from || !to || !amt) {
      preview.hidden = true;
      return;
    }
    preview.hidden = false;
    var lines = [];
    var warn = "";
    if (from.id === to.id) {
      warn = "Pick two different accounts.";
    } else {
      lines.push(describe(from, -amt, fmt));
      lines.push(describe(to, amt - interest, fmt));
      if (interest > amt) warn = "Interest can't be more than the payment.";
      else if (interest > 0) lines.push("Includes " + fmt(interest) + " interest, recorded as a Loan interest expense on " + to.name + ".");
      if (isDebt(from)) lines.push("Moving money out of " + from.name + " adds to what you owe on it, like a cash advance.");
    }
    var title = document.createElement("div");
    title.className = "tiny muted";
    title.textContent = "After this transfer (current balances)";
    preview.appendChild(title);
    lines.forEach(function (t) {
      var d = document.createElement("div");
      d.textContent = t;
      preview.appendChild(d);
    });
    if (warn) {
      var w = document.createElement("div");
      w.className = "transfer-warn";
      w.textContent = warn;
      preview.appendChild(w);
    }
  }

  function onEvent(e) {
    var f = e.target && e.target.closest && e.target.closest("form[data-transfer-form]");
    if (f) update(f);
  }
  document.addEventListener("input", onEvent);
  document.addEventListener("change", onEvent);
  function init() {
    document.querySelectorAll("form[data-transfer-form]").forEach(update);
  }
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", init);
  else init();
})();
