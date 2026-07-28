package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/kepthx/gym-tracker/internal/config"
	"github.com/kepthx/gym-tracker/internal/db"
	"github.com/kepthx/gym-tracker/internal/store"
)

// importData pours an export back into the database.
//
// A backup that has never been restored from is not a backup. This subcommand makes
// restoring real, and the completeness of the export verifiable by a round-trip test.
func importData(ctx context.Context, args []string) error {
	if len(args) != 2 {
		return errors.New("использование: gymtracker import <имя-пользователя> <файл.json>")
	}
	username, path := args[0], args[1]

	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("прочитать файл выгрузки: %w", err)
	}
	var data store.Export
	if err := json.Unmarshal(raw, &data); err != nil {
		return fmt.Errorf("разобрать выгрузку: %w", err)
	}

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

	st := store.New(database)
	user, _, err := st.UserByName(ctx, username)
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("пользователь %q не заведён — сначала gymtracker adduser %s", username, username)
	}
	if err != nil {
		return err
	}

	result, err := st.Import(ctx, user.ID, &data, time.Now())
	if err != nil {
		return err
	}

	fmt.Printf("влито: программ %d, тренировок %d, подходов %d",
		result.Programs, result.Sessions, result.Sets)
	if result.Skipped > 0 {
		fmt.Printf(", пропущено %d", result.Skipped)
	}
	fmt.Println()
	return nil
}
