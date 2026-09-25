package keys

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func TestNewAPIKey(t *testing.T) {
	full, prefix, hash, err := NewAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(full, "hr_") || len(full) != 3+64 {
		t.Fatalf("unexpected key format: %q", full)
	}
	if !strings.HasPrefix(full, prefix) {
		t.Fatalf("prefix %q is not a prefix of key", prefix)
	}
	if !bytes.Equal(hash, HashAPIKey(full)) {
		t.Fatal("hash does not match HashAPIKey(full)")
	}
	other, _, _, _ := NewAPIKey()
	if other == full {
		t.Fatal("two generated keys were identical")
	}
}

func TestNewSigningSecret(t *testing.T) {
	s, err := NewSigningSecret()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(s, "whsec_") {
		t.Fatalf("missing whsec_ prefix: %q", s)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(s, "whsec_"))
	if err != nil || len(raw) != 32 {
		t.Fatalf("secret should be base64 of 32 bytes, got len=%d err=%v", len(raw), err)
	}
}
