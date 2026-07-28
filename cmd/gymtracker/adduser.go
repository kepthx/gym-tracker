package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/kepthx/gym-tracker/internal/auth"
	"github.com/kepthx/gym-tracker/internal/config"
	"github.com/kepthx/gym-tracker/internal/db"
	"github.com/kepthx/gym-tracker/internal/program"
	"github.com/kepthx/gym-tracker/internal/store"
)

// addUser creates a user from the command line.
//
// There is deliberately no web registration: the app is personal, there are two users at
// most, and an open sign-up form on a public address is extra attack surface.
func addUser(ctx context.Context, args []string) error {
	isAdmin := false
	var username, displayName string

	for _, arg := range args {
		switch {
		case arg == "--admin":
			isAdmin = true
		case strings.HasPrefix(arg, "--name="):
			displayName = strings.TrimPrefix(arg, "--name=")
		case strings.HasPrefix(arg, "-"):
			return fmt.Errorf("неизвестный флаг %q", arg)
		case username == "":
			username = arg
		default:
			return fmt.Errorf("лишний аргумент %q", arg)
		}
	}

	if username == "" {
		return errors.New("использование: gymtracker adduser <имя> [--admin] [--name=Отображаемое имя]")
	}
	if !usernameRe.MatchString(username) {
		return fmt.Errorf("имя %q: допустимы латинские буквы, цифры, дефис и подчёркивание", username)
	}
	if displayName == "" {
		displayName = username
	}

	password, err := readPassword()
	if err != nil {
		return err
	}
	if len([]rune(password)) < 8 {
		return errors.New("пароль короче 8 символов")
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
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
	id, err := st.CreateUser(ctx, username, displayName, hash, isAdmin)
	if errors.Is(err, store.ErrUserExists) {
		return fmt.Errorf("пользователь %q уже заведён", username)
	}
	if err != nil {
		return err
	}

	// The program is attached right away if the file already exists: otherwise the user
	// would log in and see "no program set" until the next restart.
	snapshots, err := program.LoadDir(cfg.ProgramsDir)
	if err != nil {
		return err
	}
	report, err := st.SyncPrograms(ctx, snapshots)
	if err != nil {
		return err
	}

	fmt.Printf("пользователь %q заведён, id=%d\n", username, id)
	if hash, ok := report.Attached[username]; ok {
		fmt.Printf("программа привязана: %s\n", hash[:12])
	} else {
		fmt.Printf("программы нет — создайте %s/%s.json и перезапустите сервис\n", cfg.ProgramsDir, username)
	}
	return nil
}

// readPassword reads the password from standard input.
//
// The password deliberately does not come from command-line arguments: from there it would
// end up in the shell history and in ps output.
func readPassword() (string, error) {
	fmt.Fprint(os.Stderr, "Пароль: ")
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("прочитать пароль: %w", err)
	}
	fmt.Fprintln(os.Stderr)
	return strings.TrimRight(line, "\r\n"), nil
}
