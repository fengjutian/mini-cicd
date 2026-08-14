package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/charlesfeng/mini-cicd/apps/server/internal/config"
	"github.com/charlesfeng/mini-cicd/apps/server/internal/database"
	"github.com/charlesfeng/mini-cicd/apps/server/internal/runneripc"
	webserver "github.com/charlesfeng/mini-cicd/apps/server/internal/server"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if len(os.Args) > 1 && os.Args[1] == "runner" {
		if err := runnerCommand(logger); err != nil {
			logger.Error("runner stopped", "error", err)
			os.Exit(1)
		}
		return
	}
	cfg, err := config.Load()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	if len(os.Args) > 1 && (os.Args[1] == "backup" || os.Args[1] == "restore") {
		if err := databaseCommand(cfg, os.Args[1], os.Args[2:]); err != nil {
			logger.Error("database command failed", "error", err)
			os.Exit(1)
		}
		return
	}

	db, err := database.Open(cfg.DatabasePath)
	if err != nil {
		logger.Error("open database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := database.Migrate(db); err != nil {
		logger.Error("migrate database", "error", err)
		os.Exit(1)
	}

	handler, err := webserver.New(db, cfg, logger)
	if err != nil {
		logger.Error("initialize server", "error", err)
		os.Exit(1)
	}
	defer handler.Close()
	httpServer := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		logger.Info("mini-ci-cd started", "address", cfg.ListenAddr, "data_dir", cfg.DataDir)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server stopped", "error", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
	}
}

func runnerCommand(logger *slog.Logger) error {
	socket := os.Getenv("MINICICD_RUNNER_SOCKET")
	root := os.Getenv("MINICICD_RUNNER_WORKSPACE_DIR")
	shell := os.Getenv("MINICICD_SHELL")
	socketGID, e1 := strconv.Atoi(os.Getenv("MINICICD_RUNNER_SOCKET_GID"))
	jobUID, e2 := strconv.Atoi(os.Getenv("MINICICD_RUNNER_JOB_UID"))
	jobGID, e3 := strconv.Atoi(os.Getenv("MINICICD_RUNNER_JOB_GID"))
	if socket == "" || root == "" || e1 != nil || e2 != nil || e3 != nil {
		return errors.New("runner requires socket, workspace and numeric socket/job UID/GID configuration")
	}
	if shell == "" {
		shell = "/bin/bash"
	}
	server, err := runneripc.NewServer(socket, root, shell, socketGID, jobUID, jobGID, logger)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	logger.Info("isolated runner started", "socket", socket, "workspace", root, "job_uid", jobUID)
	return server.Serve(ctx)
}

func databaseCommand(cfg config.Config, command string, args []string) error {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	if command == "backup" {
		output := flags.String("output", "", "backup file path")
		if err := flags.Parse(args); err != nil {
			return err
		}
		if *output == "" {
			return errors.New("backup requires --output")
		}
		db, err := database.Open(cfg.DatabasePath)
		if err != nil {
			return err
		}
		defer db.Close()
		if err = database.Backup(db, *output); err != nil {
			return err
		}
		fmt.Printf("backup created: %s\n", *output)
		return nil
	}
	input := flags.String("input", "", "backup file path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *input == "" {
		return errors.New("restore requires --input")
	}
	previous, err := database.Restore(*input, cfg.DatabasePath)
	if err != nil {
		return err
	}
	if previous != "" {
		fmt.Printf("restore complete; previous database: %s\n", previous)
	} else {
		fmt.Println("restore complete")
	}
	return nil
}
