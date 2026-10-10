# Coolify only copies the compose file to the server, not the repo, so a
# ./Caddyfile bind mount ends up as an empty directory. Bake it in instead.
FROM caddy:2
COPY Caddyfile /etc/caddy/Caddyfile
