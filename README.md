# vps-browser

A private web browser that runs on your own VPS. Open your domain, log in, and browse. Every website sees your **VPS's IP and connection**, not yours, so sites that are blocked or throttled where you are work normally. There's no VPN app and nothing to install on your device, and it works from any modern browser on a phone or PC.

```
your phone/PC ──HTTPS + login──▶ VPS: Caddy ──▶ Chromium (in Docker) ──▶ internet (VPS IP)
```

- **Real Chromium** on a virtual desktop, streamed to a web page (via [linuxserver/chromium](https://docs.linuxserver.io/images/docker-chromium/)), with sound and clipboard support.
- **Automatic HTTPS** (Let's Encrypt) and a **username/password login** in front of everything (via [Caddy](https://caddyserver.com/)).
- **Persistent profile:** bookmarks, history and logged-in sites survive restarts.
- **One command** to install on a fresh Ubuntu/Debian server.

## Requirements

- An Ubuntu 22.04+ or Debian 12+ VPS with **2 GB RAM minimum** (4 GB recommended) and root access.
- A domain or subdomain with a DNS **A record** pointing at the VPS IP (e.g. `browse.example.com → 203.0.113.10`).
- Ports **80** and **443** open in your cloud provider's firewall or security group.

## Deploy

SSH into the VPS, then run:

```bash
git clone https://github.com/raff4y/vps-browser.git
cd vps-browser
sudo ./setup.sh
```

The script installs Docker if needed, asks for your domain, email, username, password, start page and timezone, then starts everything. Open `https://your-domain` and log in. The first visit can take about 30 seconds while the certificate is issued.

**Unattended install:** pass the answers as environment variables:

```bash
sudo DOMAIN=browse.example.com ACME_EMAIL=you@example.com \
     AUTH_USER=admin AUTH_PASSWORD='a-long-random-password' \
     START_PAGE=https://www.google.com TZ=UTC ./setup.sh
```

**Manual install:** `cp .env.example .env`, fill it in (see the comments inside), then `docker compose up -d`.

### Deploy with Coolify

Coolify already runs its own proxy on ports 80/443 and handles HTTPS, so use the Coolify compose file instead of `setup.sh`:

1. Create a **Docker Compose** application from this repo and set **Docker Compose Location** to `/docker-compose.coolify.yml`.
2. Give the **`caddy`** service your domain (e.g. `https://browse.example.com`). Leave the `browser` service **without** a domain, otherwise the login is skipped.
3. Add environment variables `AUTH_USER` and `AUTH_HASH` (generate the hash with `docker run --rm caddy:2 caddy hash-password --plaintext 'PASSWORD'`). Tick **Is Literal?** on `AUTH_HASH`, because the hash contains `$` signs. Optionally set `START_PAGE` and `TZ`.
4. Deploy.

## Common commands

Run these from the `vps-browser` folder:

```bash
docker compose ps                            # status
docker compose logs -f                       # see what's happening
docker compose restart browser               # browser frozen
docker compose pull && docker compose up -d  # update to the latest images
git pull && docker compose up -d             # update this repo
docker compose down                          # stop
```

**Change password:** run `sudo ./setup.sh` again and choose to reconfigure, or do it by hand: generate a hash with `docker run --rm caddy:2 caddy hash-password --plaintext 'NEW_PASSWORD'`, paste it into `AUTH_HASH='...'` in `.env` (keep the single quotes), then run `docker compose up -d caddy`.

**Back up / restore the browser profile:**

```bash
docker run --rm -v vps-browser-data:/data -v "$PWD":/backup alpine tar czf /backup/browser-profile.tgz -C /data .
docker compose stop browser && docker run --rm -v vps-browser-data:/data -v "$PWD":/backup alpine tar xzf /backup/browser-profile.tgz -C /data && docker compose start browser
```

**Start fresh (wipe the profile):** `docker compose down && docker volume rm vps-browser-data && docker compose up -d`.

## Troubleshooting

| Problem | Fix |
|---|---|
| Certificate error / site won't load | Check that the A record points at the VPS (`getent hosts your-domain`) and that ports 80/443 are open. Then look at `docker compose logs caddy`. |
| Login prompt keeps coming back | Wrong password, or `AUTH_HASH` in `.env` isn't wrapped in single quotes. |
| Black screen or "aw snap" | `docker compose restart browser`. If it keeps happening, the VPS probably needs more RAM. |
| Video is choppy | Open the sidebar in the web UI and lower the resolution/quality. See the WebRTC option in [HANDOVER.md](HANDOVER.md#8-possible-next-steps-backlog-in-rough-priority-order). |

## Security

- Use a **strong, unique password**. Anyone who logs in gets full control of a browser that holds all your logged-in sessions.
- The Chromium container is never exposed directly. Only Caddy publishes ports, and it requires the login for every request. Don't add `ports:` to the `browser` service.
- Your profile lives in the Docker volume `vps-browser-data` on the VPS. Keep the server private, and back the profile up if it matters to you (see below).
- Basic auth has no rate limiting. For an internet-facing deployment, consider adding fail2ban or putting Authelia in front (2FA).
- Using proxies to reach blocked content may be restricted where you live. Check your local laws.

## How it works

See [HANDOVER.md](HANDOVER.md) for the full architecture, design decisions, local testing steps and roadmap.

## License

[MIT](LICENSE)
