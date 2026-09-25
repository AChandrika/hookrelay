package diagnosis

import (
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
	in := `user jane.doe@example.com sent Authorization: Bearer abc123def456ghi ` +
		`jwt eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjMifQ.c2lnbmF0dXJl card 4242 4242 4242 4242 ` +
		`secret whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw hash 9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08`
	out := Redact(in)
	for _, leaked := range []string{"jane.doe", "abc123def456ghi", "eyJhbGci", "4242 4242", "whsec_MfKQ", "9f86d081"} {
		if strings.Contains(out, leaked) {
			t.Errorf("redacted output still contains %q:\n%s", leaked, out)
		}
	}
	for _, kept := range []string{"[EMAIL]", "Bearer [TOKEN]", "[JWT]", "[NUMBER]", "[SECRET]", "[HEX]"} {
		if !strings.Contains(out, kept) {
			t.Errorf("expected %q in output:\n%s", kept, out)
		}
	}
}

func TestRedactKeepsUsefulDetail(t *testing.T) {
	in := `HTTP 500: connection pool exhausted after 30s (max 20)`
	if out := Redact(in); out != in {
		t.Fatalf("ordinary error text was changed:\n%s\n%s", in, out)
	}
}

func TestRedactURL(t *testing.T) {
	got := RedactURL("https://user:pass@hooks.example.com/in?token=s3cret&team=42#frag")
	want := "https://hooks.example.com/in?team=REDACTED&token=REDACTED"
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}
