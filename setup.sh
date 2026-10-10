#!/usr/bin/env bash
# One-shot installer for an Ubuntu/Debian VPS. Run as root from the repo folder:
#   sudo ./setup.sh
#
# Safe to re-run. Every question can be answered ahead of time with an
# environment variable, so it can also run unattended, e.g.:
#   sudo DOMAIN=browse.example.com ACME_EMAIL=me@example.com \
#        AUTH_USER=admin AUTH_PASSWORD='long-secret' ./setup.sh
set -euo pipefail

cd "$(dirname "$0")"

if [[ $EUID -ne 0 ]]; then
  echo "Please run as root: sudo ./setup.sh" >&2
  exit 1
fi

# 1. Docker
if ! command -v docker >/dev/null 2>&1; then
  echo "==> Installing Docker..."
  curl -fsSL https://get.docker.com | sh
fi
if ! docker compose version >/dev/null 2>&1; then
  echo "!! 'docker compose' is missing. Install the Docker Compose plugin and re-run." >&2
  exit 1
fi

mem_kb=$(awk '/MemTotal/{print $2}' /proc/meminfo 2>/dev/null || echo 0)
if (( mem_kb > 0 && mem_kb < 1900000 )); then
  echo "!! Warning: this server has less than 2 GB RAM. Chromium may crash or be slow."
fi

# 2. Settings
configure=yes
if [[ -f .env ]]; then
  read -rp "Existing .env found. Reconfigure it? [y/N]: " ans
  [[ "$ans" =~ ^[Yy] ]] || configure=no
fi

if [[ $configure == yes ]]; then
  DOMAIN=${DOMAIN:-}
  while [[ -z "$DOMAIN" ]]; do
    read -rp "Domain (e.g. browse.example.com): " DOMAIN
  done
  ACME_EMAIL=${ACME_EMAIL:-}
  while [[ -z "$ACME_EMAIL" ]]; do
    read -rp "Email for HTTPS certificate: " ACME_EMAIL
  done
  if [[ -z "${AUTH_USER:-}" ]]; then
    read -rp "Login username [admin]: " AUTH_USER
    AUTH_USER=${AUTH_USER:-admin}
  fi
  PASS=${AUTH_PASSWORD:-}
  while [[ -z "$PASS" ]]; do
    read -rsp "Login password: " PASS; echo
    read -rsp "Repeat password: " PASS2; echo
    if [[ -z "$PASS" || "$PASS" != "$PASS2" ]]; then
      echo "Passwords empty or don't match, try again."
      PASS=""
    fi
  done
  if (( ${#PASS} < 12 )); then
    echo "!! Warning: password is shorter than 12 characters. Anyone who guesses it controls your browser."
  fi
  if [[ -z "${START_PAGE:-}" ]]; then
    read -rp "Start page [https://www.google.com]: " START_PAGE
    START_PAGE=${START_PAGE:-https://www.google.com}
  fi
  if [[ -z "${TZ:-}" ]]; then
    detected_tz=$(timedatectl show -p Timezone --value 2>/dev/null || cat /etc/timezone 2>/dev/null || echo UTC)
    read -rp "Browser timezone [${detected_tz}]: " TZ
    TZ=${TZ:-$detected_tz}
  fi

  echo "==> Hashing password..."
  # Password goes in on stdin so it never shows up in the process list.
  AUTH_HASH=$(printf '%s\n' "$PASS" | docker run --rm -i caddy:2 caddy hash-password)
  unset PASS PASS2 AUTH_PASSWORD

  cat > .env <<EOF
DOMAIN=$DOMAIN
ACME_EMAIL=$ACME_EMAIL
AUTH_USER=$AUTH_USER
AUTH_HASH='$AUTH_HASH'
START_PAGE=$START_PAGE
TZ=$TZ
EOF
  chmod 600 .env
else
  DOMAIN=$(sed -n 's/^DOMAIN=//p' .env)
  AUTH_USER=$(sed -n 's/^AUTH_USER=//p' .env)
fi

# 3. DNS sanity check
SERVER_IP=$(curl -fsS4 --max-time 10 https://api.ipify.org || true)
DNS_IP=$(getent ahostsv4 "$DOMAIN" | awk 'NR==1{print $1}' || true)
if [[ -n "$SERVER_IP" && "$DNS_IP" != "$SERVER_IP" ]]; then
  echo "!! Warning: $DOMAIN resolves to '${DNS_IP:-nothing}', but this server is $SERVER_IP."
  echo "!! Add an A record for $DOMAIN -> $SERVER_IP, or HTTPS will fail."
fi

# 4. Firewall
if command -v ufw >/dev/null 2>&1 && ufw status | grep -q "Status: active"; then
  ufw allow 80/tcp
  ufw allow 443/tcp
  ufw allow 443/udp
fi

# 5. Start
mkdir -p data/caddy
docker compose pull
docker compose up -d

echo
echo "Done. Open https://$DOMAIN and log in as '$AUTH_USER'."
echo "(The first visit can take ~30s while the HTTPS certificate is issued.)"
