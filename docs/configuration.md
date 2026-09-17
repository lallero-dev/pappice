# Configuration

Use one env file per installation; keep it when upgrading:

| Method | Template | Active file |
|---|---|---|
| Docker Compose | [Docker env](../deploy/docker/pappice.env.example) | `deploy/docker/pappice.env` |
| Binary + systemd | [systemd env](../deploy/systemd/pappice.env.example) | `/etc/pappice/pappice.env` |
| Local development | [full reference](../.env.example) | `.env` in the working directory |

Copy optional settings from the full reference. Omitted settings use app defaults;
`.env.example` is not loaded.
Precedence: **CLI flags > process env > working-directory `.env` > defaults**.
See `pappice serve -h` for flags.

Quote spaces and metacharacters: `PAPPICE_BRAND_SUBTITLE='customer support'`.
Single quotes preserve `$` in shells and [Compose](https://docs.docker.com/reference/compose-file/services/#env_file).
Keep credentials out of Git.

## HTTPS

Set `PAPPICE_PUBLIC_URL` to your HTTPS address. Both templates require a private
[HTTPS proxy](../deploy/README.md#https) for browser login. Direct TLS requires
`PAPPICE_TLS_CERT` and `PAPPICE_TLS_KEY`.

## Email

Email defaults to off. To enable SMTP, add:

```dotenv
PAPPICE_EMAIL_NOTIFICATIONS=true
PAPPICE_SMTP_HOST=smtp.example.com
PAPPICE_SMTP_PORT=587
PAPPICE_SMTP_USER=your-user
PAPPICE_SMTP_PASSWORD='your-password'
PAPPICE_SMTP_FROM=no-reply@support.example.com
PAPPICE_SMTP_TLS_MODE=starttls
```

Host + from also enable email when the flag is false. To disable, set false and
clear the host. Implicit TLS: use `tls` and your provider's port (usually 465).
Test from the admin UI. Notifications are outbound-only, queued with a 30-second default delay.

## Storage and other settings

Docker paths must match volume mounts.
See [`.env.example`](../.env.example) for branding, limits, and retention;
keep development-only webhook overrides off. Apply changes using the
[deployment guide](../deploy/README.md#operations).
