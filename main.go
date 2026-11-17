// Command tugas-submission adalah server web untuk mengumpulkan tugas ke repository GitHub.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lendradev/tugas-submission/internal/auth"
	"github.com/lendradev/tugas-submission/internal/config"
	"github.com/lendradev/tugas-submission/internal/giteaimpl"
	"github.com/lendradev/tugas-submission/internal/github"
	"github.com/lendradev/tugas-submission/internal/store"
	syncer "github.com/lendradev/tugas-submission/internal/sync"
	"github.com/lendradev/tugas-submission/internal/web"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if err := run(log); err != nil {
		log.Error("server berhenti", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg := config.Load()

	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.StagingDir(), 0o750); err != nil {
		return err
	}

	st, err := store.Open(cfg.DBPath())
	if err != nil {
		return err
	}
	defer st.Close()

	if err := seedAdmin(st, cfg); err != nil {
		return err
	}

	var pusher syncer.Pusher
	switch {
	case cfg.GiteaReady():
		// Mode Gitea dipilih eksplisit (untuk pengujian lokal).
		pusher = giteaimpl.New(cfg.RepoOwner, cfg.RepoName, cfg.GitHubTok, cfg.Branch, cfg.GitHubAPI)
		log.Info("memakai pusher Gitea (Contents API)", "api", cfg.GitHubAPI)
	case cfg.GitHubReady():
		pusher = github.New(cfg.RepoOwner, cfg.RepoName, cfg.GitHubTok, cfg.Branch, cfg.GitHubAPI)
	default:
		log.Warn("konfigurasi GitHub belum lengkap; sinkronisasi dinonaktifkan",
			"butuh", "TS_REPO_OWNER, TS_REPO_NAME, TS_GITHUB_TOKEN")
	}

	a := auth.New(st, cfg.SessionTTL)
	sc := syncer.New(st, pusher, log)
	scheduler := syncer.NewScheduler(sc, st, cfg.StagingDir(), log, 15*time.Second)

	srv, err := web.New(cfg, st, a, pusher, scheduler, log)
	if err != nil {
		return err
	}
	if err := srv.LoadSettings(); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	scheduler.Start(ctx)
	go housekeeping(ctx, st, scheduler, log)

	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       5 * time.Minute,
		WriteTimeout:      5 * time.Minute,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("server berjalan", "addr", cfg.Addr, "repo", cfg.RepoOwner+"/"+cfg.RepoName)
		if cfg.TLSCert != "" && cfg.TLSKey != "" {
			errCh <- httpSrv.ListenAndServeTLS(cfg.TLSCert, cfg.TLSKey)
			return
		}
		errCh <- httpSrv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		log.Info("mematikan server")
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return httpSrv.Shutdown(shutdownCtx)
}

// seedAdmin membuat akun admin pertama bila belum ada dan password admin
// diset lewat environment.
func seedAdmin(st *store.Store, cfg config.Config) error {
	if cfg.AdminPass == "" {
		return nil
	}
	users, err := st.ListUsers()
	if err != nil {
		return err
	}
	for _, u := range users {
		if u.Role == "admin" {
			return nil
		}
	}
	hash, err := auth.HashPassword(cfg.AdminPass)
	if err != nil {
		return err
	}
	if _, err := st.CreateUser(cfg.AdminUser, hash, "Administrator", "admin"); err != nil {
		return err
	}
	return nil
}

// housekeeping membersihkan sesi kedaluwarsa dan log perubahan lama.
func housekeeping(ctx context.Context, st *store.Store, scheduler *syncer.Scheduler, log *slog.Logger) {
	t := time.NewTicker(30 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := st.PurgeExpiredSessions(); err != nil {
				log.Error("bersihkan sesi", "err", err)
			}
			if err := st.PurgeChangeLogOlderThan(24 * time.Hour); err != nil {
				log.Error("bersihkan log perubahan", "err", err)
			}
		}
	}
}
