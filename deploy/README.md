# Production Deploy

This is the reference deployment for Debian or Ubuntu using nginx for public
HTTPS and systemd for the Pappice process. Pappice listens on local HTTP at
`127.0.0.1:8388`; nginx terminates public HTTPS and forwards
`X-Forwarded-Proto: https`. `PAPPICE_TRUST_PROXY_HEADERS=true` is enabled in the
production environment template, so do not expose the Pappice listener directly
to the public internet.

Before starting, point the chosen hostname at the server and obtain a TLS
certificate. The nginx template expects the certificate and key at
`/etc/letsencrypt/live/<hostname>/fullchain.pem` and `privkey.pem`; edit the
template if your ACME client stores them elsewhere.

## Files

- `deploy/env/pappice.env.example`: production environment template.
- `deploy/nginx/pappice.conf.example`: nginx site template.
- `deploy/systemd/pappice.service`: application service.
- `deploy/systemd/pappice-backup.service`: one-shot `pappice backup` service.
- `deploy/systemd/pappice-backup.timer`: daily backup timer.

## First Install

Choose the hostname and resolve the latest release tag:

```sh
DOMAIN=support.example.com
LATEST_URL="$(curl -fsSLI -o /dev/null -w '%{url_effective}' https://github.com/lallero-dev/pappice/releases/latest)"
VERSION="${LATEST_URL##*/}"
case "$(uname -m)" in
  x86_64|amd64) PAPPICE_ARCH=amd64 ;;
  aarch64|arm64) PAPPICE_ARCH=arm64 ;;
  *) echo "Unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac
ARCHIVE=pappice-${VERSION}-linux-${PAPPICE_ARCH}.tar.gz
BASE_URL=https://github.com/lallero-dev/pappice/releases/download/${VERSION}
```

Install OS packages:

```sh
sudo apt-get update
sudo apt-get install -y ca-certificates curl nginx
```

Download and unpack the release archive:

```sh
curl -fLO "${BASE_URL}/${ARCHIVE}"
curl -fLO "${BASE_URL}/${ARCHIVE}.sha256"
sha256sum -c "${ARCHIVE}.sha256"
rm -rf pappice-release
mkdir pappice-release
tar -xzf "$ARCHIVE" -C pappice-release --strip-components=1
cd pappice-release
```

Create the service account and directories:

```sh
sudo useradd --system --home /var/lib/pappice --shell /usr/sbin/nologin pappice
sudo install -d -o pappice -g pappice -m 0750 /var/lib/pappice /var/lib/pappice/uploads /var/backups/pappice
sudo install -d -o root -g pappice -m 0750 /etc/pappice
```

Install the binary:

```sh
sudo install -o root -g root -m 0755 pappice /usr/local/bin/pappice
```

Install deploy assets:

```sh
sudo install -o root -g pappice -m 0640 deploy/env/pappice.env.example /etc/pappice/pappice.env
sudo sed -i "s/support.example.com/$DOMAIN/g" /etc/pappice/pappice.env
sudo install -o root -g root -m 0644 deploy/systemd/pappice.service /etc/systemd/system/pappice.service
sudo install -o root -g root -m 0644 deploy/systemd/pappice-backup.service /etc/systemd/system/pappice-backup.service
sudo install -o root -g root -m 0644 deploy/systemd/pappice-backup.timer /etc/systemd/system/pappice-backup.timer
```

Edit `/etc/pappice/pappice.env` before starting. Set branding and SMTP values
now if you have them; otherwise leave `PAPPICE_EMAIL_NOTIFICATIONS=false` and
enable email later from a known-good SMTP configuration. Keep
`PAPPICE_TRUST_PROXY_HEADERS=true` only when nginx is the only public entry
point. Keep `PAPPICE_ALLOW_INSECURE_WEBHOOKS=false` and
`PAPPICE_ALLOW_PRIVATE_WEBHOOKS=false` in production.

Install the nginx site after the certificate files described above exist:

```sh
sudo install -o root -g root -m 0644 deploy/nginx/pappice.conf.example /etc/nginx/sites-available/pappice.conf
sudo sed -i "s/support.example.com/$DOMAIN/g" /etc/nginx/sites-available/pappice.conf
sudo ln -sf /etc/nginx/sites-available/pappice.conf /etc/nginx/sites-enabled/pappice.conf
sudo nginx -t
sudo systemctl reload nginx
```

Start Pappice and backups:

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now pappice.service
sudo systemctl enable --now pappice-backup.timer
```

Open `https://$DOMAIN` and create the first admin account.

## Configuration

`deploy/env/pappice.env.example` is the complete production template. Every
runtime setting is also available as a command-line flag; process environment
variables override values loaded from an environment file. Run `pappice doctor`
after changing configuration.

Branding is configured with the `PAPPICE_BRAND_*` values. Attachments are stored
under `PAPPICE_UPLOAD_DIR`; SQLite stores their metadata, so the database and
upload directory must be backed up and restored together.

Pappice sends no-reply email and never processes inbound replies. SMTP delivery
is optional and uses the durable SQLite outbox. Email and webhook updates wait
for `PAPPICE_NOTIFICATION_DELAY`, and pending updates for one ticket are
coalesced.

API automation uses bearer tokens. Webhook deliveries are signed with
`X-Pappice-Signature`; secrets are shown once after creation or rotation.
Webhook URLs must be public HTTPS unless the development-only escape hatches are
explicitly enabled.

## Checks

```sh
sudo -u pappice bash -lc 'set -a; source /etc/pappice/pappice.env; set +a; /usr/local/bin/pappice doctor'
systemctl status pappice.service
journalctl -u pappice.service -f
```

From the admin UI:

- Send a test email if SMTP is enabled.
- Create a product.
- Create a test customer.
- Create and reply to a test ticket.
- Run one manual backup:

```sh
sudo systemctl start pappice-backup.service
sudo journalctl -u pappice-backup.service -n 50
```

## Upgrade

```sh
LATEST_URL="$(curl -fsSLI -o /dev/null -w '%{url_effective}' https://github.com/lallero-dev/pappice/releases/latest)"
VERSION="${LATEST_URL##*/}"
case "$(uname -m)" in
  x86_64|amd64) PAPPICE_ARCH=amd64 ;;
  aarch64|arm64) PAPPICE_ARCH=arm64 ;;
  *) echo "Unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac
ARCHIVE=pappice-${VERSION}-linux-${PAPPICE_ARCH}.tar.gz
BASE_URL=https://github.com/lallero-dev/pappice/releases/download/${VERSION}
curl -fLO "${BASE_URL}/${ARCHIVE}"
curl -fLO "${BASE_URL}/${ARCHIVE}.sha256"
sha256sum -c "${ARCHIVE}.sha256"
rm -rf pappice-release
mkdir pappice-release
tar -xzf "$ARCHIVE" -C pappice-release --strip-components=1
cd pappice-release
sudo systemctl start pappice-backup.service
sudo systemctl stop pappice.service
sudo install -o root -g root -m 0755 pappice /usr/local/bin/pappice
sudo -u pappice bash -lc 'set -a; source /etc/pappice/pappice.env; set +a; /usr/local/bin/pappice db status'
sudo -u pappice bash -lc 'set -a; source /etc/pappice/pappice.env; set +a; /usr/local/bin/pappice db migrate --dry-run'
sudo -u pappice bash -lc 'set -a; source /etc/pappice/pappice.env; set +a; /usr/local/bin/pappice db migrate'
sudo systemctl start pappice.service
```

## Build From Source

Maintainers can create a release archive from a source checkout with:

```sh
scripts/build-release.sh
```

Set `GOOS` and `GOARCH` to build another target, for example
`GOOS=linux GOARCH=arm64 scripts/build-release.sh`.

## Restore

Stop Pappice before restoring:

```sh
sudo systemctl stop pappice.service
sudo -u pappice bash -lc 'set -a; source /etc/pappice/pappice.env; set +a; /usr/local/bin/pappice restore -yes latest'
sudo systemctl start pappice.service
```
