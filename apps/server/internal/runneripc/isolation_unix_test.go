//go:build !windows

package runneripc

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServerRunsAsUnprivilegedJobUser(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root to verify UID boundary")
	}
	base := t.TempDir()
	if err := os.Chmod(base, 0o755); err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(base, "workspace")
	socket := filepath.Join(base, "runner.sock")
	server, err := NewServer(socket, workspace, "/bin/bash", os.Getgid(), 65534, 65534, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(workspace, "p", "1", "source")
	if err = os.MkdirAll(dir, 0o770); err != nil {
		t.Fatal(err)
	}
	if err = os.Chown(filepath.Join(workspace, "p"), 65534, 65534); err != nil {
		t.Fatal(err)
	}
	if err = os.Chown(filepath.Join(workspace, "p", "1"), 65534, 65534); err != nil {
		t.Fatal(err)
	}
	if err = os.Chown(dir, 65534, 65534); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(base, "control-secret")
	if err = os.WriteFile(secret, []byte("must-not-read"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	for i := 0; i < 100; i++ {
		if _, e := os.Stat(socket); e == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	var logs []string
	err = Execute(context.Background(), socket, Request{Command: "id -u; cat " + secret, Directory: dir, TimeoutSeconds: 5}, func(line string) error { logs = append(logs, line); return nil })
	if err == nil {
		t.Fatal("job unexpectedly read control-plane secret")
	}
	if len(logs) == 0 || strings.TrimSpace(logs[0]) != "65534" {
		t.Fatalf("job did not run as UID 65534: %#v", logs)
	}
	if strings.Contains(strings.Join(logs, "\n"), "must-not-read") {
		t.Fatal("control-plane secret leaked")
	}
	cancel()
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runner did not stop")
	}
}
