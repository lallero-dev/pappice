package store

import (
	"fmt"
	"time"
)

func (s *Store) MarkTicketRead(ticketID, userID int64, when time.Time) error {
	if ticketID < 1 || userID < 1 {
		return fmt.Errorf("%w: ticket and user are required", ErrValidation)
	}
	return markTicketRead(s.db, ticketID, userID, when)
}

func markTicketRead(exec sqlExecer, ticketID, userID int64, when time.Time) error {
	_, err := exec.Exec(`
		INSERT INTO ticket_reads (ticket_id, user_id, last_read_at)
		VALUES (?, ?, ?)
		ON CONFLICT(ticket_id, user_id) DO UPDATE SET last_read_at = excluded.last_read_at`,
		ticketID, userID, formatTime(when.UTC()),
	)
	return normalizeSQLError(err)
}
