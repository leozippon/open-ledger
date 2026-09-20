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
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"ledger/internal/books"
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
	dataDir, dbPath, err := dataPaths()
	if err != nil {
		return err
	}
	tlsConfig, err := tlsSetup()
	if err != nil {
		return err
	}
	secret, err := sessionSecret()
	if err != nil {
		return err
	}

	reg, err := books.Open(dataDir)
	if err != nil {
		return err
	}
	defer reg.Close()

	if err := importExisting(reg, dbPath, os.Getenv("LEDGER_IMPORT")); err != nil {
		return err
	}

	admin := store.Admin{
		Username: env("LEDGER_ADMIN_USER", "admin"),
		Password: os.Getenv("LEDGER_ADMIN_PASSWORD"),
	}
	empty, err := reg.Empty()
	if err != nil {
		return err
	}
	switch {
	case !empty && admin.Password != "":
		log.Print("ledger: the directory already has accounts, so LEDGER_ADMIN_USER and LEDGER_ADMIN_PASSWORD are ignored")
	case empty && admin.Password != "":
		acct, err := reg.Create(admin)
		if err != nil {
			return err
		}
		log.Printf("ledger: created the first book for %q; manage further members in the app", acct.Username)
	case empty && os.Getenv("LEDGER_SIGNUP") == "0":
		return errors.New("the directory has no accounts yet: set LEDGER_ADMIN_PASSWORD to create the first administrator, or enable signup")
	}

	handler, err := server.New(reg, server.Config{
		Secret:      secret,
		Secure:      tlsConfig != nil,
		Signup:      os.Getenv("LEDGER_SIGNUP") != "0",
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
	log.Printf("ledger: listening on %s://%s, data %s", scheme, listener.Addr(), dataDir)

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

// importExisting registers leftover single-file ledgers so an upgrade keeps
// every member's login. A path that is already in the directory is skipped.
// dataPaths puts the account directory and the optional upgrade file in one place.
// LEDGER_DATA is the directory. LEDGER_DB defaults to ledger.db inside it, or
// supplies the directory when LEDGER_DATA is unset.
func dataPaths() (dataDir, dbPath string, err error) {
	dataDir = os.Getenv("LEDGER_DATA")
	dbPath = os.Getenv("LEDGER_DB")
	switch {
	case dataDir == "" && dbPath == "":
		dbPath = "ledger.db"
		dataDir = "."
	case dataDir == "":
		dataDir = filepath.Dir(dbPath)
		if dataDir == "" {
			dataDir = "."
		}
	case dbPath == "":
		dbPath = filepath.Join(dataDir, "ledger.db")
	}
	if dataDir, err = filepath.Abs(dataDir); err != nil {
		return "", "", err
	}
	dbPath, err = filepath.Abs(dbPath)
	return dataDir, dbPath, err
}

func importExisting(reg *books.Books, dbPath, extra string) error {
	var paths []string
	if fileExists(dbPath) {
		paths = append(paths, dbPath)
	}
	for _, path := range strings.Split(extra, ",") {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if !fileExists(path) {
			return fmt.Errorf("LEDGER_IMPORT file %q does not exist", path)
		}
		paths = append(paths, path)
	}
	for _, path := range paths {
		id, added, err := reg.Import(path)
		if err != nil {
			return err
		}
		if added {
			log.Printf("ledger: imported book %s from %s", id, path)
		}
	}
	return nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
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
