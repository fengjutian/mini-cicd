package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/charlesfeng/mini-cicd/apps/server/internal/application"
	"github.com/charlesfeng/mini-cicd/apps/server/internal/artifact"
	"github.com/charlesfeng/mini-cicd/apps/server/internal/auth"
	"github.com/charlesfeng/mini-cicd/apps/server/internal/config"
	"github.com/charlesfeng/mini-cicd/apps/server/internal/deployment"
	"github.com/charlesfeng/mini-cicd/apps/server/internal/gitops"
	"github.com/charlesfeng/mini-cicd/apps/server/internal/logstore"
	"github.com/charlesfeng/mini-cicd/apps/server/internal/maintenance"
	"github.com/charlesfeng/mini-cicd/apps/server/internal/notification"
	"github.com/charlesfeng/mini-cicd/apps/server/internal/project"
	"github.com/charlesfeng/mini-cicd/apps/server/internal/providerstatus"
	"github.com/charlesfeng/mini-cicd/apps/server/internal/runner"
	"github.com/charlesfeng/mini-cicd/apps/server/internal/secret"
	"github.com/charlesfeng/mini-cicd/apps/server/internal/workspace"
)

const sessionCookie = "minicicd_session"

var Version = "dev"

type contextKey string

const userKey contextKey = "user"

type user struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	Username  string `json:"username"`
	Role      string `json:"role"`
	CreatedAt string `json:"createdAt"`
}

type Server struct {
	db             *sql.DB
	cfg            config.Config
	logger         *slog.Logger
	limiter        *loginLimiter
	webhookLimiter *requestLimiter
	projects       *project.Service
	applications   *application.Service
	deps           *deployment.Service
	runner         *runner.Manager
	logs           *logstore.Store
	box            *secret.Box
	handler        http.Handler
	webhookCtx     context.Context
	webhookCancel  context.CancelFunc
	webhookWG      sync.WaitGroup
	maintenance    *maintenance.Manager
	notifications  *notification.Manager
	providerStatus *providerstatus.Manager
}

func New(db *sql.DB, cfg config.Config, logger *slog.Logger) (*Server, error) {
	box, err := secret.New(cfg.MasterKey)
	if err != nil {
		return nil, err
	}
	if !box.Available() {
		var encrypted int
		if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM project_variables WHERE cipher_value IS NOT NULL UNION ALL SELECT 1 FROM projects WHERE git_secret_cipher IS NOT NULL OR ssh_private_key_cipher IS NOT NULL OR webhook_secret_cipher IS NOT NULL)`).Scan(&encrypted); err != nil {
			return nil, err
		}
		if encrypted != 0 {
			return nil, errors.New("MINICICD_MASTER_KEY is required because encrypted secrets already exist")
		}
	}
	git := gitops.New(cfg.DataDir, db, box)
	deps := deployment.New(db, git)
	workspaceDir := cfg.RunnerWorkspaceDir
	if workspaceDir == "" {
		workspaceDir = filepath.Join(cfg.DataDir, "workspaces")
	}
	workspaceMode := os.FileMode(0o700)
	if cfg.RunnerEndpoint != "" {
		workspaceMode = 0o770
	}
	spaces, err := workspace.NewRoot(workspaceDir, workspaceMode)
	if err != nil {
		return nil, err
	}
	logs, err := logstore.New(cfg.DataDir, cfg.LogMaxBytes)
	if err != nil {
		return nil, err
	}
	artifacts, err := artifact.New(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	s := &Server{db: db, cfg: cfg, logger: logger, limiter: newLoginLimiter(), webhookLimiter: newRequestLimiter(120), projects: project.New(db, box), applications: application.New(db), deps: deps, logs: logs, box: box}
	s.webhookCtx, s.webhookCancel = context.WithCancel(context.Background())
	s.runner = runner.New(db, deps, git, spaces, logs, box, cfg.Shell, cfg.GlobalParallel, cfg.CancelGrace, logger).UseRemote(cfg.RunnerEndpoint).UseArtifacts(artifacts)
	s.maintenance = maintenance.New(db, logs, spaces, cfg.CleanupInterval, cfg.WorkspaceRetention, cfg.LogRetention, cfg.DeploymentRetention, logger).ConfigureBackups(cfg.DatabasePath, filepath.Join(cfg.DataDir, "backups"), cfg.BackupInterval, cfg.BackupRetention).ConfigureAudit(cfg.AuditRetention, cfg.AuditMaxEvents)
	s.notifications = notification.New(db, box, logger)
	s.providerStatus = providerstatus.New(db, box, logger)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/status", s.status)
	mux.HandleFunc("POST /api/v1/setup", s.setup)
	mux.HandleFunc("POST /api/v1/auth/login", s.login)
	mux.HandleFunc("POST /api/v1/auth/logout", s.requireAuth(s.logout))
	mux.HandleFunc("GET /api/v1/auth/me", s.requireAuth(s.me))
	mux.HandleFunc("PUT /api/v1/auth/password", s.requireAuth(s.changePassword))
	mux.HandleFunc("GET /api/v1/auth/sessions", s.requireAuth(s.listSessions))
	mux.HandleFunc("DELETE /api/v1/auth/sessions/{id}", s.requireAuth(s.deleteSession))
	mux.HandleFunc("GET /api/v1/system/checks", s.requireAuth(s.systemChecks))
	mux.HandleFunc("GET /api/v1/system/storage", s.requireAuth(s.systemStorage))
	mux.HandleFunc("GET /api/v1/audit-events", s.requireAuth(s.listAuditEvents))
	mux.HandleFunc("GET /api/v1/projects", s.requireAuth(s.listProjects))
	mux.HandleFunc("POST /api/v1/projects", s.requireAuth(s.createProject))
	mux.HandleFunc("GET /api/v1/projects/{id}", s.requireAuth(s.getProject))
	mux.HandleFunc("PUT /api/v1/projects/{id}", s.requireAuth(s.updateProject))
	mux.HandleFunc("DELETE /api/v1/projects/{id}", s.requireAuth(s.archiveProject))
	mux.HandleFunc("GET /api/v1/projects/{id}/variables", s.requireAuth(s.listVariables))
	mux.HandleFunc("PUT /api/v1/projects/{id}/variables/{name}", s.requireAuth(s.putVariable))
	mux.HandleFunc("DELETE /api/v1/projects/{id}/variables/{name}", s.requireAuth(s.deleteVariable))
	mux.HandleFunc("GET /api/v1/projects/{id}/environments/{environment}/variables", s.requireAuth(s.listEnvironmentVariables))
	mux.HandleFunc("PUT /api/v1/projects/{id}/environments/{environment}/variables/{name}", s.requireAuth(s.putEnvironmentVariable))
	mux.HandleFunc("DELETE /api/v1/projects/{id}/environments/{environment}/variables/{name}", s.requireAuth(s.deleteEnvironmentVariable))
	mux.HandleFunc("POST /api/v1/projects/{id}/deployments", s.requireAuth(s.createDeployment))
	mux.HandleFunc("GET /api/v1/projects/{id}/deployments", s.requireAuth(s.listDeployments))
	mux.HandleFunc("GET /api/v1/projects/{id}/webhook-deliveries", s.requireAuth(s.listWebhookDeliveries))
	mux.HandleFunc("GET /api/v1/projects/{id}/application/status", s.requireAuth(s.applicationStatus))
	mux.HandleFunc("GET /api/v1/projects/{id}/application/logs", s.requireAuth(s.applicationLogs))
	mux.HandleFunc("POST /api/v1/projects/{id}/application/{action}", s.requireAuth(s.applicationAction))
	mux.HandleFunc("GET /api/v1/projects/{id}/notification-deliveries", s.requireAuth(s.listNotificationDeliveries))
	mux.HandleFunc("GET /api/v1/projects/{id}/commit-status-deliveries", s.requireAuth(s.listCommitStatusDeliveries))
	mux.HandleFunc("GET /api/v1/deployments/{id}", s.requireAuth(s.getDeployment))
	mux.HandleFunc("GET /api/v1/deployments/{id}/steps", s.requireAuth(s.deploymentSteps))
	mux.HandleFunc("POST /api/v1/deployments/{id}/cancel", s.requireAuth(s.cancelDeployment))
	mux.HandleFunc("POST /api/v1/deployments/{id}/redeploy", s.requireAuth(s.redeployDeployment))
	mux.HandleFunc("POST /api/v1/deployments/{id}/rollback", s.requireAuth(s.rollbackDeployment))
	mux.HandleFunc("POST /api/v1/deployments/{id}/approve", s.requireAuth(s.approveDeployment))
	mux.HandleFunc("GET /api/v1/deployments/{id}/logs", s.requireAuth(s.deploymentLogs))
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("POST /api/v1/webhooks/{projectID}/{provider}", s.webhook)
	mux.HandleFunc("GET /api/v1/dashboard", s.requireAuth(s.dashboard))
	mux.HandleFunc("GET /", s.index)
	s.handler = s.audit(s.securityHeaders(s.sameOrigin(s.limitBody(mux))))
	s.runner.Start()
	s.recoverWebhooks()
	s.maintenance.Start()
	s.notifications.Start()
	s.providerStatus.Start()
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.handler.ServeHTTP(w, r) }
func (s *Server) Close() {
	s.providerStatus.Stop()
	s.notifications.Stop()
	s.maintenance.Stop()
	s.webhookCancel()
	s.webhookWG.Wait()
	s.runner.Stop()
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	if err := s.db.Ping(); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unhealthy"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) status(w http.ResponseWriter, _ *http.Request) {
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Could not read system status.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"initialized": count > 0, "version": Version})
}

func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email           string `json:"email"`
		Username        string `json:"username"`
		Password        string `json:"password"`
		ConfirmPassword string `json:"confirmPassword"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	input.Username = strings.TrimSpace(input.Username)
	if _, err := mail.ParseAddress(input.Email); err != nil || !strings.Contains(input.Email, "@") {
		writeError(w, http.StatusUnprocessableEntity, "invalid_email", "Enter a valid email address.")
		return
	}
	if len(input.Username) < 2 || len(input.Username) > 64 {
		writeError(w, http.StatusUnprocessableEntity, "invalid_username", "Username must be between 2 and 64 characters.")
		return
	}
	if input.Password != input.ConfirmPassword {
		writeError(w, http.StatusUnprocessableEntity, "password_mismatch", "Passwords do not match.")
		return
	}
	passwordHash, err := auth.HashPassword(input.Password)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_password", err.Error())
		return
	}

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Could not initialize the system.")
		return
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Could not initialize the system.")
		return
	}
	if count != 0 {
		writeError(w, http.StatusConflict, "already_initialized", "The system has already been initialized.")
		return
	}

	now := time.Now().UTC()
	userID, err := randomID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Could not initialize the system.")
		return
	}
	if _, err := tx.ExecContext(r.Context(), `INSERT INTO users(id,email,username,password_hash,role,created_at) VALUES(?,?,?,?,?,?)`,
		userID, input.Email, input.Username, passwordHash, "owner", now.Format(time.RFC3339Nano)); err != nil {
		if isUniqueError(err) {
			writeError(w, http.StatusConflict, "already_initialized", "The system has already been initialized.")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "Could not initialize the system.")
		return
	}
	rawToken, tokenHash, err := auth.NewSessionToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Could not create a session.")
		return
	}
	if _, err := tx.ExecContext(r.Context(), `INSERT INTO sessions(id_hash,user_id,created_at,expires_at,last_seen_at,ip_address,user_agent) VALUES(?,?,?,?,?,?,?)`,
		tokenHash, userID, now.Format(time.RFC3339Nano), now.Add(s.cfg.SessionTTL).Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), s.clientIP(r), r.UserAgent()); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Could not create a session.")
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Could not initialize the system.")
		return
	}
	s.setSessionCookie(w, rawToken, now.Add(s.cfg.SessionTTL))
	writeJSON(w, http.StatusCreated, user{ID: userID, Email: input.Email, Username: input.Username, Role: "owner", CreatedAt: now.Format(time.RFC3339Nano)})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	clientIP := s.clientIP(r)
	if !s.limiter.allow(clientIP) {
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, "rate_limited", "Too many login attempts. Try again later.")
		return
	}
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	var u user
	var passwordHash string
	err := s.db.QueryRowContext(r.Context(), `SELECT id,email,username,password_hash,role,created_at FROM users WHERE email = ?`, strings.TrimSpace(input.Email)).
		Scan(&u.ID, &u.Email, &u.Username, &passwordHash, &u.Role, &u.CreatedAt)
	if err != nil || !auth.VerifyPassword(passwordHash, input.Password) {
		s.limiter.failed(clientIP)
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "Email or password is incorrect.")
		return
	}
	s.limiter.success(clientIP)
	rawToken, tokenHash, err := auth.NewSessionToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Could not create a session.")
		return
	}
	now := time.Now().UTC()
	if _, err := s.db.ExecContext(r.Context(), `INSERT INTO sessions(id_hash,user_id,created_at,expires_at,last_seen_at,ip_address,user_agent) VALUES(?,?,?,?,?,?,?)`,
		tokenHash, u.ID, now.Format(time.RFC3339Nano), now.Add(s.cfg.SessionTTL).Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), s.clientIP(r), r.UserAgent()); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Could not create a session.")
		return
	}
	s.setSessionCookie(w, rawToken, now.Add(s.cfg.SessionTTL))
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	cookie, _ := r.Cookie(sessionCookie)
	if cookie != nil {
		_, _ = s.db.ExecContext(r.Context(), `DELETE FROM sessions WHERE id_hash = ?`, auth.HashSessionToken(cookie.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.cfg.SecureCookies, SameSite: http.SameSiteStrictMode})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, r.Context().Value(userKey).(user))
}

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookie)
		if err != nil || cookie.Value == "" {
			writeError(w, http.StatusUnauthorized, "unauthorized", "Authentication is required.")
			return
		}
		var u user
		var expiresAt, lastSeenAt string
		err = s.db.QueryRowContext(r.Context(), `
SELECT u.id,u.email,u.username,u.role,u.created_at,s.expires_at,s.last_seen_at
FROM sessions s JOIN users u ON u.id=s.user_id
WHERE s.id_hash=?`, auth.HashSessionToken(cookie.Value)).Scan(&u.ID, &u.Email, &u.Username, &u.Role, &u.CreatedAt, &expiresAt, &lastSeenAt)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "The session is invalid.")
			return
		}
		now := time.Now().UTC()
		expires, err := time.Parse(time.RFC3339Nano, expiresAt)
		if err != nil || !expires.After(now) {
			_, _ = s.db.ExecContext(r.Context(), `DELETE FROM sessions WHERE id_hash=?`, auth.HashSessionToken(cookie.Value))
			writeError(w, http.StatusUnauthorized, "session_expired", "The session has expired.")
			return
		}
		if s.cfg.SessionIdleTTL > 0 {
			lastSeen, err := time.Parse(time.RFC3339Nano, lastSeenAt)
			if err != nil || now.Sub(lastSeen) > s.cfg.SessionIdleTTL {
				_, _ = s.db.ExecContext(r.Context(), `DELETE FROM sessions WHERE id_hash=?`, auth.HashSessionToken(cookie.Value))
				writeError(w, http.StatusUnauthorized, "session_idle_expired", "The session was idle for too long.")
				return
			}
		}
		_, _ = s.db.ExecContext(r.Context(), `UPDATE sessions SET last_seen_at=? WHERE id_hash=?`, now.Format(time.RFC3339Nano), auth.HashSessionToken(cookie.Value))
		next(w, r.WithContext(context.WithValue(r.Context(), userKey, u)))
	}
}

func (s *Server) setSessionCookie(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", Expires: expires, HttpOnly: true, Secure: s.cfg.SecureCookies, SameSite: http.SameSiteStrictMode})
}

func (s *Server) clientIP(r *http.Request) string {
	if s.cfg.TrustProxy {
		if value := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0]); value != "" {
			return value
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func (s *Server) limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		next.ServeHTTP(w, r)
	})
}

func (s *Server) sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		origin := r.Header.Get("Origin")
		if origin == "" {
			next.ServeHTTP(w, r)
			return
		}
		parsed, err := url.Parse(origin)
		if err != nil || !strings.EqualFold(parsed.Host, r.Host) {
			writeError(w, http.StatusForbidden, "origin_rejected", "Cross-origin requests are not allowed.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; style-src 'self'; script-src 'self'; connect-src 'self'; form-action 'self'")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
		if s.cfg.SecureCookies {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

func decodeJSON(r *http.Request, target any) error {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return errors.New("Content-Type must be application/json")
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("Request body must be valid JSON")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("Request body must contain a single JSON object")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func randomID() (string, error) {
	raw, _, err := auth.NewSessionToken()
	if err != nil {
		return "", err
	}
	return raw[:22], nil
}

func isUniqueError(err error) bool {
	return strings.Contains(strings.ToLower(fmt.Sprint(err)), "unique")
}

type loginLimiter struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
}

func newLoginLimiter() *loginLimiter { return &loginLimiter{attempts: make(map[string][]time.Time)} }

func (l *loginLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := time.Now().Add(-time.Minute)
	recent := l.attempts[key][:0]
	for _, at := range l.attempts[key] {
		if at.After(cutoff) {
			recent = append(recent, at)
		}
	}
	l.attempts[key] = recent
	if len(l.attempts) > 10000 {
		for k, v := range l.attempts {
			if len(v) == 0 || v[len(v)-1].Before(cutoff) {
				delete(l.attempts, k)
			}
		}
	}
	return len(recent) < 5
}

func (l *loginLimiter) failed(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.attempts[key] = append(l.attempts[key], time.Now())
}

type requestLimiter struct {
	mu    sync.Mutex
	limit int
	hits  map[string][]time.Time
}

func newRequestLimiter(limit int) *requestLimiter {
	return &requestLimiter{limit: limit, hits: map[string][]time.Time{}}
}
func (l *requestLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := time.Now().Add(-time.Minute)
	recent := l.hits[key][:0]
	for _, at := range l.hits[key] {
		if at.After(cutoff) {
			recent = append(recent, at)
		}
	}
	if len(recent) >= l.limit {
		l.hits[key] = recent
		return false
	}
	recent = append(recent, time.Now())
	l.hits[key] = recent
	if len(l.hits) > 10000 {
		for k, v := range l.hits {
			if len(v) == 0 || v[len(v)-1].Before(cutoff) {
				delete(l.hits, k)
			}
		}
	}
	return true
}

func (l *loginLimiter) success(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, key)
}
