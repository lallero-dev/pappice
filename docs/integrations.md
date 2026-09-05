# API Replies And Webhook Retries

## Posting Replies

Authenticate with `Authorization: Bearer <token>`. Tokens inherit their owner's
permissions, and replies show that user's name. Use a separate account when an
integration should appear as a bot.

`POST /api/tickets/{id}/comments` accepts a JSON object with `body` and
`visibility`. Visibility defaults to `public`; `internal` requires staff access
to the product. A public reply reopens a closed ticket.

Use `Idempotency-Key` when a request might be retried:

```sh
curl 'https://support.example.test/api/tickets/123/comments' \
  -H "Authorization: Bearer $PAPPICE_TOKEN" \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: assistant:delivery-identifier:reply' \
  --data '{"body":"Suggested response for review.","visibility":"internal"}'
```

The same mechanism applies to `PATCH /api/tickets/{id}`, including a nested
`comment` and ticket field changes. JSON and multipart requests are supported.

- A key contains 1–200 visible ASCII characters, without spaces.
- Keys are scoped to the authenticated user and ticket, shared by both endpoints.
  Reuse the same key and request content for retries; use a new key for a new action.
- The first successful request commits the key, mutation, and domain events
  together. Concurrent matching requests create only one mutation.
- A matching retry returns the **current ticket**, with
  `Idempotency-Replayed: true`. It keeps the endpoint's normal success status:
  `201` for comment creation, `200` for ticket updates. It does not repeat comments,
  status changes, timestamps, read markers, or notification events.
- Reusing a key with different content returns `409 Conflict`. JSON whitespace
  and property order do not matter. Comment bodies are trimmed and omitted
  visibility means `public`. Attachment content, filenames, metadata, and order
  must match; temporary upload paths are excluded from comparison.
- Failed transactions do not consume the key. Keys survive restarts and have no
  time-based expiry; deleting the ticket or user removes its keys.
- Permissions are checked on every request, including retries. Omitting the key
  preserves the existing behavior: each successful request creates a new mutation.

Persist the request body before sending it. In particular, an AI worker should
retry its saved reply rather than generate new text under the same key.

## Receiving Webhooks

Webhook delivery can repeat when an HTTP response is lost or the process stops
before recording success. Failed deliveries are retried with backoff, normally
up to five attempts. Receivers must handle duplicates.

Every delivery includes:

- `X-Pappice-Event`: the event type.
- `X-Pappice-Delivery-ID`: an opaque identifier for this notification, reused
  across retries and crash recovery. Separate webhook subscriptions get separate
  IDs, even when triggered by the same ticket change.
- `delivery_id` in the JSON body, matching the header.
- `X-Pappice-Signature`: `sha256=` followed by the hex HMAC-SHA256 of the exact
  request body, using the webhook secret, when a secret is configured.

Verify the signature against the raw body using a constant-time comparison
before accepting work. Deduplicate using the signed body's `delivery_id`; if
using the header, check that it matches the body.

A receiver should persist each delivery in its own work queue under a unique
delivery ID, then promptly return a `2xx` response. An already queued delivery
also receives `2xx`. Return an error if persistence fails. Run slow work after
acknowledgment: Pappice's HTTP request timeout is eight seconds. Acknowledging
before saving the work can lose it if the receiver crashes.

Use an action-specific API key such as `assistant:<delivery_id>:reply` when the
worker posts its result. Durable receiver deduplication prevents duplicate jobs;
API idempotency prevents duplicate replies when the worker retries a write.
Pappice provides these identifiers and write guarantees; the external receiver
owns its queue and job deduplication.

Pending ticket updates may be coalesced until their first delivery attempt. Once
attempted, a notification's ID and payload stay fixed, and subsequent updates
create another notification. Delivery order is not guaranteed; fetch the current
ticket when acting on an event. Each manual webhook test gets a new ID.

## Upgrading

This change adds schema migration 8. Before starting the updated server against
an existing database, follow the usual upgrade procedure and run:

```sh
pappice db migrate --dry-run
pappice db migrate
```

Migration assigns IDs to existing queued notifications and creates the ticket
request table. It preserves existing notification payloads and delivery states.
It cannot deduplicate deliveries already processed before receivers used IDs.
