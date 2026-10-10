# HANDOVER — vps-browser

Technical notes for anyone (human or AI agent) working on this repo: the goal, architecture, design decisions, how to test, and the roadmap. User-facing docs are in [README.md](README.md).

---

## 1. Goal

An open-source, self-hosted **remote browser** that anyone can deploy on their own VPS:

1. Run one command on a fresh server.
2. Finish setup in a web wizard: domain, account, start page. No config files.
3. Open `https://their-domain`, sign in, and use a real Chromium that runs on the server, so websites see the server's IP and connection.

The client only receives a video-like stream and sends input back. Nothing is installed on the client, and no VPN is involved.

**Non-goals (for now):** multiple users, VPN or system-wide proxy, and hosting more than one domain at a time. See §8.

---

## 2. Architecture

```
 User's device                        VPS
 ┌──────────────┐  :80/:443   ┌──────────────────────────────────────────────────────┐
 │ any browser  │────────────▶│ caddy  (only service with published ports)           │
 └──────────────┘             │  https://  on-demand TLS ── ask ──▶ panel:8081        │
                              │    /_vb/*  ───────────────────────▶ panel:8080        │
                              │    /*      forward_auth panel:8080/_vb/auth/check     │
                              │            then ──────────────────▶ browser:3000      │
                              │  http://   ───────────────────────▶ panel:8080        │
                              │                                                      │
                              │ panel  (Go, distroless, non-root, NO docker socket)  │
                              │  setup wizard · sign-in · dashboard · auth check     │
                              │  volumes: panel-data, browser-policies (rw),         │
                              │           browser-control (rw)                       │
                              │                                                      │
                              │ browser  (linuxserver/chromium + restart watcher)    │
                              │  volumes: browser-data:/config,                      │
                              │   browser-policies:/etc/chromium/policies/recommended│
                              │   browser-control:/vps-browser (both read-only)      │
                              └──────────────────────────────────────────────────────┘
 networks: edge = caddy + browser · control = caddy + panel
```

### Request flow
- **Before setup:** `http://SERVER-IP` goes to the panel, which serves `/_vb/setup`. That page is only usable with the **setup token** printed by the installer (`/_vb/setup?token=…` sets an HttpOnly cookie and then redirects to drop the token from the URL).
- **Setup done:** the domain is saved in `panel-data/config.json`. The first HTTPS request for that domain makes Caddy ask `panel:8081/tls-allowed?domain=…`. Only the configured domain gets a certificate (**on-demand TLS**), so **changing the domain needs no Caddy reload**.
- **Browser access:** Caddy's `forward_auth` calls `/_vb/auth/check`. The panel returns 200 for a valid session cookie. Otherwise it returns a 302 to `/_vb/login` (for page loads) or a 401 (for assets and websockets). The `Upgrade`/`Connection` headers are stripped from the auth subrequest.
- **Plain HTTP after setup:** redirects to `https://DOMAIN`, except `/_vb/api/cert-status` and static files, which the wizard's final "waiting for HTTPS" screen polls from `http://SERVER-IP`.
- **Wrong host over HTTPS** (e.g. an old domain whose certificate Caddy still has cached): redirects to the current domain.

### Panel (`panel/`)
- Go standard library plus `golang.org/x/crypto/bcrypt`. Templates and static files are embedded (`web/`), with no JS build step.
- `config.json` holds the domain, username, bcrypt hash (cost 12), session secret, session version, start page, setup token and the last 8 sign-ins. It's written atomically and re-read when its mtime changes, so CLI commands affect the running server.
- **Sessions:** an HMAC-signed cookie `vb_session` = base64(user)|version|expiry, valid for 30 days, HttpOnly, Secure, SameSite=Lax. Bumping `SessionVersion` signs everyone out. That happens on password or username change, "sign out others" and `reset`, and the current session is re-issued.
- **CSRF:** every POST must carry a matching `Origin` (or `Referer`).
- **Brute force:** a per-IP lockout after 5 failures (1 min, doubling, max 1 h), held in memory. The IP comes from `X-Forwarded-For`, which Caddy overwrites with the real client IP (verified: a spoofed header is ignored).
- **CSP** is `default-src 'self'`, so there are no inline scripts or styles. Use classes, not `style=` attributes.
- **CLI:** `/panel serve | setup-url | reset | version`. `reset` re-opens setup (keeping the domain and profile) and prints a new token URL.
- **Open setup window:** if the panel starts unconfigured, setup also works *without* the token for `openSetupWindow` (1 hour), so opening the domain or IP lands on the wizard (`App.canSetup`). The owner chose this trade-off: new certificates show up in public CT logs and bots scan them, so the window is kept short. The window is counted from panel start, so a restart of an unconfigured install re-opens it. `reset` on a running, already-configured panel never opens it, only the token works.
- **Start page:** written as Chromium **recommended** policies (`HomepageLocation`, `RestoreOnStartup`, `RestoreOnStartupURLs`) into the shared `browser-policies` volume. They're defaults, so the user can still override them inside Chromium.
- **Restart browser:** the panel writes a timestamp to `browser-control/restart`. `browser/restart-watcher` (an s6 custom service in the browser image) notices it and kills Chromium, and the image's own watchdog (`RESTART_APP=true`) relaunches it. **The panel deliberately has no Docker socket.**
- **Close browser** (to free the server's RAM): `POST /_vb/api/browser/close` creates `browser-control/stopped` and clears this device's session cookie (it goes to `/_vb/login?closed=1`). The watcher sees the file and kills Chromium. `browser/chromium-gate` replaces the image's launcher `/usr/bin/wrapped-chromium` (the original is moved to `wrapped-chromium-real`) and holds every watchdog relaunch in a `sleep 1` loop while the file exists. `authCheck` removes the file on the next signed-in **page load** of the browser (not on assets or websockets, so a forgotten tab reconnecting doesn't wake it up), and so does Restart. The Selkies desktop and stream keep running; only Chromium stops, and that's where nearly all of the memory goes. The flag survives container restarts, so a closed browser stays closed after a reboot. If upstream renames `wrapped-chromium`, the browser image build fails on purpose.
- **Public IP:** the `PUBLIC_IP` env var, or ipify / icanhazip / ifconfig.me, cached for 10 minutes.
- **DNS check:** resolves A/AAAA and compares them with the public IP. It warns about Cloudflare's proxy and about stray AAAA records.

### Proxy mode (`TLS_MODE=proxy`, used for Coolify)
- HTTPS and the domain belong to the outer proxy. The panel skips its HTTP-to-HTTPS and wrong-host redirects, the wizard hides the domain step (the domain is taken from the `Host` of the setup request), `cert-status` is always ready, and changing the domain from the dashboard is refused.
- The setup link is printed in the panel's logs as `https://YOUR-DOMAIN/_vb/setup?token=…`. The panel image has no shell, so Coolify's terminal can't be used; run `docker exec <panel> /panel reset` on the host instead.
- Client IP: both Caddyfiles send `X-Real-IP {client_ip}` to the panel. Behind Coolify, `trusted_proxies_strict` parses `X-Forwarded-For` right to left, so a client can't spoof it.

### Why these choices (don't re-litigate without reason)
| Decision | Reason |
|---|---|
| `linuxserver/chromium` | Maintained, a single HTTP port (3000), audio and clipboard, a persistent profile, and a built-in watchdog. The current base is Selkies (Wayland, websocket stream on `/api/websockets`). |
| Named volume for `/config` | Selkies creates its Wayland Unix socket in `/config/.XDG`. Docker Desktop bind mounts on Windows/macOS can't hold sockets (the compositor fails with `os error 5`). |
| Caddy on-demand TLS + `ask` | The domain can be set and changed from the UI without editing or reloading Caddy, and only the configured domain can get a certificate. |
| `forward_auth` to the panel | A real sign-in page (not the basic-auth popup), lockout, sign-out, and session management. |
| No Docker socket in the panel | The panel faces the internet. Docker access is root on the host. |
| Separate `edge` / `control` networks | The remote Chromium (which the user drives) can't reach the panel's internal endpoints. |
| Setup token | Otherwise anyone who finds a fresh server's IP could claim it first. |
| DuckDNS as the "no domain" suggestion | `sslip.io` and `nip.io` are **not** on the Public Suffix List, so all their users share one Let's Encrypt rate limit and issuance often fails. DuckDNS is on the PSL. |
| Images built on the server | Fully open source and reproducible, with no registry or CI publishing needed. The first install takes a few minutes. |

**Alternative considered:** [m1k1o/neko](https://github.com/m1k1o/neko) (WebRTC, smoother video). It needs an extra UDP port. It's the upgrade path if the stream quality isn't enough (§8).

---

## 3. Repo layout

| Path | Purpose |
|---|---|
| `install.sh` | One-line installer (`curl … \| sudo bash`): installs git/curl/Docker, adds swap on small servers, checks ports 80/443, clones to `/opt/vps-browser`, opens ufw/firewalld, links the CLI, builds, starts, and prints the setup URL. Re-running it updates the install. Supports the `REPO_URL`, `BRANCH` and `INSTALL_DIR` env vars (useful for forks). |
| `vps-browser` | Management CLI (symlinked to `/usr/local/bin`): status, logs, restart, stop/start, update, setup-url, reset, backup/restore, uninstall, version. |
| `docker-compose.yml` | The caddy, panel and browser services, plus networks and volumes. |
| `docker-compose.coolify.yml` | Variant for Coolify. Coolify's proxy owns 80/443 and does TLS, so nothing publishes host ports; Coolify routes the domain to `caddy:80`. The panel runs with `TLS_MODE=proxy`. |
| `coolify/` | `Dockerfile` + plain-HTTP `Caddyfile` (routing + `forward_auth`, `trusted_proxies private_ranges` + `trusted_proxies_strict`) baked into a Caddy image. Baked in because Coolify copies only the compose file to the server, so a `./Caddyfile` bind mount becomes an empty directory and Caddy fails to start. |
| `Caddyfile` | Routing and on-demand TLS. `TLS_MODE` picks the `tls_acme` or `tls_internal` snippet. |
| `panel/` | Control panel source (Go) and its `Dockerfile`. `web/templates`, `web/static`. |
| `browser/` | The browser image: linuxserver/chromium plus `restart-watcher` and `chromium-gate` (Restart / Close buttons). |
| `.env.example` | Optional settings: `TZ`, `PUBLIC_IP`, `TLS_MODE`, `HTTP_PORT`/`HTTPS_PORT`. Not needed for a normal install. |
| `docs/` | README screenshots (taken with the example IP 203.0.113.10 and domain browse.example.com). |
| `.github/workflows/ci.yml` | CI: shellcheck, go vet/build, compose and Caddyfile validation, a no-ports guard on the browser service. |

### Volumes
| Volume | Contents |
|---|---|
| `vps-browser-data` | Chromium profile: cookies, sessions, bookmarks. **Sensitive.** |
| `vps-browser-panel` | `config.json`: password hash, session secret. **Sensitive.** |
| `vps-browser-certs` | Caddy's certificates and ACME account. |
| `browser-policies`, `browser-control` | Panel to browser hand-off. Recreated as needed. |

---

## 4. Deploying

See the README. In short, `curl -fsSL https://raw.githubusercontent.com/raff4y/vps-browser/main/install.sh | sudo bash`, then open the printed link.

Requirements: Ubuntu 22.04+/Debian 12+ (or another Linux with Docker), 2 GB+ RAM, ports 80/443 open, and a domain with an A record pointing at the server.

---

## 5. Local development and testing

```bash
TLS_MODE=internal docker compose up -d --build
docker compose exec panel /panel setup-url     # use http://localhost/_vb/setup?token=… and domain "localhost"
```

`TLS_MODE=internal` uses Caddy's own CA and also allows `localhost` as a domain. Build or vet the panel without a local Go install:

```bash
docker run --rm -v "$PWD/panel:/src" -w /src golang:1.26-alpine sh -c 'gofmt -l . && go vet ./... && go build ./...'
```

Checks that should pass (all verified on Docker Desktop, 2026-10-10):
1. `http://localhost/` redirects to `/_vb/setup`. Setup returns 403 without the token and 200 with it.
2. POSTs without a matching `Origin` return 403.
3. Setup rejects a password under 10 characters. After setup, `/_vb/setup` redirects away and plain HTTP redirects to HTTPS.
4. `https://localhost/` while signed out returns 302 to `/_vb/login?next=…`. Assets return 401. A websocket to `/api/websockets` returns 401.
5. A wrong password returns 401. After 5 failures you get 429 even with the right password. A spoofed `X-Forwarded-For` doesn't help.
6. While signed in, `/` returns 200 and `/api/websockets` returns **101**.
7. Changing the start page updates `/etc/chromium/policies/recommended/vps-browser.json` in the browser container.
8. Restart browser: the Chromium PID changes, and the logs show the watcher and the watchdog.
8b. Close browser: you land on the sign-in page with the "browser is closed" notice, `pgrep -f /usr/lib/chromium/chromium` in the browser container finds nothing (and still nothing 30 s later), and memory use drops. After you sign in, Chromium is back within a few seconds. *(Not yet run.)*
9. Password change: the current session stays valid and other cookies get 401. Sign-out gives 401.
10. `tls-allowed` returns 200 for the configured domain and 403 otherwise. The browser container can't reach `panel:8080`.
11. `docker compose exec panel /panel reset` re-opens setup.

To look at the HTTPS-only pages in a browser without the certificate warning, run a throwaway Caddy on the `control` network that proxies `:8091` to `panel:8080` and sets `X-Forwarded-Proto https` and `Host`/`Origin` to the configured domain.

**Not yet tested:** the full install on a real VPS with a real domain and Let's Encrypt, and `install.sh` end to end on a clean server.

---

## 6. Current status

- [x] Panel (wizard, sign-in, dashboard, auth), Caddy on-demand TLS, browser restart watcher, installer and CLI.
- [x] Local end-to-end tests (§5) pass. The UI was checked in Chrome (desktop width).
- [x] Proxy mode tested locally behind a stand-in for Coolify's proxy: setup, sign-in, websocket 101, spoof-proof lockout, domain change refused.
- [ ] Real Coolify deployment with the new stack.
- [ ] Real VPS + domain + Let's Encrypt.
- [ ] UI checked at phone width (there's responsive CSS, but it hasn't been looked at).

---

## 7. Security rules (must keep)

1. Only `caddy` publishes ports (and in the Coolify file, nothing does). **Never add `ports:` to `browser` or `panel`.** CI enforces this.
2. Never give the panel the Docker socket.
3. Keep the browser off the `control` network.
4. Every non-`/_vb/` path must go through `forward_auth`. Don't add unauthenticated paths to the browser.
5. Keep `tls-allowed` strict (configured domain only). Otherwise anyone can make the server request certificates for arbitrary names.
6. Never commit `.env`, backups (`*.tgz`) or volume contents.
7. Some countries regulate VPN and proxy use. Whether to use this is the deployer's decision.

---

## 8. Possible next steps

1. **Test on a real VPS** with a real domain, including `install.sh` on a clean Ubuntu 24.04.
2. **2FA (TOTP)** for sign-in.
3. **Smoother video:** an optional `neko` (WebRTC) backend.
4. **Multiple users / browsers:** a browser container per user, routed by the panel.
5. **Whole-device proxy:** an optional authenticated SOCKS5/HTTPS proxy or WireGuard, for phone apps.
6. **Prebuilt images** on GHCR (needs a CI token with `workflow` and `packages` scopes) for faster installs.
7. **Persistent lockouts and audit log**, plus fail2ban integration.
