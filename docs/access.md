# Account Types And Product Roles

Each account has one global type: **Admin**, **Staff**, or **Customer**. Product
memberships determine access within individual products.

- Admin accounts manage Pappice and have full access to every product, regardless
  of their product memberships.
- Staff accounts can access products they belong to, using their product role.
- Customer accounts see only their own tickets and public conversations. Assigning
  a Customer account a staff product role does not grant staff privileges.

For Staff accounts, product roles allow:

| Action | Manager | Staff | Internal contributor | Viewer | Customer |
|---|---|---|---|---|---|
| Read product tickets | Yes | Yes | Yes | Yes | Own tickets |
| Read internal notes and their attachments | Yes | Yes | Yes | No | No |
| Add internal notes and attachments | Yes | Yes | Yes | No | No |
| Send public replies | Yes | Yes | No | No | Own tickets |
| Create tickets | Yes | Yes | No | No | Yes |
| Edit ticket details or status | Yes | Yes | No | No | No |
| Be assigned tickets | Yes | Yes | No | No | No |
| Manage product settings, members, and webhooks | Yes | No | No | No | No |
