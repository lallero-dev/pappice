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
Test from the admin UI. Notifications are queued with a 30-second default delay.

### Replies through IMAP (optional)

Requesters can reply to notification emails on existing tickets. Replies are public,
reopen closed tickets, and use the same attachment limits and notification rules
as the portal. Email cannot create tickets or add participants from CC.

With SMTP enabled, configure a dedicated mailbox:

```dotenv
PAPPICE_IMAP_ADDR=imap.example.com:993
PAPPICE_IMAP_USER=replies@example.com
PAPPICE_IMAP_PASSWORD='your-app-password'
PAPPICE_IMAP_MAILBOX=INBOX
PAPPICE_EMAIL_REPLY_ADDRESS=replies@example.com
PAPPICE_IMAP_AUTHSERV_ID=mx.example.com
```

IMAP uses verified implicit TLS and password authentication; OAuth-only accounts
are unsupported. The reply address must accept `replies+<token>@example.com`
and deliver it to this mailbox. Its local part must be at most 23 bytes, without
`+`. Pappice adds a ticket/requester token in `Reply-To`; keep these addresses private.

Set `AUTHSERV_ID` to the identifier in your receiving server's
`Authentication-Results` header, not necessarily the IMAP hostname. The mail server
must remove forged headers claiming that identifier and supply a DMARC result.
Pappice requires `dmarc=pass` for the sender's domain, the requester's current email
address, a valid token, and current ticket access. Do not enable this on a mailbox
whose receiving server cannot guarantee those authentication results.

Pappice polls every minute, at most 100 messages per poll. It processes messages
regardless of their read status and never deletes them. Accepted messages are
marked read; rejected messages are flagged/starred with the reason in server logs.
Review these in the mailbox. Temporary failures retry automatically; to retry a
rejected message after fixing its cause, copy it back into the mailbox as a new
message. Use one Pappice installation per mailbox.

Plain text and HTML-only replies are supported; quoted history is removed at
the notification separator, while signatures are preserved.
Limits: 25 MiB per email including encoding, 1 MiB of reply text, plus
the configured attachment limits. A rejected attachment rejects the whole reply.
Automatic messages, bounces, and mailing-list messages are not imported.

Delivery retries are deduplicated by `Message-ID`; without it, identical raw
messages are deduplicated. Clear `PAPPICE_IMAP_ADDR` to disable reception.
Existing notifications retain their original reply instructions.
`pappice doctor` validates settings without connecting to IMAP.

## Storage and other settings

Docker paths must match volume mounts.
See [`.env.example`](../.env.example) for branding, limits, and retention;
keep development-only webhook overrides off. Apply changes using the
[deployment guide](../deploy/README.md#operations).
