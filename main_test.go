package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDataPaths(t *testing.T) {
	t.Setenv("LEDGER_DATA", "")
	t.Setenv("LEDGER_DB", "")
	dir, db, err := dataPaths()
	if err != nil {
		t.Fatal(err)
	}
	wd, _ := os.Getwd()
	if dir != wd || db != filepath.Join(wd, "ledger.db") {
		t.Fatalf("defaults = %s %s", dir, db)
	}

	root := t.TempDir()
	t.Setenv("LEDGER_DATA", root)
	t.Setenv("LEDGER_DB", "")
	dir, db, err = dataPaths()
	if err != nil {
		t.Fatal(err)
	}
	if dir != root || db != filepath.Join(root, "ledger.db") {
		t.Fatalf("data dir only = %s %s", dir, db)
	}
}

func TestTLSSetupNeedsBothOrNeither(t *testing.T) {
	certFile, keyFile := writeSelfSigned(t)

	t.Run("neither serves plain http", func(t *testing.T) {
		t.Setenv("LEDGER_TLS_CERT", "")
		t.Setenv("LEDGER_TLS_KEY", "")
		cfg, err := tlsSetup()
		if err != nil || cfg != nil {
			t.Fatalf("tlsSetup() = %v, %v; want nil, nil", cfg, err)
		}
	})

	// Half a pair is a mistake, and quietly falling back to an unencrypted
	// port would be the worst possible answer to it.
	for name, env := range map[string][2]string{
		"only a certificate": {certFile, ""},
		"only a key":         {"", keyFile},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("LEDGER_TLS_CERT", env[0])
			t.Setenv("LEDGER_TLS_KEY", env[1])
			if _, err := tlsSetup(); err == nil {
				t.Fatal("tlsSetup() accepted half a certificate pair")
			}
		})
	}

	t.Run("missing file fails at startup", func(t *testing.T) {
		t.Setenv("LEDGER_TLS_CERT", filepath.Join(t.TempDir(), "absent.crt"))
		t.Setenv("LEDGER_TLS_KEY", keyFile)
		if _, err := tlsSetup(); err == nil {
			t.Fatal("tlsSetup() accepted a missing certificate file")
		}
	})

	t.Run("both", func(t *testing.T) {
		t.Setenv("LEDGER_TLS_CERT", certFile)
		t.Setenv("LEDGER_TLS_KEY", keyFile)
		cfg, err := tlsSetup()
		if err != nil {
			t.Fatalf("tlsSetup(): %v", err)
		}
		if cfg.MinVersion != tls.VersionTLS12 {
			t.Errorf("MinVersion = %#x, want TLS 1.2", cfg.MinVersion)
		}
		if len(cfg.Certificates) != 1 {
			t.Errorf("loaded %d certificates, want 1", len(cfg.Certificates))
		}
	})
}

// writeSelfSigned produces the kind of certificate the README tells the operator
// to generate with openssl.
func writeSelfSigned(t *testing.T) (certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "ledger-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}

	dir := t.TempDir()
	certFile, keyFile = filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key")
	write := func(path, blockType string, bytes []byte) {
		if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: bytes}), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	write(certFile, "CERTIFICATE", der)
	write(keyFile, "EC PRIVATE KEY", keyDER)
	return certFile, keyFile
}
