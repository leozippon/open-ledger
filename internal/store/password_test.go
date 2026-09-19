package store

import (
	"encoding/base64"
	"regexp"
	"testing"
)

// The stored form has to describe itself, so the cost can be raised later
// without invalidating the hashes already in the database.
var encodedHash = regexp.MustCompile(`^pbkdf2-sha256\$(\d+)\$([A-Za-z0-9+/]+)\$([A-Za-z0-9+/]+)$`)

func TestHashPasswordIsSelfDescribing(t *testing.T) {
	encoded, err := hashPassword("family-secret")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	fields := encodedHash.FindStringSubmatch(encoded)
	if fields == nil {
		t.Fatalf("hash %q does not match %s", encoded, encodedHash)
	}
	if fields[1] != "600000" {
		t.Errorf("iteration count = %s, want 600000", fields[1])
	}
	salt, err := base64.RawStdEncoding.DecodeString(fields[2])
	if err != nil || len(salt) != hashSaltLength {
		t.Errorf("salt %q decodes to %d bytes (%v), want %d", fields[2], len(salt), err, hashSaltLength)
	}
	key, err := base64.RawStdEncoding.DecodeString(fields[3])
	if err != nil || len(key) != hashKeyLength {
		t.Errorf("key %q decodes to %d bytes (%v), want %d", fields[3], len(key), err, hashKeyLength)
	}

	// Two hashes of the same password differ, because the salt is per user.
	other, err := hashPassword("family-secret")
	if err != nil {
		t.Fatalf("hash again: %v", err)
	}
	if other == encoded {
		t.Error("two hashes of the same password are identical, so the salt is not random")
	}

	for _, candidate := range []string{"family-secret", "family-secre", "", "Family-secret"} {
		ok, err := verifyPassword(encoded, candidate)
		if err != nil {
			t.Fatalf("verify %q: %v", candidate, err)
		}
		if want := candidate == "family-secret"; ok != want {
			t.Errorf("verify %q = %v, want %v", candidate, ok, want)
		}
	}
}

func TestVerifyPasswordRejectsBrokenHashes(t *testing.T) {
	for name, encoded := range map[string]string{
		"empty":           "",
		"unknown scheme":  "bcrypt$10$abc$def",
		"missing field":   "pbkdf2-sha256$600000$c2FsdA",
		"bad iterations":  "pbkdf2-sha256$many$c2FsdA$aGFzaA",
		"zero iterations": "pbkdf2-sha256$0$c2FsdA$aGFzaA",
		"bad base64":      "pbkdf2-sha256$600000$c2FsdA$not base64!",
	} {
		if _, err := verifyPassword(encoded, "family-secret"); err == nil {
			t.Errorf("%s: verify accepted %q", name, encoded)
		}
	}
}
