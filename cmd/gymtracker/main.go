// Command gymtracker is the gym tracker server.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"syscall"
	"time"

	"github.com/kepthx/gym-tracker/internal/api"
	"github.com/kepthx/gym-tracker/internal/backup"
	"github.com/kepthx/gym-tracker/internal/config"
	"github.com/kepthx/gym-tracker/internal/db"
	"github.com/kepthx/gym-tracker/internal/program"
	"github.com/kepthx/gym-tracker/internal/server"
	"github.com/kepthx/gym-tracker/internal/store"
	"github.com/kepthx/gym-tracker/internal/web"
)

var usernameRe = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,32}$`)

// version is substituted at build time: go build -ldflags "-X main.version=..."
var version = "dev"

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch {
	case len(os.Args) > 1 && os.Args[1] == "adduser":
		err = addUser(ctx, os.Args[2:])
	case len(os.Args) > 1 && os.Args[1] == "import":
		err = importData(ctx, os.Args[2:])
	case len(os.Args) > 1 && os.Args[1] == "version":
		fmt.Println(version)
	case len(os.Args) > 1:
		err = fmt.Errorf("неизвестная команда %q; доступно: adduser, import, version", os.Args[1])
	default:
		err = serve(ctx)
	}

	if err != nil {
		log.Error("остановлено", "ошибка", err)
		os.Exit(1)
	}
}

func serve(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	database, err := db.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer database.Close()

	if err := database.Migrate(ctx); err != nil {
		return err
	}

	// A broken program stops startup: handing someone a program other than the one they
	// train by is worse than not coming up at all.
	snapshots, err := program.LoadDir(cfg.ProgramsDir)
	if err != nil {
		return err
	}

	st := store.New(database)
	report, err := st.SyncPrograms(ctx, snapshots)
	if err != nil {
		return err
	}
	logProgramReport(report)

	backups := backup.New(database, cfg.BackupDir)
	go backups.Loop(ctx)
	st.SetOnSessionFinished(func() { backups.MaybeRun(ctx, time.Now()) })

	mux := http.NewServeMux()
	api.New(api.Deps{
		Store:     st,
		TokenTTL:  cfg.TokenTTL,
		DebugAuth: cfg.DebugAuth,
		Backups:   backups,
		DBPath:    cfg.DBPath,
		Version:   version,
		ReloadPrograms: func(ctx context.Context) (map[string]string, error) {
			// Changing a program means editing a file plus this call. No code change needed.
			fresh, err := program.LoadDir(cfg.ProgramsDir)
			if err != nil {
				return nil, err
			}
			report, err := st.SyncPrograms(ctx, fresh)
			if err != nil {
				return nil, err
			}
			logProgramReport(report)
			return report.Attached, nil
		},
	}).Routes(mux)

	// The frontend is embedded in the binary: deployment is copying one file.
	assets, err := web.Handler()
	if err != nil {
		return err
	}
	mux.Handle("GET /", assets)

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"ok":true}`)
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := database.R.PingContext(r.Context()); err != nil {
			http.Error(w, `{"ok":false}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"ok":true}`)
	})

	go pruneLoop(ctx, st)

	slog.Info("запускаюсь", "версия", version, "база", cfg.DBPath)
	return server.Run(ctx, server.Config{
		Addr:    cfg.Addr,
		Domain:  cfg.Domain,
		CertDir: cfg.CertDir,
		Email:   cfg.ACMEEmail,
		Staging: cfg.ACMEStaging,
	}, api.Wrap(mux))
}

// pruneLoop clears out expired tokens, old login attempts and operation ledger rows.
// No separate service is set up for this: the process already holds a database connection.
func pruneLoop(ctx context.Context, st *store.Store) {
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()

	for {
		if err := st.PruneAuth(ctx, time.Now()); err != nil && ctx.Err() == nil {
			slog.Error("уборка не удалась", "ошибка", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func logProgramReport(r *store.ProgramSyncReport) {
	for username, hash := range r.Attached {
		slog.Info("программа привязана", "пользователь", username, "хеш", hash[:12])
	}
	if len(r.UnknownUsers) > 0 {
		slog.Warn("файлы программ без пользователя", "имена", r.UnknownUsers)
	}
	if len(r.UsersWithoutProgram) > 0 {
		slog.Warn("пользователи без программы", "имена", r.UsersWithoutProgram)
	}
}
