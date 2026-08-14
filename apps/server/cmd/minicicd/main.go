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
	"syscall"
	"time"

	"github.com/charlesfeng/mini-cicd/apps/server/internal/config"
	"github.com/charlesfeng/mini-cicd/apps/server/internal/database"
	webserver "github.com/charlesfeng/mini-cicd/apps/server/internal/server"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
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
