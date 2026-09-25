package signing

import (
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"
)

// Test vector from the Standard Webhooks / Svix documentation. Matching it
// proves we're compatible with receivers that use their official libraries.
func TestSignKnownVector(t *testing.T) {
	got, err := Sign("whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw", "msg_p5jXN8AQM9LWM0D4loKWxJek",
		time.Unix(1614265330, 0), []byte(`{"test": 2432232314}`))
	if err != nil {
		t.Fatal(err)
	}
	want := "v1,g0hM9SsE+OTPJTGt/tmIKtSyZlE3uFJELVlNIOLJ1OE="
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestVerify(t *testing.T) {
	const secret = "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw"
	now := time.Unix(1_700_000_000, 0)
	body := []byte(`{"hello":"world"}`)

	headers := func(ts time.Time) http.Header {
		sig, err := Sign(secret, "msg_1", ts, body)
		if err != nil {
			t.Fatal(err)
		}
		h := http.Header{}
		h.Set("webhook-id", "msg_1")
		h.Set("webhook-timestamp", strconv.FormatInt(ts.Unix(), 10))
		h.Set("webhook-signature", "v1,bogus "+sig) // rotation: one stale, one valid
		return h
	}

	if err := Verify(secret, headers(now), body, now); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	if err := Verify(secret, headers(now), []byte(`{"hello":"evil"}`), now); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("tampered body: got %v, want ErrInvalidSignature", err)
	}
	if err := Verify(secret, headers(now.Add(-10*time.Minute)), body, now); !errors.Is(err, ErrBadTimestamp) {
		t.Fatalf("replayed old request: got %v, want ErrBadTimestamp", err)
	}
	if err := Verify(secret, http.Header{}, body, now); !errors.Is(err, ErrMissingHeaders) {
		t.Fatalf("no headers: got %v, want ErrMissingHeaders", err)
	}
}
