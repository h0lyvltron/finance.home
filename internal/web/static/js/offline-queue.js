/* Offline quick-add queue — localStorage → sync when online.
   Isolated from Datastar morph targets (only touches #qa-error / #qa-offline). */
(function () {
  var KEY = "finance.home.offlineQueue.v1";

  function csrf() {
    var el = document.querySelector("#qa-form input[name=csrf]");
    return el ? el.value : "";
  }

  function readQueue() {
    try {
      return JSON.parse(localStorage.getItem(KEY) || "[]");
    } catch (e) {
      return [];
    }
  }

  function writeQueue(items) {
    localStorage.setItem(KEY, JSON.stringify(items));
    paintBadge();
  }

  function paintBadge() {
    var el = document.getElementById("qa-offline");
    if (!el) return;
    var n = readQueue().length;
    if (n > 0) {
      el.hidden = false;
      el.textContent = n + " queued offline — will sync when back online.";
    } else if (!navigator.onLine) {
      el.hidden = false;
      el.textContent = "You're offline. Saves go to the local queue.";
    } else {
      el.hidden = true;
      el.textContent = "";
    }
  }

  function formPayload(form) {
    var fd = new FormData(form);
    var obj = {};
    fd.forEach(function (v, k) {
      obj[k] = v;
    });
    if (!obj.client_key) {
      obj.client_key = "offline-" + Date.now() + "-" + Math.random().toString(36).slice(2, 8);
    }
    return obj;
  }

  function enqueueFromForm(form) {
    var item = formPayload(form);
    if (!item.amount) return false;
    var q = readQueue();
    q.push(item);
    writeQueue(q);
    var err = document.getElementById("qa-error");
    if (err) {
      err.className = "alert alert-ok";
      err.innerHTML = "Saved offline. <a href=\"/drafts\">Drafts</a> after sync.";
    }
    var amt = document.getElementById("qa-amount");
    if (amt) amt.value = "";
    return true;
  }

  async function syncOne(item) {
    var body = new URLSearchParams();
    Object.keys(item).forEach(function (k) {
      if (item[k] != null) body.set(k, item[k]);
    });
    if (!body.get("csrf")) body.set("csrf", csrf());
    var res = await fetch("/transactions/draft/sync", {
      method: "POST",
      headers: {
        "Content-Type": "application/x-www-form-urlencoded",
        "X-CSRF-Token": body.get("csrf") || "",
      },
      body: body.toString(),
      credentials: "same-origin",
    });
    if (!res.ok) throw new Error("sync failed");
    return res.json();
  }

  async function flushQueue() {
    if (!navigator.onLine) {
      paintBadge();
      return;
    }
    var q = readQueue();
    if (!q.length) {
      paintBadge();
      return;
    }
    var remaining = [];
    for (var i = 0; i < q.length; i++) {
      try {
        await syncOne(q[i]);
      } catch (e) {
        remaining.push(q[i]);
      }
    }
    writeQueue(remaining);
  }

  window.__qaSaveDraft = function () {
    var form = document.getElementById("qa-form");
    if (!form) return;
    var item = formPayload(form);
    if (!item.amount) {
      var err = document.getElementById("qa-error");
      if (err) {
        err.className = "alert alert-error";
        err.textContent = "Enter an amount to save a draft.";
      }
      return;
    }
    item.client_key = item.client_key || "draft-" + Date.now();
    var body = new URLSearchParams();
    Object.keys(item).forEach(function (k) {
      if (item[k] != null) body.set(k, item[k]);
    });
    body.set("csrf", csrf());
    fetch("/transactions/draft", {
      method: "POST",
      headers: {
        "Content-Type": "application/x-www-form-urlencoded",
        "X-CSRF-Token": csrf(),
        Accept: "application/json",
      },
      body: body.toString(),
      credentials: "same-origin",
    }).then(function (res) {
      if (!res.ok) throw new Error("draft failed");
      var err = document.getElementById("qa-error");
      if (err) {
        err.className = "alert alert-ok";
        err.innerHTML = 'Draft saved. <a href="/drafts">View drafts</a>';
      }
      var amt = document.getElementById("qa-amount");
      if (amt) amt.value = "";
    }).catch(function () {
      enqueueFromForm(form);
    });
  };

  document.addEventListener("DOMContentLoaded", function () {
    var form = document.getElementById("qa-form");
    if (form) {
      form.addEventListener(
        "submit",
        function (ev) {
          if (!navigator.onLine) {
            ev.preventDefault();
            ev.stopImmediatePropagation();
            enqueueFromForm(form);
          }
        },
        true
      );
    }
    paintBadge();
    flushQueue();
  });

  window.addEventListener("online", flushQueue);
})();
