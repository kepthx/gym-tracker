// Package config gathers settings from environment variables.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

type Config struct {
	// Addr is the address of the plain HTTP listener. Used in development and when no
	// domain is set.
	Addr string
	// DBPath is the database file. The directory is created at startup.
	DBPath string
	// ProgramsDir is the programs/ directory, with one file per user.
	ProgramsDir string
	// GuidesPath is the exercise technique guides file. Kept out of the program files on
	// purpose: a program is hashed and history hangs off that hash, guides are prose.
	GuidesPath string
	// BackupDir is where the copies made by VACUUM INTO are placed.
	BackupDir string
	// TokenTTL is the lifetime of a login token. The password cannot be asked for at the
	// gym, hence months.
	TokenTTL time.Duration
	// DebugAuth enables the authentication stub via the X-Debug-User header.
	// For developing the sync core only; off by default.
	DebugAuth bool

	// Domain switches on production mode: a listener on 443 with automatic certificate
	// issuance. An empty value means plain HTTP on Addr.
	Domain string
	// CertDir is the persistent certificate cache. Without it every restart orders a new
	// certificate and runs into Let's Encrypt's weekly limit.
	CertDir string
	// ACMEEmail is the address for certificate expiry notices.
	ACMEEmail string
	// ACMEStaging points the first order at the staging directory: it protects against a
	// week-long block while DNS, firewall and systemd are being set up.
	ACMEStaging bool
}

func Load() (*Config, error) {
	c := &Config{
		Addr:        env("GYM_ADDR", ":8080"),
		DBPath:      env("GYM_DB", filepath.Join("data", "gymtracker.db")),
		ProgramsDir: env("GYM_PROGRAMS", "programs"),
		GuidesPath:  env("GYM_GUIDES", filepath.Join("guides", "exercises.json")),
		BackupDir:   env("GYM_BACKUP_DIR", filepath.Join("data", "backups")),
		TokenTTL:    180 * 24 * time.Hour,
		DebugAuth:   os.Getenv("GYM_DEBUG_AUTH") == "1",

		Domain:      os.Getenv("GYM_DOMAIN"),
		CertDir:     env("GYM_CERT_DIR", filepath.Join("data", "autocert")),
		ACMEEmail:   os.Getenv("GYM_ACME_EMAIL"),
		ACMEStaging: os.Getenv("GYM_ACME_STAGING") == "1",
	}

	if raw := os.Getenv("GYM_TOKEN_TTL_DAYS"); raw != "" {
		days, err := strconv.Atoi(raw)
		if err != nil || days < 1 {
			return nil, fmt.Errorf("GYM_TOKEN_TTL_DAYS=%q: нужно целое число дней >= 1", raw)
		}
		c.TokenTTL = time.Duration(days) * 24 * time.Hour
	}

	return c, nil
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
