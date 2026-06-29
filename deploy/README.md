# Production Deploy

This setup uses nginx for public HTTPS and systemd for the Pappice process.
Pappice listens on local HTTP at `127.0.0.1:8388`; nginx terminates public HTTPS
and forwards `X-Forwarded-Proto: https`. `PAPPICE_TRUST_PROXY_HEADERS=true` is
enabled in the production environment template, so do not expose the Pappice
listener directly to the public internet.

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
ARCHIVE=pappice-${VERSION}-linux-amd64.tar.gz
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

Install the nginx site after issuing the public certificate with your preferred
ACME flow:

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
ARCHIVE=pappice-${VERSION}-linux-amd64.tar.gz
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

Maintainers can create the same release archive from a source checkout with:

```sh
scripts/build-release.sh
```

## Restore

Stop Pappice before restoring:

```sh
sudo systemctl stop pappice.service
sudo -u pappice bash -lc 'set -a; source /etc/pappice/pappice.env; set +a; /usr/local/bin/pappice restore -yes latest'
sudo systemctl start pappice.service
```
