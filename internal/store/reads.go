package store

import (
	"fmt"
	"time"
)

func (s *Store) MarkIssueRead(issueID, userID int64, when time.Time) error {
	if issueID < 1 || userID < 1 {
		return fmt.Errorf("%w: issue and user are required", ErrValidation)
	}
	_, err := s.db.Exec(`
		INSERT INTO ticket_reads (issue_id, user_id, last_read_at)
		VALUES (?, ?, ?)
		ON CONFLICT(issue_id, user_id) DO UPDATE SET last_read_at = excluded.last_read_at`,
		issueID, userID, formatTime(when.UTC()),
	)
	return normalizeSQLError(err)
}

func (s *Store) IssueReadTimes(userID int64, issueIDs []int64) (map[int64]time.Time, error) {
	result := make(map[int64]time.Time)
	if userID < 1 || len(issueIDs) == 0 {
		return result, nil
	}
	args := make([]any, 0, len(issueIDs)+1)
	args = append(args, userID)
	for _, id := range issueIDs {
		args = append(args, id)
	}
	rows, err := s.db.Query(`
		SELECT issue_id, last_read_at
		FROM ticket_reads
		WHERE user_id = ? AND issue_id IN (`+placeholders(len(issueIDs))+`)`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var issueID int64
		var lastRead string
		if err := rows.Scan(&issueID, &lastRead); err != nil {
			return nil, err
		}
		result[issueID] = parseTime(lastRead)
	}
	return result, rows.Err()
}
