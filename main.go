// Command ledger runs the family bookkeeping server.
package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ledger/internal/server"
	"ledger/internal/store"
)

func main() {
	log.SetFlags(log.LstdFlags)
	if err := run(); err != nil {
		log.Fatalf("ledger: %v", err)
	}
}

func run() error {
	addr := env("LEDGER_ADDR", ":18080")
	dbPath := env("LEDGER_DB", "ledger.db")
	tlsConfig, err := tlsSetup()
	if err != nil {
		return err
	}
	secret, err := sessionSecret()
	if err != nil {
		return err
	}

	admin := store.Admin{
		Username: env("LEDGER_ADMIN_USER", "admin"),
		Password: os.Getenv("LEDGER_ADMIN_PASSWORD"),
	}
	st, err := store.Open(dbPath, admin)
	if errors.Is(err, store.ErrAdminRequired) {
		return errors.New("the ledger has no members yet: set LEDGER_ADMIN_PASSWORD to create the first administrator")
	}
	if err != nil {
		return err
	}
	defer st.Close()
	switch {
	case st.CreatedAdmin() != "":
		log.Printf("ledger: created the first administrator %q; manage further members in the app", st.CreatedAdmin())
	case admin.Password != "":
		log.Print("ledger: the ledger already has members, so LEDGER_ADMIN_USER and LEDGER_ADMIN_PASSWORD are ignored")
	}

	handler, err := server.New(st, server.Config{
		Secret:      secret,
		Secure:      tlsConfig != nil,
		DeepSeekKey: os.Getenv("LEDGER_DEEPSEEK_KEY"),
		DeepSeekURL: os.Getenv("LEDGER_DEEPSEEK_URL"),
	})
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on LEDGER_ADDR=%q: %w", addr, err)
	}
	scheme := "http"
	if tlsConfig != nil {
		listener, scheme = tls.NewListener(listener, tlsConfig), "https"
	}

	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      90 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(listener) }()
	log.Printf("ledger: listening on %s://%s, database %s", scheme, listener.Addr(), dbPath)

	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdown)
	}
}

// tlsSetup loads the certificate pair, or returns nil for plain HTTP when
// neither variable is set. Half a pair is a configuration mistake, not a
// reason to silently serve an unencrypted port.
func tlsSetup() (*tls.Config, error) {
	certFile, keyFile := os.Getenv("LEDGER_TLS_CERT"), os.Getenv("LEDGER_TLS_KEY")
	if certFile == "" && keyFile == "" {
		return nil, nil
	}
	if certFile == "" || keyFile == "" {
		return nil, errors.New("LEDGER_TLS_CERT and LEDGER_TLS_KEY must be set together")
	}
	certificate, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load TLS certificate: %w", err)
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}, nil
}

// sessionSecret reads LEDGER_SECRET, or generates a per-process key when it is
// unset. A generated key means every restart invalidates existing sessions.
func sessionSecret() ([]byte, error) {
	if secret := os.Getenv("LEDGER_SECRET"); secret != "" {
		if len(secret) < 16 {
			return nil, errors.New("LEDGER_SECRET must be at least 16 characters")
		}
		return []byte(secret), nil
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("generate session secret: %w", err)
	}
	log.Print("ledger: LEDGER_SECRET is unset, using a random key; logins reset on restart")
	return secret, nil
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
