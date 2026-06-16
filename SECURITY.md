# Security Policy

Pappice is alpha software and has not been externally security audited.

## Supported Versions

Only the latest released version receives security fixes. There are no LTS
branches while the project is in alpha.

## Reporting A Vulnerability

Please do not report security issues in public GitHub issues.

Use GitHub private vulnerability reporting when available. If it is not
available, contact the maintainer through the GitHub profile and ask for a
private reporting channel.

Include:

- affected version or commit;
- deployment shape, for example binary/systemd/nginx or Docker;
- clear reproduction steps;
- expected impact;
- relevant logs or HTTP requests with secrets removed.

## Scope

Security reports are useful for issues in Pappice itself, including
authentication, authorization, session handling, uploads, webhooks, email
notifications, and data exposure.

Operational issues such as weak SMTP credentials, exposed private listeners,
incorrect reverse proxy headers, missing backups, or permissive filesystem
permissions should be fixed by the instance operator. Reports are still welcome
when documentation or defaults make a secure setup unclear.

## Disclosure

The goal is to confirm, fix, and release security fixes before public details are
shared. Public disclosure should wait until a fixed release is available.
