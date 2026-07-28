// Package server brings up the HTTP listeners: a plain one for development and a TLS one
// with automatic certificate issuance for production.
package server

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"time"

	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"
)

// The timeouts a reverse proxy used to provide for free. http.Server's defaults mean
// "no limit", and that means a slow client holding a connection for as long as it likes.
func newServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 16,
	}
}

type Config struct {
	// Addr is the address of the plain listener. Used when no domain is set.
	Addr string
	// Domain is the domain name for the certificate. An empty value means running without TLS.
	Domain string
	// CertDir is the certificate cache directory. Required: without it every restart orders
	// a new certificate.
	CertDir string
	// Email for certificate expiry notices.
	Email string
	// Staging points the order at Let's Encrypt's staging directory.
	Staging bool
}

// Run brings the service up and keeps running until the context is cancelled.
func Run(ctx context.Context, cfg Config, handler http.Handler) error {
	if cfg.Domain == "" {
		return runPlain(ctx, cfg.Addr, handler)
	}
	return runTLS(ctx, cfg, handler)
}

func runPlain(ctx context.Context, addr string, handler http.Handler) error {
	srv := newServer(addr, handler)
	slog.Info("слушаю без TLS", "адрес", addr)
	return serve(ctx, srv, func() error { return srv.ListenAndServe() })
}

// runTLS brings up :443 with automatic Let's Encrypt certificate issuance.
//
// nginx and certbot are not needed here: the binary obtains the certificate itself over
// tls-alpn-01, entirely on port 443. Port 80 only carries the redirect and a fallback
// challenge method.
func runTLS(ctx context.Context, cfg Config, handler http.Handler) error {
	if cfg.CertDir == "" {
		return errors.New("не задан каталог кэша сертификатов")
	}
	// Without a persistent cache every restart orders a new certificate, and Let's Encrypt
	// allows five identical certificates per week. Past that comes a week-long block.
	if err := os.MkdirAll(cfg.CertDir, 0o700); err != nil {
		return err
	}

	manager := &autocert.Manager{
		Prompt: autocert.AcceptTOS,
		Cache:  autocert.DirCache(cfg.CertDir),
		// The whitelist is mandatory. Without it a certificate would be ordered for any name
		// anyone points at this address, and the rate limits would burn through in an hour.
		HostPolicy: autocert.HostWhitelist(cfg.Domain),
		Email:      cfg.Email,
	}
	if cfg.Staging {
		manager.Client = &acme.Client{DirectoryURL: "https://acme-staging-v02.api.letsencrypt.org/directory"}
		slog.Warn("сертификаты заказываются в тестовом каталоге Let's Encrypt")
	}

	tlsSrv := newServer(":443", handler)
	tlsSrv.TLSConfig = manager.TLSConfig()

	// Port 80: the redirect to https and a fallback domain ownership challenge.
	httpSrv := newServer(":80", manager.HTTPHandler(nil))

	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("слушатель :80 остановлен", "ошибка", err)
		}
	}()
	go warmUp(ctx, manager, cfg.Domain)

	slog.Info("слушаю с TLS", "домен", cfg.Domain, "кэш сертификатов", cfg.CertDir)
	return serve(ctx, tlsSrv, func() error {
		defer httpSrv.Close()
		return tlsSrv.ListenAndServeTLS("", "")
	})
}

// warmUp obtains and renews the certificate without waiting for the first visitor.
//
// autocert renews lazily, at handshake time. A personal app can go days without a single
// request, and the certificate would expire in the meantime. These few lines remove that
// scenario.
func warmUp(ctx context.Context, manager *autocert.Manager, domain string) {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	for {
		// No SupportedProtos: naming acme-tls/1 would make autocert return the challenge
		// certificate instead of the real one.
		hello := &tls.ClientHelloInfo{ServerName: domain}
		if _, err := manager.GetCertificate(hello); err != nil {
			slog.Warn("не удалось прогреть сертификат", "ошибка", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func serve(ctx context.Context, srv *http.Server, listen func() error) error {
	errc := make(chan error, 1)
	go func() {
		if err := listen(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		slog.Info("останавливаюсь")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}
