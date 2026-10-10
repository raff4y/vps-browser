# HANDOVER — vps-browser

Technical notes for anyone (human or AI agent) working on this repo: the goal, architecture, decisions, testing and roadmap.

---

## 1. What we're building (the goal)

The user lives somewhere that blocks or throttles some websites (for example, Pakistan blocked YouTube from 2012 to 2016 and X/Twitter in 2024–25). They have a **VPS** in another country and want to:

1. Open **their own domain** (e.g. `https://browse.example.com`) from any device.
2. Get a **username/password login prompt**.
3. After logging in, use a **full web browser that actually runs on the VPS**, so every website sees the **VPS's IP address and uses the VPS's internet connection**, not the user's local connection.

It is a self-hosted "remote browser" (browser isolation). The user's device only receives a video-like stream of the remote browser and sends back mouse and keyboard input. Nothing is installed on the client device, no VPN app is needed, and any modern browser works (phone or PC).

**Non-goals for v1:** no multiple users or accounts system, no VPN, and no system-wide proxy. Those are listed as possible future work in §8.

---

## 2. Architecture

```
 User's device                                VPS (abroad)
 ┌─────────────────────┐   HTTPS :443   ┌──────────────────────────────────────────┐
 │ Any browser          │──────────────▶│ caddy (container)                         │
 │ https://DOMAIN       │◀──────────────│  - auto HTTPS (Let's Encrypt)             │
 └─────────────────────┘  websocket     │  - basic_auth (username + bcrypt hash)    │
                          stream        │  - reverse_proxy → browser:3000           │
                                        │            │  (private docker network)    │
                                        │            ▼                              │
                                        │ browser (container)                       │
                                        │  lscr.io/linuxserver/chromium             │
                                        │  - real Chromium on a virtual desktop     │
                                        │  - streamed to the web page               │
                                        │  - browses using the VPS IP ─────────────────▶ Internet
                                        └──────────────────────────────────────────┘
```

- **Only Caddy publishes ports** (80, 443/tcp, 443/udp). The `browser` container has **no published ports**. It can only be reached through Caddy on the `internal` docker network. **Never add `ports:` to the browser service**, because that would skip the login.
- The login is HTTP basic auth done by Caddy, so the browser shows its built-in username/password popup. The password is stored only as a bcrypt hash.
- Browser data (bookmarks, cookies, logged-in sites, history) persists in the Docker named volume `vps-browser-data`. It is deliberately **not** a bind mount: the Selkies desktop creates its Wayland Unix socket in `/config/.XDG`, and Docker Desktop bind mounts on Windows/macOS can't hold sockets (the compositor fails with `could not bind a wayland-N socket ... os error 5`).
- TLS certs persist in `./data/caddy`.

### Why this stack (decisions already made — don't re-litigate without reason)

| Decision | Reason |
|---|---|
| `linuxserver/chromium` image | Maintained, single HTTP port (3000), audio and clipboard support, persistent profile, simple env-var config. LinuxServer has been moving its GUI images from the KasmVNC base to the Selkies base. Either way it serves the web UI on port 3000, so the proxy config is the same. |
| Caddy | Automatic HTTPS with zero config, websocket proxying works out of the box, built-in `basic_auth` with bcrypt. |
| Basic auth at the proxy | Simplest secure option. The browser container's own auth (`CUSTOM_USER`/`PASSWORD`) is deliberately **not** used, so there is no double login. |
| Docker Compose | One command to deploy on any Linux VPS. |

**Alternative considered:** [m1k1o/neko](https://github.com/m1k1o/neko) streams over WebRTC, so video and YouTube are smoother. It needs an extra UDP/TCP port for WebRTC and is more complex to set up. It's the upgrade path if video quality over the current stream isn't good enough (see §8).

---

## 3. Repo layout

| File | Purpose |
|---|---|
| `docker-compose.yml` | Two services: `browser` (Chromium) and `caddy` (HTTPS + login). |
| `Caddyfile` | Caddy config. Reads `DOMAIN`, `ACME_EMAIL`, `AUTH_USER`, `AUTH_HASH` from env via `{$VAR}`. |
| `.env.example` | Template for `.env`. |
| `.env` | **Real secrets, git-ignored.** Created by `setup.sh` on the VPS. |
| `setup.sh` | One-shot installer for an Ubuntu/Debian VPS. Installs Docker, asks for domain, email, username, password, start page and timezone (or reads them from env vars), hashes the password, writes `.env`, checks DNS, opens the ufw firewall, then runs `docker compose up -d`. Safe to re-run. |
| `.gitignore` | Ignores `.env` and `data/`. |
| `.gitattributes` | Forces LF line endings so `setup.sh` runs on Linux even when edited on Windows. |
| `.github/workflows/ci.yml` | CI: shellcheck, compose and Caddyfile validation, and a check that the browser service publishes no ports. |
| `LICENSE` | MIT. |
| `README.md` | Short human-facing usage guide. |
| `HANDOVER.md` | This file. |

### Environment variables (`.env`)

| Var | Example | Notes |
|---|---|---|
| `DOMAIN` | `browse.example.com` | Needs a DNS **A record** pointing at the VPS IP. `localhost` works for local testing (Caddy uses its own internal CA, so the certificate warning is expected). |
| `ACME_EMAIL` | `you@example.com` | Let's Encrypt contact email. |
| `AUTH_USER` | `admin` | Login username. |
| `AUTH_HASH` | `'$2a$14$...'` | bcrypt hash. **Must be wrapped in single quotes** in `.env`, otherwise Compose treats the `$` signs as variables. Generate it with `docker run --rm caddy:2 caddy hash-password --plaintext 'PASSWORD'`. |
| `START_PAGE` | `https://www.youtube.com` | Passed to Chromium via `CHROME_CLI`. |
| `TZ` | `UTC` | Timezone inside the browser (e.g. `Asia/Karachi`). |

---

## 4. How to deploy (on the VPS)

Requirements: Ubuntu 22.04+/Debian 12+ VPS, **2 GB RAM minimum (4 GB recommended)**, root access, and a domain or subdomain with an A record pointing at the VPS IP. Ports 80 and 443 must be open in the cloud provider's firewall or security group.

```bash
# on the VPS
git clone https://github.com/raff4y/vps-browser.git
cd vps-browser
sudo ./setup.sh
```

Then open `https://DOMAIN` and log in.

Manual alternative: `cp .env.example .env`, edit it, then `docker compose up -d`.

### Day-to-day ops

```bash
docker compose ps                    # status
docker compose logs -f caddy         # HTTPS / login issues
docker compose logs -f browser       # browser issues
docker compose pull && docker compose up -d   # update images
docker compose restart browser       # browser frozen
docker compose down                  # stop everything
```

To change the password, re-run `sudo ./setup.sh` and choose to reconfigure, or generate a new hash, replace `AUTH_HASH` in `.env` (keep the single quotes), then run `docker compose up -d caddy`.

---

## 5. How to test locally (Windows, Docker Desktop)

```powershell
# in the repo folder: create .env from .env.example with DOMAIN=localhost
# and the hash of a test password (e.g. test123)
docker compose up -d
```

Open `https://localhost`, accept the self-signed certificate warning, and log in.

Things to verify:
1. Without credentials, `curl -k -I https://localhost` returns **401**.
2. With credentials, `curl -k -I -u admin:test123 https://localhost` returns **200**.
3. The page loads a Chromium desktop, and you can browse and play a YouTube video with sound.
4. Inside the remote browser, visiting `https://api.ipify.org` shows the **VPS IP** (when tested locally it shows the home IP, which is expected).
5. Nothing answers on port 3000 from the host (the browser container isn't exposed).
6. The stream websocket is guarded too: an upgrade request to `/api/websockets` returns **401** without credentials and **101** with them:
   `curl -sk --http1.1 -u admin:test123 -i -H 'Connection: Upgrade' -H 'Upgrade: websocket' -H 'Sec-WebSocket-Version: 13' -H 'Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==' https://localhost/api/websockets`

Ports 80 and 443 must be free on the PC. If IIS, Skype or another app holds them, stop it first or temporarily change the Caddy ports.

---

## 6. Current status

- [x] Compose, Caddyfile, `.env.example`, `setup.sh`, README, CI and MIT license.
- [x] Local end-to-end test on Docker Desktop (see §5).
- [ ] Deployed and verified on a real VPS with a real domain (§4, then the §5 checklist).

---

## 7. Security rules (must keep)

1. The browser container must **never** publish a port. All access goes through Caddy plus auth.
2. Never commit `.env` or `data/`, and never share the `vps-browser-data` volume. The profile contains cookies and sessions for every site you log into.
3. Keep `basic_auth` on the whole site. Don't add unauthenticated paths.
4. Use a strong password. Basic auth has no rate limiting. If the domain gets attacked, add fail2ban on Caddy's access log or move to Authelia or Authentik (see §8).
5. Anyone who gets the login has full control of a browser holding your logged-in sessions. Treat it like a password manager.
6. Some countries regulate VPN and proxy use. Whether to use this is the deployer's decision.

---

## 8. Possible next steps (backlog, in rough priority order)

1. **Deploy to the VPS** (§4) and verify the checklist in §5 against the real domain.
2. **Smoother video:** if YouTube is choppy, try tuning the stream settings in the web UI sidebar first. If that's not enough, switch the `browser` service to `m1k1o/neko` (WebRTC, with its own login), keeping Caddy in front. It needs a UDP mux port opened (e.g. `NEKO_WEBRTC_UDPMUX=52000`).
3. **Better login page and brute-force protection:** replace `basic_auth` with Authelia (supports 2FA/TOTP) via Caddy `forward_auth`, or add fail2ban.
4. **Whole-device proxy (optional):** add a password-protected HTTPS or SOCKS5 proxy, or a WireGuard container, so phone apps (like the YouTube app) can also use the VPS IP. This is separate from the remote browser.
5. **Multiple users:** separate browser containers per user, routed by subdomain or path, each with its own auth.
6. **Auto-updates:** Watchtower, or a cron job running `docker compose pull && docker compose up -d`.
