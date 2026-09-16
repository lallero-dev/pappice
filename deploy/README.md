# Install Pappice

Choose **[Docker Compose](#docker-compose) (recommended)** or
[binary + systemd](#binary-and-systemd). Both require HTTPS for browser login.
Published binaries target **linux/amd64** and **linux/arm64**, with checksums.

[HTTPS](#https) · [Operations](#operations) · [Upgrade](#upgrade) · [Restore](#restore) · [Configuration](../docs/configuration.md)

## Docker Compose

Requires Linux, Docker Engine, Compose, Git, and curl. Verify `docker compose version`
and `docker info` work. Docker builds locally; no host Go or Node.js needed.
Clone release source; the binary archive cannot build the image:

```sh
PAPPICE_LATEST_URL="$(curl -fsSLI -o /dev/null -w '%{url_effective}' https://github.com/lallero-dev/pappice/releases/latest)"
PAPPICE_VERSION="${PAPPICE_LATEST_URL##*/}"
git clone --depth 1 --branch "$PAPPICE_VERSION" https://github.com/lallero-dev/pappice.git pappice
cd pappice
cp deploy/docker/pappice.env.example deploy/docker/pappice.env
chmod 600 deploy/docker/pappice.env
```

Set `PAPPICE_PUBLIC_URL` in the env file. Complete [HTTPS](#https), then start
from the repository root:

```sh
docker compose -f deploy/docker/compose.yaml up --build -d
```

Open your public HTTPS URL and create the first admin account.
Compose runs as UID/GID `10001` and writes only to `/data`, `/backups`, and `/tmp`.
Rename `pappice-data` and `pappice-backups` volumes for additional instances.

## Binary and systemd

Requires Debian/Ubuntu and sudo; no Go, Node.js, or Docker needed.

### Download a release

```sh
sudo apt-get update
sudo apt-get install -y ca-certificates curl
```

Download the latest release, or set `PAPPICE_VERSION` to a specific tag:

```sh
PAPPICE_LATEST_URL="$(curl -fsSLI -o /dev/null -w '%{url_effective}' https://github.com/lallero-dev/pappice/releases/latest)"
PAPPICE_VERSION="${PAPPICE_LATEST_URL##*/}"
case "$(uname -m)" in
  x86_64|amd64) PAPPICE_ARCH=amd64 ;;
  aarch64|arm64) PAPPICE_ARCH=arm64 ;;
  *) echo "No release binary for this architecture" >&2; exit 1 ;;
esac
PAPPICE_ARCHIVE=pappice-${PAPPICE_VERSION}-linux-${PAPPICE_ARCH}.tar.gz
PAPPICE_BASE_URL=https://github.com/lallero-dev/pappice/releases/download/${PAPPICE_VERSION}
PAPPICE_RELEASE_DIR="$(mktemp -d)"
cd "$PAPPICE_RELEASE_DIR"
curl -fLO "${PAPPICE_BASE_URL}/${PAPPICE_ARCHIVE}" &&
curl -fLO "${PAPPICE_BASE_URL}/${PAPPICE_ARCHIVE}.sha256" &&
sha256sum -c "${PAPPICE_ARCHIVE}.sha256" &&
tar -xzf "$PAPPICE_ARCHIVE" --strip-components=1
```

Continue from this directory after checksum `OK`.

### Install the service

Set your hostname and install:

```sh
PAPPICE_DOMAIN=support.example.com
sudo useradd --system --home /var/lib/pappice --shell /usr/sbin/nologin pappice
sudo install -d -o pappice -g pappice -m 0750 /var/lib/pappice /var/lib/pappice/uploads /var/backups/pappice
sudo install -d -o root -g pappice -m 0750 /etc/pappice
sudo install -o root -g root -m 0755 pappice /usr/local/bin/pappice
sudo install -o root -g pappice -m 0640 deploy/systemd/pappice.env.example /etc/pappice/pappice.env
sudo sed -i "s/support.example.com/$PAPPICE_DOMAIN/g" /etc/pappice/pappice.env
sudo install -o root -g root -m 0644 deploy/systemd/pappice.service /etc/systemd/system/pappice.service
sudo install -o root -g root -m 0644 deploy/systemd/pappice-backup.service /etc/systemd/system/pappice-backup.service
sudo install -o root -g root -m 0644 deploy/systemd/pappice-backup.timer /etc/systemd/system/pappice-backup.timer
```

Complete [HTTPS](#https), then start Pappice and daily backups:

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now pappice.service
sudo systemctl enable --now pappice-backup.timer
```

Open `https://$PAPPICE_DOMAIN` and create the first admin account.

## HTTPS

Point DNS at your server, allow ports 80/443, and match `PAPPICE_PUBLIC_URL`.
Both methods expose host `127.0.0.1:8388`.

### nginx on Debian or Ubuntu

From the extracted release (binary) or repository root (Docker):

```sh
PAPPICE_DOMAIN=support.example.com
sudo apt-get update
sudo apt-get install -y nginx certbot python3-certbot-nginx
sudo systemctl enable --now nginx
sudo certbot certonly --nginx -d "$PAPPICE_DOMAIN" --deploy-hook "systemctl reload nginx"
```

Follow Certbot's prompts, then install the site:

```sh
sudo install -o root -g root -m 0644 deploy/nginx/pappice.conf.example /etc/nginx/sites-available/pappice.conf
sudo sed -i "s/support.example.com/$PAPPICE_DOMAIN/g" /etc/nginx/sites-available/pappice.conf
sudo ln -sf /etc/nginx/sites-available/pappice.conf /etc/nginx/sites-enabled/pappice.conf
sudo nginx -t && sudo systemctl reload nginx
```

For existing certificates, change the template's paths.
Ensure Certbot renewal is scheduled; test with `sudo certbot renew --dry-run`.
The [deploy hook](https://eff-certbot.readthedocs.io/en/stable/using.html#renewing-certificates) reloads nginx after renewal.
Optional HTTP/2 requires [nginx 1.25.1+](https://nginx.org/en/docs/http/ngx_http_v2_module.html#http2).
Return to your method's startup commands; until Pappice starts, expect 502.

### Existing reverse proxy

Configure your proxy to:

- Terminate HTTPS and forward to `http://127.0.0.1:8388`.
- Set `Host`, `X-Forwarded-Host`, and `X-Forwarded-Proto: https`.
- Replace client-supplied `X-Real-IP` with the actual client address.
- Allow attachment requests up to 64 MiB, as in the nginx template.

For a proxy in Docker, connect it to Pappice's private Docker network and use
the service address. Keep Pappice's port private when trusting proxy headers.

## Operations

Back up SQLite and uploads together; copy backups off-host.
After env changes, run `doctor`, then restart/recreate as shown below.

### Docker operations

From the repository root:

```sh
docker compose -f deploy/docker/compose.yaml ps
docker compose -f deploy/docker/compose.yaml logs -f pappice
docker compose -f deploy/docker/compose.yaml run --rm pappice doctor
docker compose -f deploy/docker/compose.yaml exec pappice /pappice healthcheck
docker compose -f deploy/docker/compose.yaml run --rm pappice db status
docker compose -f deploy/docker/compose.yaml run --rm pappice backup
```

Schedule the backup command; Compose does not schedule it. Backups are in
`pappice-backups`; host snapshots must include both volumes.
Apply env changes with `docker compose -f deploy/docker/compose.yaml up -d`.

### systemd operations

Load the service's env file and working directory for manual commands:

```sh
sudo -u pappice bash -ec 'set -a; source /etc/pappice/pappice.env; set +a; cd /var/lib/pappice; /usr/local/bin/pappice doctor'
sudo -u pappice bash -ec 'set -a; source /etc/pappice/pappice.env; set +a; cd /var/lib/pappice; /usr/local/bin/pappice healthcheck'
systemctl status pappice.service
journalctl -u pappice.service -f
sudo systemctl start pappice-backup.service
sudo journalctl -u pappice-backup.service -n 50
```

The timer backs up to `/var/backups/pappice` around 03:15 daily.
Apply env changes by restarting `pappice.service`.

## Upgrade

Read release notes, back up with the current version, and keep your env file and data.
If migration fails, keep Pappice stopped and inspect the error.
The systemd template moved to `deploy/systemd/`; the installed path remains
`/etc/pappice/pappice.env`. Docker volume names are unchanged.

### Docker upgrade

Fetch the latest release and rebuild, or set `PAPPICE_VERSION` to a specific tag:

```sh
PAPPICE_LATEST_URL="$(curl -fsSLI -o /dev/null -w '%{url_effective}' https://github.com/lallero-dev/pappice/releases/latest)"
PAPPICE_VERSION="${PAPPICE_LATEST_URL##*/}"
git fetch --depth 1 origin "refs/tags/$PAPPICE_VERSION:refs/tags/$PAPPICE_VERSION" &&
git switch --detach "$PAPPICE_VERSION" &&
docker compose -f deploy/docker/compose.yaml build --pull
```

Stop, migrate, and restart:

```sh
docker compose -f deploy/docker/compose.yaml stop pappice &&
docker compose -f deploy/docker/compose.yaml run --rm -e TMPDIR=/data pappice db migrate --dry-run &&
docker compose -f deploy/docker/compose.yaml run --rm -e TMPDIR=/data pappice db migrate &&
docker compose -f deploy/docker/compose.yaml up -d
```

`TMPDIR=/data` lets dry-run database copies exceed the 64 MiB `/tmp` limit;
allow enough free disk space.

### systemd upgrade

Repeat [Download a release](#download-a-release). From the newly extracted directory:

```sh
sudo systemctl start pappice-backup.service &&
sudo systemctl stop pappice.service &&
sudo install -o root -g root -m 0755 pappice /usr/local/bin/pappice &&
sudo -u pappice bash -ec 'set -a; source /etc/pappice/pappice.env; set +a; cd /var/lib/pappice; /usr/local/bin/pappice db migrate --dry-run' &&
sudo -u pappice bash -ec 'set -a; source /etc/pappice/pappice.env; set +a; cd /var/lib/pappice; /usr/local/bin/pappice db migrate' &&
sudo systemctl start pappice.service
```

## Restore

Restore prints recovery directories containing the previous database and uploads,
saved beside their destinations (`pappice-data` for Docker). Older backups may
need a compatible version or migration before startup.

### Docker restore

```sh
docker compose -f deploy/docker/compose.yaml stop pappice &&
docker compose -f deploy/docker/compose.yaml run --rm pappice restore -yes latest &&
docker compose -f deploy/docker/compose.yaml up -d
```

### systemd restore

```sh
sudo systemctl stop pappice.service &&
sudo -u pappice bash -ec 'set -a; source /etc/pappice/pappice.env; set +a; cd /var/lib/pappice; /usr/local/bin/pappice restore -yes latest' &&
sudo systemctl start pappice.service
```
