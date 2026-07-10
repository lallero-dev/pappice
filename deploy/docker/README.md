# Docker Deploy

This path builds the image locally from a source checkout and runs Pappice with
Docker Compose. Runtime state is kept in Docker named volumes.

The container listens with plain HTTP on `127.0.0.1:8388`. Put nginx, Caddy, or
Traefik in front of it for public HTTPS.

The Compose service runs as UID/GID `10001`, drops Linux capabilities, uses
`no-new-privileges`, keeps the root filesystem read-only, and mounts only
`/data`, `/backups`, and a small `/tmp` tmpfs as writable paths.

## Setup

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

Compose creates the `pappice-data` and `pappice-backups` volumes on first
start. They contain the SQLite database, uploads, and app backup snapshots.

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

Use the app-level backup for normal restores. If you need a full host-level
snapshot, include both Docker volumes.

## Upgrade

The Docker image contains only the Pappice binary and embedded web assets. The
SQLite database, uploads, and backup snapshots stay in the `pappice-data` and
`pappice-backups` Docker volumes.

After updating the source checkout to a newer version, create an app-level
backup first:

```sh
docker compose -f deploy/docker/compose.yaml run --rm pappice backup
```

If you also want a volume-level snapshot, stop the container and copy the
`pappice-data` and `pappice-backups` volumes with your usual Docker host backup
tool.

Rebuild the image:

```sh
docker compose -f deploy/docker/compose.yaml build --pull
```

Stop the running app, then run migrations explicitly with the new image:

```sh
docker compose -f deploy/docker/compose.yaml stop pappice
docker compose -f deploy/docker/compose.yaml run --rm pappice db migrate --dry-run
docker compose -f deploy/docker/compose.yaml run --rm pappice db migrate
```

Start the upgraded container:

```sh
docker compose -f deploy/docker/compose.yaml up -d
```

If the dry run fails, do not run the real migration. Keep the old container
stopped, inspect the error, and restore the volumes from your snapshot if
needed.

## Restore

Stop Pappice before restoring an app-level backup:

```sh
docker compose -f deploy/docker/compose.yaml stop pappice
docker compose -f deploy/docker/compose.yaml run --rm pappice restore -yes latest
docker compose -f deploy/docker/compose.yaml up -d
```
