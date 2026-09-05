package server

import (
	"embed"
	"net/http"
	"strings"
	"time"

	"pappice/internal/store"
)

//go:embed web/index.html web/static/*
var assets embed.FS

const (
	sessionCookieName     = "pappice_session"
	accountLinkExpiry     = 24 * time.Hour
	defaultSessionTTL     = 14 * 24 * time.Hour
	defaultBrandName      = "Pappice"
	defaultBrandSubtitle  = "customer support"
	defaultBrandColor     = "#5bb974"
	defaultUploadDir      = "pappice-uploads"
	defaultBackupDir      = "pappice-backups"
	defaultMaxUploadSize  = 10 << 20
	defaultMaxUploadFiles = 5
	defaultVersion        = "dev"
)

var (
	appAdminSections   = []string{"accounts", "tokens", "webhooks", "email", "maintenance", "audit"}
	appProductSections = []string{"general", "members", "webhooks", "deliveries"}
)

type RateLimit struct {
	Limit  int
	Window time.Duration
}

type Logger interface {
	Printf(format string, args ...any)
}

type Options struct {
	AllowInsecureWebhooks bool
	AllowPrivateWebhooks  bool
	TrustProxyHeaders     bool
	Branding              Branding
	DomainEventRetention  time.Duration
	EmailNotifications    bool
	NotificationDelay     time.Duration
	PublicURL             string
	SessionTTL            time.Duration
	Version               string
	UploadDir             string
	BackupDir             string
	MaxUploadSize         int64
	MaxUploadFiles        int
	AllowedUploadTypes    []string
	LoginRateLimit        RateLimit
	AccountLinkRateLimit  RateLimit
	Logger                Logger
}

type Branding struct {
	Name     string `json:"name"`
	Subtitle string `json:"subtitle"`
	Mark     string `json:"mark"`
	Color    string `json:"color"`
}

type Server struct {
	store              *store.Store
	started            time.Time
	handler            http.Handler
	client             *http.Client
	eventWake          chan struct{}
	webhookWake        chan struct{}
	options            Options
	loginLimiter       *requestLimiter
	accountLinkLimiter *requestLimiter
}

func NewServer(tracker *store.Store, opts ...Options) *Server {
	options := Options{}
	if len(opts) > 0 {
		options = opts[0]
	}
	if options.SessionTTL <= 0 {
		options.SessionTTL = defaultSessionTTL
	}
	options.Version = strings.TrimSpace(options.Version)
	if options.Version == "" {
		options.Version = defaultVersion
	}
	options.Branding = normalizeBranding(options.Branding)
	options = normalizeUploadOptions(options)
	options.BackupDir = strings.TrimSpace(options.BackupDir)
	if options.BackupDir == "" {
		options.BackupDir = defaultBackupDir
	}
	options.LoginRateLimit = withDefaultRateLimit(options.LoginRateLimit, 10, time.Minute)
	options.AccountLinkRateLimit = withDefaultRateLimit(options.AccountLinkRateLimit, 10, time.Minute)
	s := &Server{
		store:              tracker,
		started:            time.Now().UTC(),
		eventWake:          make(chan struct{}, 1),
		webhookWake:        make(chan struct{}, 1),
		options:            options,
		loginLimiter:       newRequestLimiter(options.LoginRateLimit),
		accountLinkLimiter: newRequestLimiter(options.AccountLinkRateLimit),
	}
	s.client = s.newWebhookClient()
	s.handler = s.securityHeaders(s.routes())
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	setupRequired, err := s.store.SetupRequired()
	if err != nil {
		respondStoreError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"name":           "pappice",
		"branding":       s.options.Branding,
		"started_at":     s.started,
		"needs_setup":    setupRequired,
		"statuses":       store.Statuses(),
		"priorities":     store.Priorities(),
		"roles":          store.Roles(),
		"product_roles":  store.ProductRoles(),
		"webhook_events": store.Events(),
		"email_enabled":  s.options.EmailNotifications,
		"uploads":        s.publicUploadConfig(),
	})
}
