package server

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	pathpkg "path"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"pappice/internal/store"
)

var defaultAllowedUploadTypes = []string{
	"image/png",
	"image/jpeg",
	"image/gif",
	"image/webp",
	"application/pdf",
	"application/json",
	"application/zip",
	"text/plain",
	"text/csv",
}

type uploadConfig struct {
	MaxSizeBytes int64    `json:"max_size_bytes"`
	MaxFiles     int      `json:"max_files"`
	AllowedTypes []string `json:"allowed_types"`
}

type storedUpload struct {
	Attachment store.CreateAttachment
	Path       string
	Created    bool
}

func normalizeUploadOptions(options Options) Options {
	options.UploadDir = strings.TrimSpace(options.UploadDir)
	if options.UploadDir == "" {
		options.UploadDir = defaultUploadDir
	}
	if options.MaxUploadSize <= 0 {
		options.MaxUploadSize = defaultMaxUploadSize
	}
	if options.MaxUploadFiles <= 0 {
		options.MaxUploadFiles = defaultMaxUploadFiles
	}
	options.AllowedUploadTypes = normalizeAllowedUploadTypes(options.AllowedUploadTypes)
	return options
}

func normalizeAllowedUploadTypes(values []string) []string {
	if len(values) == 0 {
		values = defaultAllowedUploadTypes
	}
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = cleanContentType(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	if len(result) == 0 {
		return append([]string(nil), defaultAllowedUploadTypes...)
	}
	return result
}

func (s *Server) publicUploadConfig() uploadConfig {
	return uploadConfig{
		MaxSizeBytes: s.options.MaxUploadSize,
		MaxFiles:     s.options.MaxUploadFiles,
		AllowedTypes: append([]string(nil), s.options.AllowedUploadTypes...),
	}
}

func (s *Server) handleAttachmentByID(w http.ResponseWriter, r *http.Request) {
	auth, ok := s.requireAuth(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	id, ok := parseTrailingID(w, r.URL.Path, "/api/attachments/")
	if !ok {
		return
	}
	attachment, err := s.store.GetAttachment(id)
	if err != nil {
		respondStoreError(w, err)
		return
	}
	ticket, err := s.store.GetTicket(attachment.TicketID)
	if err != nil {
		respondStoreError(w, err)
		return
	}
	if !s.canReadTicket(auth.User, ticket) || !s.canReadAttachment(auth.User, ticket, attachment) {
		respondError(w, http.StatusNotFound, "not found")
		return
	}

	file, stat, err := s.openAttachmentFile(attachment.StorageKey)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			respondError(w, http.StatusNotFound, "attachment file not found")
			return
		}
		respondError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer file.Close()

	contentType := defaultString(attachment.ContentType, "application/octet-stream")
	disposition := "attachment"
	if r.URL.Query().Get("preview") == "1" && isInlinePreviewImage(contentType) {
		disposition = "inline"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": attachment.Filename}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, attachment.Filename, stat.ModTime(), file)
}

func isInlinePreviewImage(contentType string) bool {
	switch cleanContentType(contentType) {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}

func (s *Server) canReadAttachment(user store.User, ticket store.Ticket, attachment store.Attachment) bool {
	if attachment.CommentID == nil {
		return true
	}
	for _, comment := range ticket.Comments {
		if comment.ID != *attachment.CommentID {
			continue
		}
		return comment.Visibility == "" || comment.Visibility == "public" || s.canEditTicket(user, ticket.ProductID)
	}
	return false
}

func isMultipartRequest(r *http.Request) bool {
	contentType := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	return strings.HasPrefix(contentType, "multipart/form-data")
}

func (s *Server) parseMultipartForm(w http.ResponseWriter, r *http.Request) bool {
	if r.MultipartForm != nil {
		return true
	}
	maxBodyBytes := int64(s.options.MaxUploadFiles)*s.options.MaxUploadSize + 1<<20
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := r.ParseMultipartForm(maxBodyBytes); err != nil {
		respondError(w, http.StatusBadRequest, "Upload blocked: each file can be up to "+formatUploadBytes(s.options.MaxUploadSize)+", with at most "+strconv.Itoa(s.options.MaxUploadFiles)+" files per request.")
		return false
	}
	return true
}

func cleanupMultipartForm(r *http.Request) {
	if r != nil && r.MultipartForm != nil {
		_ = r.MultipartForm.RemoveAll()
	}
}

func (s *Server) saveRequestAttachments(w http.ResponseWriter, r *http.Request) ([]storedUpload, bool) {
	uploads, err := s.saveMultipartAttachments(r.MultipartForm)
	if err != nil {
		respondUploadError(w, err)
		return nil, false
	}
	return uploads, true
}

func respondUploadError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrValidation) {
		respondError(w, http.StatusBadRequest, "Upload blocked: "+cleanValidationMessage(err))
		return
	}
	respondError(w, http.StatusInternalServerError, "internal server error")
}

func cleanValidationMessage(err error) string {
	message := strings.TrimSpace(err.Error())
	message = strings.TrimPrefix(message, store.ErrValidation.Error()+": ")
	if message == "" {
		return "the selected files are not allowed"
	}
	return message
}

func (s *Server) saveMultipartAttachments(form *multipart.Form) ([]storedUpload, error) {
	if form == nil {
		return nil, nil
	}
	var headers []*multipart.FileHeader
	for _, key := range []string{"attachments", "attachment"} {
		headers = append(headers, form.File[key]...)
	}
	if len(headers) == 0 {
		return nil, nil
	}
	if len(headers) > s.options.MaxUploadFiles {
		return nil, fmt.Errorf("%w: at most %d files can be attached", store.ErrValidation, s.options.MaxUploadFiles)
	}

	uploads := make([]storedUpload, 0, len(headers))
	for _, header := range headers {
		if strings.TrimSpace(header.Filename) == "" {
			continue
		}
		file, err := header.Open()
		if err != nil {
			cleanupStoredUploads(uploads)
			return nil, err
		}
		upload, err := s.saveUploadedFile(file, header)
		_ = file.Close()
		if err != nil {
			cleanupStoredUploads(uploads)
			return nil, err
		}
		uploads = append(uploads, upload)
	}
	return uploads, nil
}

func (s *Server) saveUploadedFile(file multipart.File, header *multipart.FileHeader) (storedUpload, error) {
	filename := sanitizeAttachmentFilename(header.Filename)
	if filename == "" {
		return storedUpload{}, fmt.Errorf("%w: attachment filename is required", store.ErrValidation)
	}
	if err := os.MkdirAll(s.options.UploadDir, 0o755); err != nil {
		return storedUpload{}, err
	}
	temp, err := os.CreateTemp(s.options.UploadDir, ".upload-*")
	if err != nil {
		return storedUpload{}, err
	}
	tempPath := temp.Name()
	keepTemp := false
	defer func() {
		_ = temp.Close()
		if !keepTemp {
			_ = os.Remove(tempPath)
		}
	}()

	hash := sha256.New()
	var sniff []byte
	buffer := make([]byte, 32*1024)
	var size int64
	for {
		n, readErr := file.Read(buffer)
		if n > 0 {
			size += int64(n)
			if size > s.options.MaxUploadSize {
				return storedUpload{}, fmt.Errorf("%w: attachment %q exceeds %d bytes", store.ErrValidation, filename, s.options.MaxUploadSize)
			}
			if len(sniff) < 512 {
				remaining := 512 - len(sniff)
				if n < remaining {
					remaining = n
				}
				sniff = append(sniff, buffer[:remaining]...)
			}
			if _, err := hash.Write(buffer[:n]); err != nil {
				return storedUpload{}, err
			}
			if _, err := temp.Write(buffer[:n]); err != nil {
				return storedUpload{}, err
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return storedUpload{}, readErr
		}
	}
	if size == 0 {
		return storedUpload{}, fmt.Errorf("%w: attachment %q is empty", store.ErrValidation, filename)
	}
	contentType := cleanContentType(http.DetectContentType(sniff))
	if !s.uploadContentTypeAllowed(contentType) {
		return storedUpload{}, fmt.Errorf("%w: attachment type %q is not allowed", store.ErrValidation, contentType)
	}
	if err := temp.Close(); err != nil {
		return storedUpload{}, err
	}

	sum := hex.EncodeToString(hash.Sum(nil))
	storageKey := filepath.ToSlash(filepath.Join(sum[:2], sum[2:4], sum))
	finalPath := filepath.Join(s.options.UploadDir, filepath.FromSlash(storageKey))
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		return storedUpload{}, err
	}
	created := true
	if _, err := os.Stat(finalPath); err == nil {
		created = false
	} else if !errors.Is(err, os.ErrNotExist) {
		return storedUpload{}, err
	}
	if created {
		if err := os.Rename(tempPath, finalPath); err != nil {
			if _, statErr := os.Stat(finalPath); statErr == nil {
				created = false
			} else {
				return storedUpload{}, err
			}
		} else {
			keepTemp = true
		}
	}

	return storedUpload{
		Attachment: store.CreateAttachment{
			Filename:    filename,
			ContentType: contentType,
			SizeBytes:   size,
			SHA256:      sum,
			StorageKey:  storageKey,
		},
		Path:    finalPath,
		Created: created,
	}, nil
}

func (s *Server) uploadContentTypeAllowed(contentType string) bool {
	contentType = cleanContentType(contentType)
	for _, allowed := range s.options.AllowedUploadTypes {
		if allowed == "*" || allowed == "*/*" || allowed == contentType {
			return true
		}
	}
	return false
}

func (s *Server) openAttachmentFile(storageKey string) (*os.File, os.FileInfo, error) {
	path, err := s.attachmentFilePath(storageKey)
	if err != nil {
		return nil, nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	stat, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, err
	}
	return file, stat, nil
}

func (s *Server) attachmentFilePath(storageKey string) (string, error) {
	clean := pathpkg.Clean(strings.TrimSpace(storageKey))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || pathpkg.IsAbs(clean) {
		return "", os.ErrNotExist
	}
	return filepath.Join(s.options.UploadDir, filepath.FromSlash(clean)), nil
}

func (s *Server) removeOrphanedAttachmentFiles(storageKeys []string) {
	seen := map[string]struct{}{}
	for _, storageKey := range storageKeys {
		if _, ok := seen[storageKey]; ok {
			continue
		}
		seen[storageKey] = struct{}{}
		path, err := s.attachmentFilePath(storageKey)
		if err == nil {
			_ = os.Remove(path)
		}
	}
}

func cleanupStoredUploads(uploads []storedUpload) {
	for _, upload := range uploads {
		if upload.Created && upload.Path != "" {
			_ = os.Remove(upload.Path)
		}
	}
}

func attachmentInputs(uploads []storedUpload) []store.CreateAttachment {
	inputs := make([]store.CreateAttachment, 0, len(uploads))
	for _, upload := range uploads {
		inputs = append(inputs, upload.Attachment)
	}
	return inputs
}

func multipartCreateTicketInput(r *http.Request, fallbackProductID int64) (store.CreateTicket, error) {
	productID := fallbackProductID
	if productID == 0 {
		parsed, err := strconv.ParseInt(strings.TrimSpace(multipartValue(r, "product_id")), 10, 64)
		if err != nil || parsed < 1 {
			return store.CreateTicket{}, fmt.Errorf("%w: product_id is required", store.ErrValidation)
		}
		productID = parsed
	}
	return store.CreateTicket{
		ProductID:      productID,
		Title:          multipartValue(r, "title"),
		Description:    multipartValue(r, "description"),
		Priority:       multipartValue(r, "priority"),
		Assignee:       multipartValue(r, "assignee"),
		Reporter:       multipartValue(r, "requester"),
		Source:         multipartValue(r, "source"),
		RequesterName:  multipartValue(r, "requester_name"),
		RequesterEmail: multipartValue(r, "requester_email"),
	}, nil
}

func multipartTicketPatchInput(r *http.Request) ticketPatchInput {
	var input ticketPatchInput
	if value, ok := multipartOptionalValue(r, "title"); ok {
		input.Title = &value
	}
	if value, ok := multipartOptionalValue(r, "description"); ok {
		input.Description = &value
	}
	if value, ok := multipartOptionalValue(r, "status"); ok {
		input.Status = &value
	}
	if value, ok := multipartOptionalValue(r, "priority"); ok {
		input.Priority = &value
	}
	if value, ok := multipartOptionalValue(r, "assignee"); ok {
		input.Assignee = &value
	}
	body, hasBody := multipartOptionalValue(r, "body")
	visibility, hasVisibility := multipartOptionalValue(r, "visibility")
	if hasBody || hasVisibility {
		input.Comment = &store.AddComment{Body: body, Visibility: visibility}
	}
	return input
}

func multipartCommentInput(r *http.Request) store.AddComment {
	return store.AddComment{
		Body:       multipartValue(r, "body"),
		Visibility: multipartValue(r, "visibility"),
	}
}

func multipartValue(r *http.Request, name string) string {
	value, _ := multipartOptionalValue(r, name)
	return value
}

func multipartOptionalValue(r *http.Request, name string) (string, bool) {
	if r.MultipartForm == nil {
		return "", false
	}
	values, ok := r.MultipartForm.Value[name]
	if !ok || len(values) == 0 {
		return "", false
	}
	return strings.TrimSpace(values[0]), true
}

func cleanContentType(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return ""
	}
	if mediaType, _, err := mime.ParseMediaType(value); err == nil {
		return strings.ToLower(mediaType)
	}
	if before, _, ok := strings.Cut(value, ";"); ok {
		return strings.TrimSpace(strings.ToLower(before))
	}
	return value
}

func sanitizeAttachmentFilename(value string) string {
	value = strings.TrimSpace(filepath.Base(value))
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value)
	value = strings.Trim(value, " .")
	if len(value) > 180 {
		value = value[:180]
		value = strings.Trim(value, " .")
	}
	return value
}

func formatUploadBytes(bytes int64) string {
	switch {
	case bytes >= 1024*1024 && bytes%(1024*1024) == 0:
		return strconv.FormatInt(bytes/(1024*1024), 10) + " MB"
	case bytes >= 1024*1024:
		return strconv.FormatFloat(float64(bytes)/(1024*1024), 'f', 1, 64) + " MB"
	case bytes >= 1024 && bytes%1024 == 0:
		return strconv.FormatInt(bytes/1024, 10) + " KB"
	case bytes >= 1024:
		return strconv.FormatFloat(float64(bytes)/1024, 'f', 1, 64) + " KB"
	default:
		return strconv.FormatInt(bytes, 10) + " B"
	}
}
