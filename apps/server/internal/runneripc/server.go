package runneripc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/charlesfeng/mini-cicd/apps/server/internal/procenv"
)

type Server struct {
	socket, root, shell       string
	socketGID, jobUID, jobGID int
	logger                    *slog.Logger
	serial                    sync.Mutex
}

func NewServer(socket, root, shell string, socketGID, jobUID, jobGID int, logger *slog.Logger) (*Server, error) {
	if socketGID < 0 || jobUID < 1 || jobGID < 1 {
		return nil, errors.New("runner requires valid socket GID and unprivileged job UID/GID")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(abs, 0o770); err != nil {
		return nil, err
	}
	if runtime.GOOS != "windows" {
		if err = os.Chown(abs, jobUID, jobGID); err != nil {
			return nil, err
		}
		if err = os.Chmod(abs, 0o2770); err != nil {
			return nil, err
		}
	}
	return &Server{socket: socket, root: abs, shell: shell, socketGID: socketGID, jobUID: jobUID, jobGID: jobGID, logger: logger}, nil
}
func (s *Server) Serve(ctx context.Context) error {
	if runtime.GOOS == "windows" {
		return errors.New("isolated runner daemon currently requires Linux or another Unix platform")
	}
	if err := os.MkdirAll(filepath.Dir(s.socket), 0o750); err != nil {
		return err
	}
	_ = os.Remove(s.socket)
	listener, err := net.Listen("unix", s.socket)
	if err != nil {
		return err
	}
	defer func() { listener.Close(); os.Remove(s.socket) }()
	if err = os.Chown(s.socket, 0, s.socketGID); err != nil {
		return err
	}
	if err = os.Chmod(s.socket, 0o660); err != nil {
		return err
	}
	go func() { <-ctx.Done(); listener.Close() }()
	for {
		conn, e := listener.Accept()
		if e != nil {
			if ctx.Err() != nil {
				return nil
			}
			return e
		}
		go s.handle(ctx, conn)
	}
}
func (s *Server) handle(parent context.Context, conn net.Conn) {
	defer conn.Close()
	var req Request
	if err := json.NewDecoder(io.LimitReader(conn, 2<<20)).Decode(&req); err != nil {
		s.send(conn, Response{Type: "error", Message: "invalid runner request"})
		return
	}
	dir, err := s.resolve(req.Directory)
	if err != nil {
		s.send(conn, Response{Type: "error", Message: err.Error()})
		return
	}
	if strings.TrimSpace(req.Command) == "" || req.TimeoutSeconds < 1 {
		s.send(conn, Response{Type: "error", Message: "invalid command or timeout"})
		return
	}
	if len(req.Environment) > 512 {
		s.send(conn, Response{Type: "error", Message: "too many environment variables"})
		return
	}
	for _, item := range req.Environment {
		key, _, ok := strings.Cut(item, "=")
		if !ok || key == "" || strings.ContainsRune(item, 0) || key == "MINICICD_MASTER_KEY" {
			s.send(conn, Response{Type: "error", Message: "unsafe environment variable"})
			return
		}
	}
	s.serial.Lock()
	defer s.serial.Unlock()
	ctx, cancel := context.WithTimeout(parent, time.Duration(req.TimeoutSeconds)*time.Second)
	defer cancel()
	go func() { one := make([]byte, 1); _, _ = conn.Read(one); cancel() }()
	args := []string{"-lc", req.Command}
	cmd := exec.Command(s.shell, args...)
	cmd.Dir = dir
	cmd.Env = append(procenv.Safe(), req.Environment...)
	configureProcess(cmd, s.jobUID, s.jobGID)
	pipeR, pipeW := io.Pipe()
	cmd.Stdout, cmd.Stderr = pipeW, pipeW
	if err = cmd.Start(); err != nil {
		s.send(conn, Response{Type: "error", Message: err.Error()})
		return
	}
	logsDone := make(chan struct{})
	go func() {
		defer close(logsDone)
		scan := bufio.NewScanner(pipeR)
		scan.Buffer(make([]byte, 64*1024), 1024*1024)
		for scan.Scan() {
			if s.send(conn, Response{Type: "log", Message: scan.Text()}) != nil {
				cancel()
				return
			}
		}
	}()
	wait := make(chan error, 1)
	go func() { wait <- cmd.Wait(); pipeW.Close() }()
	select {
	case err = <-wait:
		cleanupProcess(cmd.Process)
	case <-ctx.Done():
		stopProcess(cmd.Process)
		err = <-wait
	}
	<-logsDone
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		if ctx.Err() != nil {
			s.send(conn, Response{Type: "error", Message: ctx.Err().Error()})
			return
		}
		s.send(conn, Response{Type: "error", Message: err.Error()})
		return
	}
	s.send(conn, Response{Type: "exit", ExitCode: &code})
}
func (s *Server) send(conn net.Conn, value Response) error {
	return json.NewEncoder(conn).Encode(value)
}
func (s *Server) resolve(input string) (string, error) {
	abs, err := filepath.Abs(input)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(s.root, resolved)
	if err != nil || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", errors.New("runner directory escapes workspace root")
	}
	return resolved, nil
}
