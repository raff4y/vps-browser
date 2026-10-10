FROM lscr.io/linuxserver/chromium:latest
# Lets the control panel's "Restart browser" button work without giving the
# panel access to Docker. See restart-watcher for details.
COPY --chmod=755 restart-watcher /custom-services.d/vps-browser-restart
