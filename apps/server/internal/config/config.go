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
	ListenAddr     string
	DataDir        string
	DatabasePath   string
	SessionTTL     time.Duration
	SecureCookies  bool
	TrustProxy     bool
	MasterKey      []byte
	GlobalParallel int
	Shell          string
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
	var masterKey []byte
	if encoded := os.Getenv("MINICICD_MASTER_KEY"); encoded != "" {
		masterKey, err = base64.RawStdEncoding.DecodeString(encoded)
		if err != nil || len(masterKey) != 32 {
			return Config{}, fmt.Errorf("MINICICD_MASTER_KEY must be an unpadded base64-encoded 32-byte key")
		}
	}

	return Config{
		ListenAddr:     env("MINICICD_LISTEN_ADDR", "127.0.0.1:8080"),
		DataDir:        absDataDir,
		DatabasePath:   filepath.Join(absDataDir, "mini-cicd.db"),
		SessionTTL:     sessionTTL,
		SecureCookies:  secure,
		TrustProxy:     trustProxy,
		MasterKey:      masterKey,
		GlobalParallel: parallel,
		Shell:          env("MINICICD_SHELL", defaultShell()),
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
