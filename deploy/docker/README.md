# Docker Deploy

This path builds the image locally from a source checkout and runs Pappice with
Docker Compose. Runtime state is kept in bind-mounted directories under
`deploy/docker/`.

The container listens with plain HTTP on `127.0.0.1:8388`. Put nginx, Caddy, or
Traefik in front of it for public HTTPS.

## Setup

Create local state directories:

```sh
mkdir -p deploy/docker/data deploy/docker/backups
```

The container runs as UID/GID `10001`. Make the bind mounts writable by that
user:

```sh
sudo chown -R 10001:10001 deploy/docker/data deploy/docker/backups
sudo chmod 0750 deploy/docker/data deploy/docker/backups
```

Copy and edit the environment file:

```sh
cp deploy/docker/pappice.env.example deploy/docker/pappice.env
```

At minimum, set `PAPPICE_PUBLIC_URL` and SMTP values if email notifications are
enabled. Keep `PAPPICE_TRUST_PROXY_HEADERS=true` only when Pappice is behind a
private reverse proxy that sets `X-Forwarded-Proto: https`.

Start Pappice:

```sh
docker compose -f deploy/docker/compose.yaml up --build -d
```

Direct `http://127.0.0.1:8388` access is useful for health checks and token API
testing. Browser sessions need an HTTPS reverse proxy that sends
`X-Forwarded-Proto: https`. The existing nginx template in
`deploy/nginx/pappice.conf.example` already proxies to `http://127.0.0.1:8388`
and sends the expected forwarded headers.

## Operations

Check status and logs:

```sh
docker compose -f deploy/docker/compose.yaml ps
docker compose -f deploy/docker/compose.yaml logs -f pappice
```

Run database checks:

```sh
docker compose -f deploy/docker/compose.yaml run --rm pappice db status
docker compose -f deploy/docker/compose.yaml run --rm pappice db migrate --dry-run
```

Create an app-level backup:

```sh
docker compose -f deploy/docker/compose.yaml run --rm pappice backup
```

Back up `deploy/docker/data` and `deploy/docker/backups` together if you need a
full host-level snapshot. They contain the SQLite database, uploads, and backup
snapshots.

## Upgrade

The Docker image contains only the Pappice binary and embedded web assets. The
SQLite database, uploads, and backup snapshots stay on the host in the bind
mounts under `deploy/docker/`.

After updating the source checkout to a newer version, create an app-level
backup first:

```sh
docker compose -f deploy/docker/compose.yaml run --rm pappice backup
```

For a full bind-mount snapshot, stop the container and archive both mounted
directories:

```sh
docker compose -f deploy/docker/compose.yaml stop pappice
tar -C deploy/docker -czf pappice-docker-backup-$(date -u +%Y%m%dT%H%M%SZ).tar.gz data backups
```

Rebuild the image:

```sh
docker compose -f deploy/docker/compose.yaml build --pull
```

Run migrations explicitly, as with the non-Docker deploy:

```sh
docker compose -f deploy/docker/compose.yaml run --rm pappice db migrate --dry-run
docker compose -f deploy/docker/compose.yaml run --rm pappice db migrate
```

Start the upgraded container:

```sh
docker compose -f deploy/docker/compose.yaml up -d
```

If the dry run fails, do not run the real migration. Keep the old container
stopped, inspect the error, and restore the `data` and `backups` directories
from the snapshot if needed.
