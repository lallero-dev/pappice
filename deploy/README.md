# Production Deploy

This setup uses nginx for public HTTPS and systemd for the Pappice process.
Pappice also listens with local HTTPS on `127.0.0.1:8388`; nginx proxies to that
HTTPS upstream because browser sessions require the Go app to receive TLS.

## Files

- `deploy/env/pappice.env.example`: production environment template.
- `deploy/nginx/pappice.conf.example`: nginx site template.
- `deploy/systemd/pappice.service`: application service.
- `deploy/systemd/pappice-backup.service`: one-shot backup service.
- `deploy/systemd/pappice-backup.timer`: daily backup timer.

## First Install

Choose the hostname used by the copy/paste commands:

```sh
ARCHIVE=pappice-linux-amd64.tar.gz
BASE_URL=https://github.com/lallero-dev/pappice/releases/latest/download
DOMAIN=support.example.com
```

Install OS packages:

```sh
sudo apt-get update
sudo apt-get install -y ca-certificates curl nginx sqlite3 openssl
```

Download and unpack the release archive:

```sh
curl -LO "${BASE_URL}/${ARCHIVE}"
curl -LO "${BASE_URL}/${ARCHIVE}.sha256"
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
sudo install -d -o root -g root -m 0755 /opt/pappice /opt/pappice/ops
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
sudo install -o root -g root -m 0755 ops/backup.sh /opt/pappice/ops/backup.sh
sudo install -o root -g root -m 0755 ops/restore.sh /opt/pappice/ops/restore.sh
```

Edit `/etc/pappice/pappice.env` and set SMTP credentials before starting.
Keep `PAPPICE_ALLOW_INSECURE_WEBHOOKS=false` and
`PAPPICE_ALLOW_PRIVATE_WEBHOOKS=false` in production.

Create the local upstream certificate used between nginx and Pappice:

```sh
sudo openssl req -x509 -newkey rsa:2048 -nodes \
  -keyout /etc/pappice/upstream-key.pem \
  -out /etc/pappice/upstream.pem \
  -days 3650 \
  -subj /CN=127.0.0.1 \
  -addext subjectAltName=IP:127.0.0.1,DNS:localhost

sudo chown root:pappice /etc/pappice/upstream.pem /etc/pappice/upstream-key.pem
sudo chmod 0640 /etc/pappice/upstream.pem /etc/pappice/upstream-key.pem
```

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

- Send a test email.
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
ARCHIVE=pappice-linux-amd64.tar.gz
BASE_URL=https://github.com/lallero-dev/pappice/releases/latest/download
curl -LO "${BASE_URL}/${ARCHIVE}"
curl -LO "${BASE_URL}/${ARCHIVE}.sha256"
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
sudo -u pappice bash -lc 'cd /opt/pappice && set -a; source /etc/pappice/pappice.env; set +a; /opt/pappice/ops/restore.sh latest'
sudo systemctl start pappice.service
```
