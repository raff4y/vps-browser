FROM lscr.io/linuxserver/chromium:latest
# Lets the control panel's "Restart" and "Close" buttons work without giving
# the panel access to Docker. See restart-watcher for details.
COPY --chmod=755 restart-watcher /custom-services.d/vps-browser-restart
# Put chromium-gate in front of the image's launcher, so a closed browser stays
# closed. If the image ever renames its launcher, fail the build here rather
# than ship a "Close" button that doesn't free anything.
RUN mv /usr/bin/wrapped-chromium /usr/bin/wrapped-chromium-real
COPY --chmod=755 chromium-gate /usr/bin/wrapped-chromium
