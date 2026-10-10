#!/usr/bin/env bash
# vps-browser installer. On a fresh Ubuntu/Debian server, run:
#
#   curl -fsSL https://raw.githubusercontent.com/raff4y/vps-browser/main/install.sh | sudo bash
#
# It installs Docker if needed, downloads vps-browser to /opt/vps-browser,
# starts it, and prints a private link to finish setup in your web browser.
# Safe to run again: it updates an existing install.
set -euo pipefail

REPO_URL=${REPO_URL:-https://github.com/raff4y/vps-browser.git}
BRANCH=${BRANCH:-main}
INSTALL_DIR=${INSTALL_DIR:-/opt/vps-browser}

bold=$'\e[1m'; green=$'\e[32m'; yellow=$'\e[33m'; red=$'\e[31m'; reset=$'\e[0m'
step() { echo; echo "${bold}==> $*${reset}"; }
warn() { echo "${yellow}!! $*${reset}"; }
die()  { echo "${red}ERROR: $*${reset}" >&2; exit 1; }

[[ $EUID -eq 0 ]] || die "Please run as root, e.g.: curl -fsSL <url> | sudo bash"
[[ "$(uname -s)" == "Linux" ]] || die "vps-browser runs on a Linux server."

# 1. Basic tools
need=()
for cmd in git curl; do command -v "$cmd" >/dev/null 2>&1 || need+=("$cmd"); done
if (( ${#need[@]} )); then
  step "Installing ${need[*]}"
  if command -v apt-get >/dev/null 2>&1; then
    apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq "${need[@]}" ca-certificates
  elif command -v dnf >/dev/null 2>&1; then
    dnf install -y -q "${need[@]}"
  elif command -v yum >/dev/null 2>&1; then
    yum install -y -q "${need[@]}"
  else
    die "Please install ${need[*]} and run this again."
  fi
fi

# 2. Docker
if ! command -v docker >/dev/null 2>&1; then
  step "Installing Docker"
  curl -fsSL https://get.docker.com | sh
fi
systemctl enable --now docker >/dev/null 2>&1 || true
docker compose version >/dev/null 2>&1 || die "The Docker Compose plugin is missing. Install it and run this again."

# 3. Memory: Chromium needs room. Add swap on small servers that have none.
mem_kb=$(awk '/MemTotal/{print $2}' /proc/meminfo)
swap_kb=$(awk '/SwapTotal/{print $2}' /proc/meminfo)
if (( mem_kb < 3500000 && swap_kb == 0 )) && [[ ! -e /swapfile ]]; then
  step "Adding 2 GB of swap (this server has $((mem_kb / 1024)) MB RAM and no swap)"
  if { fallocate -l 2G /swapfile 2>/dev/null || dd if=/dev/zero of=/swapfile bs=1M count=2048 status=none; } \
     && chmod 600 /swapfile && mkswap /swapfile >/dev/null && swapon /swapfile; then
    echo '/swapfile none swap sw 0 0' >> /etc/fstab
  else
    rm -f /swapfile
    warn "Couldn't enable swap; continuing without it."
  fi
fi
(( mem_kb >= 1900000 )) || warn "Less than 2 GB RAM. The browser may be slow or crash on heavy sites."

# 4. Ports 80 and 443 must be free (Let's Encrypt needs them).
if command -v ss >/dev/null 2>&1; then
  busy=$(ss -ltnpH 2>/dev/null | awk '$4 ~ /:(80|443)$/' | grep -v -e docker-proxy -e '"docker' || true)
  if [[ -n "$busy" ]]; then
    echo "$busy"
    die "Ports 80/443 are already used by another program (above). Stop it (e.g. 'systemctl disable --now nginx apache2') and run this again."
  fi
fi

# 5. Download or update the code
if [[ -d "$INSTALL_DIR/.git" ]]; then
  step "Updating $INSTALL_DIR"
  git -C "$INSTALL_DIR" pull --ff-only
else
  step "Downloading vps-browser to $INSTALL_DIR"
  git clone --depth 1 --branch "$BRANCH" "$REPO_URL" "$INSTALL_DIR"
fi
cd "$INSTALL_DIR"

if [[ ! -f .env ]]; then
  tz=$(timedatectl show -p Timezone --value 2>/dev/null || true)
  printf '# Optional settings, see .env.example\nTZ=%s\n' "${tz:-UTC}" > .env
fi

# 6. Firewall
if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -q "Status: active"; then
  step "Opening ports 80 and 443 in ufw"
  ufw allow 80/tcp >/dev/null; ufw allow 443/tcp >/dev/null; ufw allow 443/udp >/dev/null
elif command -v firewall-cmd >/dev/null 2>&1 && firewall-cmd --state >/dev/null 2>&1; then
  step "Opening ports 80 and 443 in firewalld"
  firewall-cmd --permanent --add-service=http --add-service=https >/dev/null
  firewall-cmd --permanent --add-port=443/udp >/dev/null
  firewall-cmd --reload >/dev/null
fi

# 7. Management command
chmod +x vps-browser install.sh
ln -sf "$INSTALL_DIR/vps-browser" /usr/local/bin/vps-browser

# 8. Build and start
step "Building and starting (the first time takes a few minutes)"
docker compose build --pull
docker compose up -d --remove-orphans

step "Waiting for the control panel"
url=""
for _ in $(seq 1 30); do
  if url=$(docker compose exec -T panel /panel setup-url 2>/dev/null) && [[ -n "$url" ]]; then break; fi
  sleep 2
done

echo
echo "${green}${bold}vps-browser is running.${reset}"
echo
if [[ "$url" == http* ]]; then
  echo "  ${bold}Finish setup in your web browser:${reset}"
  echo
  echo "    ${bold}${url}${reset}"
  echo
  echo "  This link is private: anyone who has it can set up this server."
  echo "  You'll pick your domain, username and password there."
else
  echo "$url"
fi
echo
echo "  Manage it any time with: ${bold}vps-browser help${reset}"
echo "  Cloud firewall? Make sure ports 80 and 443 are open in your provider's dashboard."
echo
