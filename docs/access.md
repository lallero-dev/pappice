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

Deleting tickets and products requires an Admin account. No product membership
means no access, except for Admin accounts.

## Internal Contributors

Use **Internal contributor** for colleagues or integrations that provide internal
advice. They can read the product's tickets, including internal notes and
attachments, and contribute internal notes. They cannot create tickets, send
public replies, change ticket details, or manage the product.

Choose a **Staff** account type, then assign the **Internal contributor** product
role in the product's Members settings. The API value is `internal_contributor`.
Assign the role separately in each product the account should access. An Admin
account cannot be restricted this way because its global privileges override
product roles.

The browser composer is fixed to Internal note and shows an Add note button.
Internal notes leave the ticket's open/closed state unchanged. They update its
activity timestamp and unread counts, as other internal notes do, without
creating customer email or webhook notification events.

API tokens inherit the account's current product roles. Use
`"visibility":"internal"` explicitly when posting notes; omitted visibility
defaults to public and is rejected for Internal contributors. Both comment
creation and comments submitted through ticket updates enforce the restriction,
including multipart uploads. See [API replies and retries](./integrations.md).

Membership changes apply to subsequent requests through existing sessions and
tokens. Existing roles retain their behavior. This role requires no database
migration.
