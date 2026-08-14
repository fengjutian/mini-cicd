package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

type Config struct {
	ListenAddr          string
	DataDir             string
	DatabasePath        string
	SessionTTL          time.Duration
	SecureCookies       bool
	TrustProxy          bool
	MasterKey           []byte
	GlobalParallel      int
	Shell               string
	LogMaxBytes         int64
	CancelGrace         time.Duration
	CleanupInterval     time.Duration
	WorkspaceRetention  time.Duration
	LogRetention        time.Duration
	DeploymentRetention int
	BackupInterval      time.Duration
	BackupRetention     int
}

func Load() (Config, error) {
	dataDir := env("MINICICD_DATA_DIR", "./data")
	absDataDir, err := filepath.Abs(dataDir)
	if err != nil {
		return Config{}, fmt.Errorf("resolve data directory: %w", err)
	}
	if err := os.MkdirAll(absDataDir, 0o700); err != nil {
		return Config{}, fmt.Errorf("create data directory: %w", err)
	}

	sessionTTL, err := time.ParseDuration(env("MINICICD_SESSION_TTL", "24h"))
	if err != nil || sessionTTL <= 0 {
		return Config{}, fmt.Errorf("MINICICD_SESSION_TTL must be a positive duration")
	}
	secure, err := strconv.ParseBool(env("MINICICD_SECURE_COOKIES", "false"))
	if err != nil {
		return Config{}, fmt.Errorf("MINICICD_SECURE_COOKIES must be true or false")
	}
	trustProxy, err := strconv.ParseBool(env("MINICICD_TRUST_PROXY", "false"))
	if err != nil {
		return Config{}, fmt.Errorf("MINICICD_TRUST_PROXY must be true or false")
	}
	parallel, err := strconv.Atoi(env("MINICICD_GLOBAL_PARALLEL", "2"))
	if err != nil || parallel < 1 {
		return Config{}, fmt.Errorf("MINICICD_GLOBAL_PARALLEL must be a positive integer")
	}
	logMax, err := strconv.ParseInt(env("MINICICD_LOG_MAX_BYTES", "10485760"), 10, 64)
	if err != nil || logMax < 1024 {
		return Config{}, fmt.Errorf("MINICICD_LOG_MAX_BYTES must be at least 1024")
	}
	grace, err := time.ParseDuration(env("MINICICD_CANCEL_GRACE", "10s"))
	if err != nil || grace <= 0 {
		return Config{}, fmt.Errorf("MINICICD_CANCEL_GRACE must be positive")
	}
	cleanupInterval, err := time.ParseDuration(env("MINICICD_CLEANUP_INTERVAL", "1h"))
	if err != nil || cleanupInterval <= 0 {
		return Config{}, fmt.Errorf("MINICICD_CLEANUP_INTERVAL must be positive")
	}
	workspaceRetention, err := time.ParseDuration(env("MINICICD_WORKSPACE_RETENTION", "24h"))
	if err != nil || workspaceRetention <= 0 {
		return Config{}, fmt.Errorf("MINICICD_WORKSPACE_RETENTION must be positive")
	}
	logRetention, err := time.ParseDuration(env("MINICICD_LOG_RETENTION", "720h"))
	if err != nil || logRetention <= 0 {
		return Config{}, fmt.Errorf("MINICICD_LOG_RETENTION must be positive")
	}
	deploymentRetention, err := strconv.Atoi(env("MINICICD_DEPLOYMENT_RETENTION", "100"))
	if err != nil || deploymentRetention < 1 {
		return Config{}, fmt.Errorf("MINICICD_DEPLOYMENT_RETENTION must be positive")
	}
	backupInterval, err := time.ParseDuration(env("MINICICD_BACKUP_INTERVAL", "24h"))
	if err != nil || backupInterval <= 0 {
		return Config{}, fmt.Errorf("MINICICD_BACKUP_INTERVAL must be positive")
	}
	backupRetention, err := strconv.Atoi(env("MINICICD_BACKUP_RETENTION", "7"))
	if err != nil || backupRetention < 1 {
		return Config{}, fmt.Errorf("MINICICD_BACKUP_RETENTION must be positive")
	}
	var masterKey []byte
	if encoded := os.Getenv("MINICICD_MASTER_KEY"); encoded != "" {
		masterKey, err = base64.RawStdEncoding.DecodeString(encoded)
		if err != nil || len(masterKey) != 32 {
			return Config{}, fmt.Errorf("MINICICD_MASTER_KEY must be an unpadded base64-encoded 32-byte key")
		}
	}

	return Config{
		ListenAddr:          env("MINICICD_LISTEN_ADDR", "127.0.0.1:8080"),
		DataDir:             absDataDir,
		DatabasePath:        filepath.Join(absDataDir, "mini-cicd.db"),
		SessionTTL:          sessionTTL,
		SecureCookies:       secure,
		TrustProxy:          trustProxy,
		MasterKey:           masterKey,
		GlobalParallel:      parallel,
		Shell:               env("MINICICD_SHELL", defaultShell()),
		LogMaxBytes:         logMax,
		CancelGrace:         grace,
		CleanupInterval:     cleanupInterval,
		WorkspaceRetention:  workspaceRetention,
		LogRetention:        logRetention,
		DeploymentRetention: deploymentRetention,
		BackupInterval:      backupInterval,
		BackupRetention:     backupRetention,
	}, nil
}

func defaultShell() string {
	if os.PathSeparator == '\\' {
		return "powershell.exe"
	}
	return "/bin/bash"
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
