package store

import (
	"fmt"
	"time"
)

func (s *Store) MarkTicketRead(ticketID, userID int64, when time.Time) error {
	if ticketID < 1 || userID < 1 {
		return fmt.Errorf("%w: ticket and user are required", ErrValidation)
	}
	_, err := s.db.Exec(`
		INSERT INTO ticket_reads (ticket_id, user_id, last_read_at)
		VALUES (?, ?, ?)
		ON CONFLICT(ticket_id, user_id) DO UPDATE SET last_read_at = excluded.last_read_at`,
		ticketID, userID, formatTime(when.UTC()),
	)
	return normalizeSQLError(err)
}

func (s *Store) TicketReadTimes(userID int64, ticketIDs []int64) (map[int64]time.Time, error) {
	result := make(map[int64]time.Time)
	if userID < 1 || len(ticketIDs) == 0 {
		return result, nil
	}
	args := make([]any, 0, len(ticketIDs)+1)
	args = append(args, userID)
	for _, id := range ticketIDs {
		args = append(args, id)
	}
	rows, err := s.db.Query(`
		SELECT ticket_id, last_read_at
		FROM ticket_reads
		WHERE user_id = ? AND ticket_id IN (`+placeholders(len(ticketIDs))+`)`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var ticketID int64
		var lastRead string
		if err := rows.Scan(&ticketID, &lastRead); err != nil {
			return nil, err
		}
		result[ticketID] = parseTime(lastRead)
	}
	return result, rows.Err()
}
