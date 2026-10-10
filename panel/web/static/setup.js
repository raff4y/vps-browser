"use strict";
(() => {
  const { $, $$, api, ICONS, showAlert, busy, copy, toast } = VB;
  // proxy = HTTPS and the domain are handled by e.g. Coolify, so skip the domain step.
  const state = { step: 1, ip: "", dnsOK: false, checkedDomain: "", proxy: false };

  // ---- Navigation ----
  function go(n) {
    state.step = n;
    $$(".step").forEach((s) => s.classList.toggle("hidden", Number(s.dataset.step) !== n));
    $$("#stepper li").forEach((li) => {
      const i = Number(li.dataset.step);
      li.classList.toggle("done", i < n);
      li.classList.toggle("active", i === n);
    });
    window.scrollTo({ top: 0, behavior: "smooth" });
    const first = $(`.step[data-step="${n}"] input:not([type=radio])`);
    if (first) setTimeout(() => first.focus(), 50);
  }
  const skip2 = (n, dir) => (n === 2 && state.proxy ? n + dir : n);
  $$("[data-next]").forEach((b) => b.addEventListener("click", () => go(skip2(state.step + 1, 1))));
  $$("[data-back]").forEach((b) => b.addEventListener("click", () => go(skip2(state.step - 1, -1))));

  // ---- 1. Server check ----
  function setCheck(key, status, text) {
    const v = $(`#syscheck [data-k="${key}"]`);
    const dot = v.parentElement.querySelector(".status-dot");
    v.textContent = text;
    dot.className = `status-dot ${status}`;
    dot.innerHTML = status === "ok" ? ICONS.check : status === "wait" ? "" : ICONS.alert;
  }

  api("/_vb/api/setup/info").then((info) => {
    const sys = info.system;
    state.ip = sys.publicIP;
    if (sys.tlsMode === "proxy") {
      state.proxy = true;
      $('#stepper li[data-step="2"]').classList.add("hidden");
      domainInput.value = location.hostname;
      $(".review-note .alert-body").textContent = "When you finish, this setup page closes for good. HTTPS is handled by your proxy (e.g. Coolify).";
    }
    setCheck("ip", sys.publicIP ? "ok" : "warn", sys.publicIP || "not detected");
    if (sys.memoryGB) setCheck("mem", sys.memoryGB >= 1.9 ? "ok" : "warn", `${sys.memoryGB} GB`);
    else setCheck("mem", "warn", "unknown");
    setCheck("cpu", "ok", String(sys.cpus));
    $$(".server-ip").forEach((e) => (e.textContent = sys.publicIP || "YOUR-SERVER-IP"));
    if (info.domain) { $("#domain").value = info.domain; updateDNSName(); }
    if (info.username) $("#username").value = info.username;
  }).catch((e) => {
    setCheck("ip", "err", "error");
    showAlert($("#syscheck").parentElement.insertBefore(document.createElement("div"), $("#syscheck")), "err", "Couldn't load server info", e.message);
  });

  // ---- 2. Domain ----
  const domainInput = $("#domain");
  const domainNext = $("#domain-next");
  const skip = $("#dns-skip");

  function cleanDomain(v) {
    return v.trim().toLowerCase().replace(/^https?:\/\//, "").replace(/[/?#].*$/, "").replace(/\.$/, "");
  }
  function updateDNSName() {
    const parts = cleanDomain(domainInput.value).split(".").filter(Boolean);
    $("#dns-name").textContent = parts.length > 2 ? parts.slice(0, -2).join(".") : parts.length === 2 ? "@" : "browse";
  }
  domainInput.addEventListener("input", () => {
    state.dnsOK = false;
    domainNext.disabled = true;
    skip.classList.add("hidden");
    $("#dns-result").replaceChildren();
    updateDNSName();
  });
  domainInput.addEventListener("keydown", (e) => { if (e.key === "Enter") { e.preventDefault(); checkDNS(); } });
  $("#dns-check-btn").addEventListener("click", checkDNS);
  $("[data-copy-ip]").addEventListener("click", () => copy(state.ip));

  async function checkDNS() {
    const btn = $("#dns-check-btn");
    if (!domainInput.value.trim()) { domainInput.focus(); return; }
    busy(btn, true);
    try {
      const r = await api("/_vb/api/dns-check", { domain: domainInput.value });
      domainInput.value = r.domain;
      updateDNSName();
      VB.renderDNS($("#dns-result"), r);
      state.checkedDomain = r.domain;
      state.dnsOK = r.status === "ok";
      domainNext.disabled = !state.dnsOK;
      skip.classList.toggle("hidden", r.status === "ok" || r.status === "invalid");
    } catch (e) {
      showAlert($("#dns-result"), "err", "DNS check failed", e.message);
    } finally {
      busy(btn, false);
    }
  }
  domainNext.addEventListener("click", () => go(3));
  skip.addEventListener("click", () => go(3));

  // ---- 3. Account ----
  const pw = $("#password"), pw2 = $("#password2");
  VB.bindMeter(pw, $("#meter"), $("#meter-text"));
  $("#gen-pass").addEventListener("click", () => {
    const p = VB.generatePassword();
    pw.value = pw2.value = p;
    pw.type = "text";
    pw.dispatchEvent(new Event("input"));
    copy(p, "Strong password generated and copied. Save it somewhere safe.");
  });
  $("#account-next").addEventListener("click", () => {
    const box = $("#account-error");
    const user = $("#username").value.trim();
    if (!/^[A-Za-z0-9._@-]{1,64}$/.test(user)) return showAlert(box, "err", "Check your username", "Use letters, numbers and . _ @ - only.");
    if (pw.value.length < 10) return showAlert(box, "err", "Password too short", "Use at least 10 characters.");
    if (pw.value.length > 72) return showAlert(box, "err", "Password too long", "Use at most 72 characters.");
    if (pw.value !== pw2.value) return showAlert(box, "err", "Passwords don't match", "Type the same password in both boxes.");
    box.replaceChildren();
    go(4);
  });

  // ---- 4. Browser ----
  const customField = $("#custom-start-field");
  $$("input[name=start]").forEach((r) => r.addEventListener("change", () => {
    customField.classList.toggle("hidden", r.value !== "custom" || !r.checked);
    if (r.value === "custom" && r.checked) $("#custom-start").focus();
  }));
  function startPage() {
    const v = $("input[name=start]:checked").value;
    if (v !== "custom") return v;
    let u = $("#custom-start").value.trim();
    if (u && !/^https?:\/\//i.test(u)) u = "https://" + u;
    return u;
  }
  $("#browser-next").addEventListener("click", () => {
    const u = startPage();
    try { new URL(u); } catch { return showAlert($("#browser-error"), "err", "Check the start page", "Enter a web address like https://example.com."); }
    $("#browser-error").replaceChildren();
    fillReview();
    go(5);
  });

  // ---- 5. Review & launch ----
  function restoreSession() { return $("input[name=restore]:checked").value === "yes"; }
  function fillReview() {
    $("#r-domain").textContent = "https://" + cleanDomain(domainInput.value);
    $("#r-user").textContent = $("#username").value.trim();
    $("#r-pass").textContent = `${"•".repeat(10)} (${pw.value.length} characters)`;
    $("#r-start").textContent = startPage();
    $("#r-restore").textContent = restoreSession() ? "Continue where I left off" : "Open the start page";
  }

  $("#finish").addEventListener("click", async () => {
    const btn = $("#finish");
    busy(btn, true);
    try {
      const r = await api("/_vb/api/setup", {
        domain: domainInput.value,
        username: $("#username").value.trim(),
        password: pw.value,
        startPage: startPage(),
        restoreSession: restoreSession(),
      });
      pw.value = pw2.value = "";
      $("#review").classList.add("hidden");
      $("#launch").classList.remove("hidden");
      $$("#stepper li").forEach((li) => { li.classList.add("done"); li.classList.remove("active"); });
      $("#open-link").href = r.url;
      waitForHTTPS(r.domain);
    } catch (e) {
      showAlert($("#finish-error"), "err", "Couldn't finish setup", e.message);
      busy(btn, false);
    }
  });

  function waitForHTTPS(domain) {
    const started = Date.now();
    const row = $("#cert-row");
    const dot = row.querySelector(".status-dot");
    const poll = async () => {
      let r = { ready: false };
      try { r = await api("/_vb/api/cert-status"); } catch { /* keep trying */ }
      if (r.ready) {
        dot.className = "status-dot ok";
        dot.innerHTML = ICONS.check;
        $("#cert-msg").textContent = `Secured https://${domain}`;
        $("#launch-icon").classList.add("success-ring");
        $("#launch-icon").innerHTML = ICONS.check;
        $("#launch-title").textContent = "You're all set!";
        $("#launch-lead").textContent = `Your private browser is live at https://${domain}. Bookmark it on every device you use.`;
        $("#cert-help").classList.add("hidden");
        return;
      }
      if (Date.now() - started > 90_000) $("#cert-help").classList.remove("hidden");
      setTimeout(poll, 3000);
    };
    poll();
  }

  go(1);
})();
