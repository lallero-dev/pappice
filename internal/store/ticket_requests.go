package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
)

// recordTicketRequestTx reserves a key in the ticket mutation's transaction.
// A rollback releases the key; a committed key survives process restarts.
func recordTicketRequestTx(tx *sql.Tx, input SaveTicketInput) (bool, error) {
	if input.IdempotencyKey == "" {
		return false, nil
	}
	if len(input.IdempotencyKey) > 200 {
		return false, fmt.Errorf("%w: idempotency key must be at most 200 bytes", ErrValidation)
	}
	for _, c := range input.IdempotencyKey {
		if c < '!' || c > '~' {
			return false, fmt.Errorf("%w: idempotency key must contain only visible ASCII characters", ErrValidation)
		}
	}

	attachments := slices.Clone(input.Attachments)
	for i := range attachments {
		// Retried uploads have new storage keys but the same content metadata.
		attachments[i].StorageKey = ""
	}
	request, err := json.Marshal(struct {
		Patch       UpdateTicket
		Comment     *AddComment
		Attachments []CreateAttachment
	}{input.Patch, input.Comment, attachments})
	if err != nil {
		return false, err
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(request))
	result, err := tx.Exec(`
		INSERT INTO ticket_requests (ticket_id, actor_user_id, idempotency_key, request_hash)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (ticket_id, actor_user_id, idempotency_key) DO NOTHING`,
		input.TicketID, input.ActorUserID, input.IdempotencyKey, hash,
	)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 0 {
		return false, err
	}
	var previousHash string
	if err := tx.QueryRow(`
		SELECT request_hash FROM ticket_requests
		WHERE ticket_id = ? AND actor_user_id = ? AND idempotency_key = ?`,
		input.TicketID, input.ActorUserID, input.IdempotencyKey,
	).Scan(&previousHash); err != nil {
		return false, err
	}
	if previousHash != hash {
		return false, fmt.Errorf("%w: idempotency key was already used for a different request", ErrConflict)
	}
	return true, nil
}
