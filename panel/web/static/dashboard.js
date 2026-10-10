"use strict";
(() => {
  const { $, $$, el, api, showAlert, busy, toast } = VB;
  let settings = {};

  function onAuthError(e) {
    if (e.status === 401) { window.location.href = "/_vb/login?next=/_vb/"; return true; }
    return false;
  }
  function fail(e) { if (!onAuthError(e)) toast(e.message, "err"); }

  // ---- Load ----
  async function load() {
    try {
      settings = await api("/_vb/api/settings");
    } catch (e) { return fail(e); }
    const sys = settings.system;
    $$('[data-bind="ip"]').forEach((n) => (n.textContent = sys.publicIP || "unknown"));
    $$('[data-bind="yourIP"]').forEach((n) => (n.textContent = settings.yourIP));
    $$('[data-bind="mem"]').forEach((n) => (n.textContent = sys.memoryGB ? `${sys.memoryGB} GB` : "unknown"));
    $$('[data-bind="cpu"]').forEach((n) => (n.textContent = sys.cpus));

    const bf = $("#browser-form");
    bf.startPage.value = settings.startPage;
    bf.querySelector(`input[name=restore][value=${settings.restoreSession ? "yes" : "no"}]`).checked = true;
    $("#account-form").username.value = settings.username;
    if (sys.tlsMode === "proxy") {
      $("#domain-form").replaceWith(el("p", { class: "muted small" },
        "Your domain and HTTPS are managed by your reverse proxy (e.g. Coolify). To change the domain, change it there."));
    }
    renderLogins(settings.recentLogins || []);
  }

  function renderLogins(list) {
    const body = $("#logins");
    if (!list.length) {
      body.replaceChildren(el("tr", {}, el("td", { colspan: "3", class: "muted" }, "No sign-ins recorded yet.")));
      return;
    }
    body.replaceChildren(...list.map((l) => {
      const ipCell = el("td", { class: "mono" }, l.ip);
      if (l.ip === settings.yourIP) ipCell.append(el("span", { class: "you" }, "you"));
      return el("tr", {},
        el("td", { title: new Date(l.time).toLocaleString() }, VB.relTime(l.time)),
        ipCell,
        el("td", { title: l.agent }, VB.device(l.agent)));
    }));
  }

  // Inline "are you sure?" instead of a browser dialog.
  function confirmInline(area, btn, label, action) {
    btn.addEventListener("click", () => {
      const yes = el("button", { class: "btn btn-primary", type: "button" }, label);
      const no = el("button", { class: "btn btn-ghost", type: "button" }, "Cancel");
      const original = [...area.childNodes];
      area.replaceChildren(yes, no);
      no.addEventListener("click", () => area.replaceChildren(...original));
      yes.addEventListener("click", async () => {
        busy(yes, true);
        try { await action(); } catch (e) { fail(e); }
        area.replaceChildren(...original);
      });
    });
  }

  // ---- Browser ----
  $("#browser-form").addEventListener("submit", async (e) => {
    e.preventDefault();
    const f = e.target, btn = f.querySelector("button[type=submit]");
    busy(btn, true);
    try {
      const r = await api("/_vb/api/settings/browser", {
        startPage: f.startPage.value,
        restoreSession: f.querySelector("input[name=restore]:checked").value === "yes",
      });
      f.startPage.value = r.startPage;
      toast("Browser preferences saved. They apply the next time it starts.");
    } catch (err) { fail(err); }
    busy(btn, false);
  });

  confirmInline($("#restart-area"), $("#restart-btn"), "Yes, restart", async () => {
    await api("/_vb/api/browser/restart", {});
    toast("Restarting the browser. It'll be back in about 10 seconds.");
  });

  // ---- Account ----
  const af = $("#account-form");
  const meter = $("#meter"), meterText = $("#meter-text"), confirmField = $("#confirm-field");
  VB.bindMeter(af.newPassword, meter, meterText);
  af.newPassword.addEventListener("input", () => {
    const has = af.newPassword.value.length > 0;
    [meter, meterText, confirmField].forEach((n) => n.classList.toggle("hidden", !has));
  });
  $("#gen-pass").addEventListener("click", () => {
    const p = VB.generatePassword();
    af.newPassword.value = af.confirmPassword.value = p;
    af.newPassword.type = "text";
    af.newPassword.dispatchEvent(new Event("input"));
    VB.copy(p, "New password generated and copied. Save it somewhere safe.");
  });
  af.addEventListener("submit", async (e) => {
    e.preventDefault();
    const np = af.newPassword.value;
    if (np && np.length < 10) return toast("New password must be at least 10 characters.", "err");
    if (np && np !== af.confirmPassword.value) return toast("New passwords don't match.", "err");
    const btn = af.querySelector("button[type=submit]");
    busy(btn, true);
    try {
      await api("/_vb/api/settings/account", {
        username: af.username.value.trim(),
        newPassword: np,
        currentPassword: af.currentPassword.value,
      });
      af.newPassword.value = af.confirmPassword.value = af.currentPassword.value = "";
      af.newPassword.type = "password";
      af.newPassword.dispatchEvent(new Event("input"));
      toast(np ? "Password changed. Other devices were signed out." : "Account updated.");
      load();
    } catch (err) { fail(err); }
    busy(btn, false);
  });

  // ---- Domain ----
  const df = $("#domain-form"), save = $("#domain-save");
  df.domain.addEventListener("input", () => { save.disabled = true; $("#dns-result").replaceChildren(); });
  df.domain.addEventListener("keydown", (e) => { if (e.key === "Enter" && save.disabled) { e.preventDefault(); checkDNS(); } });
  $("#dns-check-btn").addEventListener("click", checkDNS);
  async function checkDNS() {
    const btn = $("#dns-check-btn");
    if (!df.domain.value.trim()) return df.domain.focus();
    busy(btn, true);
    try {
      const r = await api("/_vb/api/dns-check", { domain: df.domain.value });
      df.domain.value = r.domain;
      VB.renderDNS($("#dns-result"), r);
      if (r.domain === settings.domain) {
        showAlert($("#dns-result"), "info", "That's already your domain", "");
        save.disabled = true;
      } else {
        save.disabled = r.status === "invalid";
        save.textContent = r.status === "ok" ? "Switch domain" : "Switch anyway";
      }
    } catch (err) { fail(err); }
    busy(btn, false);
  }
  df.addEventListener("submit", async (e) => {
    e.preventDefault();
    if (save.disabled) return;
    busy(save, true);
    try {
      const r = await api("/_vb/api/settings/domain", { domain: df.domain.value });
      showAlert($("#dns-result"), "ok", "Domain switched", `Taking you to ${r.url}… Sign in again there.`);
      setTimeout(() => (window.location.href = r.url), 2500);
    } catch (err) { fail(err); busy(save, false); }
  });

  // ---- Security ----
  const revokeBtn = $("#revoke-btn");
  const revokeArea = el("span", { class: "confirm-inline" });
  revokeBtn.replaceWith(revokeArea);
  revokeArea.append(revokeBtn);
  confirmInline(revokeArea, revokeBtn, "Yes, sign them out", async () => {
    await api("/_vb/api/sessions/revoke", {});
    toast("All other devices have been signed out.");
  });

  load();
})();
