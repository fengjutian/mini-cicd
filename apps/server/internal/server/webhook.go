package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

type webhookPayload struct {
	Ref         string `json:"ref"`
	After       string `json:"after"`
	CheckoutSHA string `json:"checkout_sha"`
	Repository  struct {
		CloneURL string `json:"clone_url"`
		SSHURL   string `json:"ssh_url"`
	} `json:"repository"`
	Project struct {
		HTTPURL string `json:"git_http_url"`
		SSHURL  string `json:"git_ssh_url"`
	} `json:"project"`
}

func (s *Server) webhook(w http.ResponseWriter, r *http.Request) {
	projectID, provider := r.PathValue("projectID"), r.PathValue("provider")
	var configuredProvider, repo, branch string
	var enabled bool
	var cipher []byte
	if err := s.db.QueryRowContext(r.Context(), `SELECT webhook_provider,repository_url,branch,auto_deploy,webhook_secret_cipher FROM projects WHERE id=? AND archived_at IS NULL`, projectID).Scan(&configuredProvider, &repo, &branch, &enabled, &cipher); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", "Webhook was not found.")
		} else {
			writeError(w, http.StatusInternalServerError, "internal_error", "Could not process webhook.")
		}
		return
	}
	if !enabled || provider != configuredProvider || len(cipher) == 0 {
		writeError(w, http.StatusNotFound, "not_found", "Webhook was not found.")
		return
	}
	secretValue, err := s.box.Decrypt(cipher, "project:"+projectID+":webhook-secret")
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "secret_unavailable", "Webhook secret is unavailable.")
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Could not read webhook body.")
		return
	}
	event, delivery := webhookHeaders(provider, r)
	if event != "push" || delivery == "" || len(delivery) > 200 {
		writeError(w, http.StatusBadRequest, "invalid_webhook", "Push event and delivery ID are required.")
		return
	}
	if !verifyWebhook(provider, r, body, secretValue) {
		writeError(w, http.StatusUnauthorized, "invalid_signature", "Webhook signature is invalid.")
		return
	}
	var payload webhookPayload
	if json.Unmarshal(body, &payload) != nil {
		writeError(w, http.StatusBadRequest, "invalid_payload", "Webhook payload is invalid.")
		return
	}
	sha := payload.After
	if provider == "gitlab" && payload.CheckoutSHA != "" {
		sha = payload.CheckoutSHA
	}
	payloadRepo := payload.Repository.CloneURL
	if payloadRepo == "" {
		payloadRepo = payload.Project.HTTPURL
	}
	repoMatches := sameRepository(repo, payloadRepo) || sameRepository(repo, payload.Repository.SSHURL) || sameRepository(repo, payload.Project.SSHURL)
	reason := ""
	if payload.Ref != "refs/heads/"+branch {
		reason = "branch does not match project"
	} else if !repoMatches {
		reason = "repository does not match project"
	} else if len(sha) != 40 || strings.Trim(sha, "0") == "" {
		reason = "commit SHA is invalid or branch was deleted"
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	accepted := reason == ""
	res, err := s.db.ExecContext(r.Context(), `INSERT INTO webhook_deliveries(project_id,provider,delivery_id,event_type,accepted,rejection_reason,commit_sha,received_at) VALUES(?,?,?,?,?,?,?,?)`, projectID, provider, delivery, "push", accepted, reason, sha, now)
	if err != nil {
		if isUniqueError(err) {
			writeJSON(w, http.StatusAccepted, map[string]any{"duplicate": true})
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "Could not persist webhook.")
		return
	}
	deliveryID, _ := res.LastInsertId()
	if !accepted {
		writeJSON(w, http.StatusAccepted, map[string]any{"accepted": false, "reason": reason})
		return
	}
	s.startWebhook(deliveryID, projectID, sha)
	writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true})
}

func webhookHeaders(provider string, r *http.Request) (string, string) {
	switch provider {
	case "github":
		return strings.ToLower(r.Header.Get("X-GitHub-Event")), r.Header.Get("X-GitHub-Delivery")
	case "gitlab":
		event := strings.ToLower(r.Header.Get("X-Gitlab-Event"))
		if event == "push hook" {
			event = "push"
		}
		return event, r.Header.Get("X-Gitlab-Event-UUID")
	case "gitea":
		return strings.ToLower(r.Header.Get("X-Gitea-Event")), r.Header.Get("X-Gitea-Delivery")
	}
	return "", ""
}

func verifyWebhook(provider string, r *http.Request, body, secret []byte) bool {
	if provider == "gitlab" {
		provided := []byte(r.Header.Get("X-Gitlab-Token"))
		return len(provided) == len(secret) && subtle.ConstantTimeCompare(provided, secret) == 1
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(body)
	header, prefix := "X-Gitea-Signature", ""
	if provider == "github" {
		header, prefix = "X-Hub-Signature-256", "sha256="
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(r.Header.Get(header), prefix))
	return err == nil && hmac.Equal(decoded, mac.Sum(nil))
}

func sameRepository(a, b string) bool {
	normalize := func(v string) string {
		return strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(v), "/"), ".git")
	}
	return b != "" && strings.EqualFold(normalize(a), normalize(b))
}

func (s *Server) startWebhook(deliveryID int64, projectID, sha string) {
	s.webhookWG.Add(1)
	go func() {
		defer s.webhookWG.Done()
		d, err := s.deps.CreateAtCommit(s.webhookCtx, projectID, "webhook", sha)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			_, _ = s.db.Exec(`UPDATE webhook_deliveries SET accepted=0,rejection_reason=? WHERE id=?`, err.Error(), deliveryID)
			return
		}
		_, _ = s.db.Exec(`UPDATE webhook_deliveries SET deployment_id=? WHERE id=?`, d.ID, deliveryID)
		s.runner.Wake()
	}()
}

func (s *Server) recoverWebhooks() {
	rows, err := s.db.Query(`SELECT id,project_id,commit_sha FROM webhook_deliveries WHERE accepted=1 AND deployment_id IS NULL`)
	if err != nil {
		return
	}
	var pending []struct {
		id           int64
		project, sha string
	}
	for rows.Next() {
		var x struct {
			id           int64
			project, sha string
		}
		if rows.Scan(&x.id, &x.project, &x.sha) == nil {
			pending = append(pending, x)
		}
	}
	_ = rows.Close()
	for _, x := range pending {
		s.startWebhook(x.id, x.project, x.sha)
	}
}
