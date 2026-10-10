"use strict";
// Shared helpers for the panel pages. No dependencies, no build step.
const VB = (() => {
  const $ = (sel, el = document) => el.querySelector(sel);
  const $$ = (sel, el = document) => [...el.querySelectorAll(sel)];

  const svg = (paths, sw = 2) =>
    `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="${sw}" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${paths}</svg>`;
  const ICONS = {
    check: svg('<path d="M20 6 9 17l-5-5"/>', 2.6),
    alert: svg('<circle cx="12" cy="12" r="10"/><path d="M12 8v4M12 16h.01"/>'),
    x: svg('<path d="M18 6 6 18M6 6l12 12"/>', 2.6),
    ok: svg('<circle cx="12" cy="12" r="10"/><path d="m8 12 3 3 5-6"/>'),
  };

  async function api(path, body) {
    const opts = { credentials: "same-origin", headers: {} };
    if (body !== undefined) {
      opts.method = "POST";
      opts.headers["Content-Type"] = "application/json";
      opts.body = JSON.stringify(body);
    }
    let res;
    try {
      res = await fetch(path, opts);
    } catch {
      throw new Error("Can't reach the server. Check your connection and try again.");
    }
    let data = {};
    try { data = await res.json(); } catch { /* not JSON */ }
    if (!res.ok) {
      const err = new Error(data.error || `Something went wrong (HTTP ${res.status}).`);
      err.status = res.status;
      throw err;
    }
    return data;
  }

  function el(tag, attrs = {}, ...children) {
    const node = document.createElement(tag);
    for (const [k, v] of Object.entries(attrs)) {
      if (k === "html") node.innerHTML = v; // only ever used with our own ICONS
      else if (k === "class") node.className = v;
      else node.setAttribute(k, v);
    }
    for (const c of children) if (c != null) node.append(c);
    return node;
  }

  // kind: ok | warn | err | info
  function alertBox(kind, title, message) {
    const icon = kind === "ok" ? ICONS.ok : ICONS.alert;
    const body = el("div", { class: "alert-body" });
    if (title) body.append(el("strong", {}, title), message ? document.createElement("br") : null);
    if (message) body.append(message);
    return el("div", { class: `alert alert-${kind}`, role: kind === "err" ? "alert" : "status" }, el("span", { html: icon }), body);
  }

  function showAlert(container, kind, title, message) {
    container.replaceChildren(alertBox(kind, title, message));
  }

  function toast(message, kind = "ok") {
    let box = $("#toasts");
    if (!box) {
      box = el("div", { class: "toasts", id: "toasts", "aria-live": "polite" });
      document.body.append(box);
    }
    const t = el("div", { class: `toast ${kind === "err" ? "err" : ""}` },
      el("span", { html: kind === "err" ? ICONS.alert : ICONS.ok }), el("span", {}, message));
    box.append(t);
    setTimeout(() => t.remove(), kind === "err" ? 6000 : 3500);
  }

  function busy(btn, on) {
    if (!btn) return;
    btn.disabled = on;
    btn.classList.toggle("loading", on);
  }

  async function copy(text, message = "Copied to clipboard") {
    try {
      await navigator.clipboard.writeText(text);
      toast(message);
    } catch {
      toast("Couldn't copy. Select the text and copy it by hand.", "err");
    }
  }

  function initPasswordToggles() {
    $$("[data-toggle-password]").forEach((btn) => {
      btn.addEventListener("click", () => {
        const input = btn.parentElement.querySelector("input");
        const show = input.type === "password";
        input.type = show ? "text" : "password";
        btn.setAttribute("aria-label", show ? "Hide password" : "Show password");
      });
    });
  }

  const WEAK = /^(password|admin|123456|qwerty|letmein|welcome|iloveyou)/i;
  function strength(pw) {
    if (pw.length < 10) return { score: 0, label: `Too short: ${pw.length}/10 characters` };
    const classes = [/[a-z]/, /[A-Z]/, /\d/, /[^A-Za-z0-9]/].filter((r) => r.test(pw)).length;
    let score = 1;
    if (pw.length >= 14) score++;
    if (classes >= 3) score++;
    if (pw.length >= 20 || (pw.length >= 12 && classes === 4)) score++;
    if (WEAK.test(pw) || /^(.)\1+$/.test(pw)) score = 1;
    score = Math.min(score, 4);
    return { score, label: ["", "Weak", "Okay", "Strong", "Very strong"][score] };
  }

  function bindMeter(input, meter, text) {
    const update = () => {
      const { score, label } = strength(input.value);
      meter.dataset.score = score;
      meter.firstElementChild.style.width = input.value ? `${Math.max(score, 0.4) * 25}%` : "0";
      text.textContent = input.value ? label : "At least 10 characters.";
    };
    input.addEventListener("input", update);
    update();
    return update;
  }

  function generatePassword(len = 20) {
    const chars = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789-_!@#%+=";
    const out = [];
    const buf = new Uint32Array(len);
    crypto.getRandomValues(buf);
    for (const n of buf) out.push(chars[n % chars.length]);
    return out.join("");
  }

  function renderDNS(container, r) {
    const titles = {
      ok: `${r.domain} points to this server`,
      mismatch: `${r.domain} points somewhere else`,
      missing: `No DNS record for ${r.domain} yet`,
      invalid: "Not a valid domain",
    };
    const kind = r.status === "ok" ? "ok" : r.status === "invalid" ? "err" : "warn";
    showAlert(container, kind, titles[r.status] || "DNS check", r.message);
  }

  function relTime(iso) {
    const d = new Date(iso);
    const s = Math.round((Date.now() - d.getTime()) / 1000);
    if (s < 60) return "just now";
    if (s < 3600) return `${Math.floor(s / 60)} min ago`;
    if (s < 86400) return `${Math.floor(s / 3600)} h ago`;
    if (s < 86400 * 7) return `${Math.floor(s / 86400)} d ago`;
    return d.toLocaleDateString();
  }

  function device(ua) {
    if (!ua) return "Unknown";
    const b = /Edg\//.test(ua) ? "Edge" : /OPR\//.test(ua) ? "Opera" : /Firefox\//.test(ua) ? "Firefox"
      : /Chrome\//.test(ua) ? "Chrome" : /Safari\//.test(ua) ? "Safari" : /curl/i.test(ua) ? "curl" : "Browser";
    const os = /iPhone|iPad/.test(ua) ? "iOS" : /Android/.test(ua) ? "Android" : /Windows/.test(ua) ? "Windows"
      : /Mac OS X/.test(ua) ? "macOS" : /CrOS/.test(ua) ? "ChromeOS" : /Linux/.test(ua) ? "Linux" : "";
    return os ? `${b} on ${os}` : b;
  }

  initPasswordToggles();
  return { $, $$, el, api, ICONS, alertBox, showAlert, toast, busy, copy, strength, bindMeter, generatePassword, renderDNS, relTime, device };
})();
