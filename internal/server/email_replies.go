package server

import (
	"bytes"
	"errors"
	"fmt"

	"pappice/internal/notify"
	"pappice/internal/store"
)

// AcceptEmailReply receives an authenticated message from the IMAP worker.
func (s *Server) AcceptEmailReply(reply notify.EmailReply) error {
	if len(reply.Attachments) > s.options.MaxUploadFiles {
		return fmt.Errorf("%w: too many attachments", notify.ErrRejected)
	}
	var uploads []storedUpload
	keep := false
	defer func() {
		if !keep {
			cleanupStoredUploads(uploads)
		}
	}()
	for _, attachment := range reply.Attachments {
		upload, err := s.saveUploadedFile(bytes.NewReader(attachment.Data), attachment.Filename)
		if err != nil {
			if errors.Is(err, store.ErrValidation) {
				return fmt.Errorf("%w: attachment violates upload limits", notify.ErrRejected)
			}
			return err
		}
		uploads = append(uploads, upload)
	}
	result, err := s.store.SaveEmailReply(reply.Token, reply.From, reply.Key, reply.Body, attachmentInputs(uploads))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrValidation) || errors.Is(err, store.ErrConflict) {
			return fmt.Errorf("%w: invalid reply, credential, access, or reused message ID", notify.ErrRejected)
		}
		return err
	}
	if !result.Replayed {
		keep = true
		s.dispatchEventsSoon()
	}
	return nil
}
