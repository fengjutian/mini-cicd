package runneripc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
)

func Execute(ctx context.Context, endpoint string, request Request, onLog func(string) error) error {
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "unix", endpoint)
	if err != nil {
		return fmt.Errorf("connect isolated runner: %w", err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if err = json.NewEncoder(conn).Encode(request); err != nil {
		return err
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()
	defer close(done)
	scan := bufio.NewScanner(conn)
	scan.Buffer(make([]byte, 64*1024), 1024*1024)
	for scan.Scan() {
		var response Response
		if json.Unmarshal(scan.Bytes(), &response) != nil {
			continue
		}
		switch response.Type {
		case "log":
			if err = onLog(response.Message); err != nil {
				return err
			}
		case "error":
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errors.New(response.Message)
		case "exit":
			if response.ExitCode != nil && *response.ExitCode != 0 {
				return fmt.Errorf("step exited with code %d", *response.ExitCode)
			}
			return nil
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err = scan.Err(); err != nil {
		return err
	}
	return errors.New("isolated runner disconnected without an exit result")
}
