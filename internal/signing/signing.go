// Package signing implements webhook signatures from the Standard Webhooks spec
// (https://www.standardwebhooks.com). The worker signs; receivers verify.
package signing

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Tolerance is how old (or how far in the future) a timestamp may be. Rejecting
// old timestamps stops replay attacks: an attacker who captured a valid request
// can't resend it an hour later, because the signature covers the timestamp.
const Tolerance = 5 * time.Minute

var (
	ErrMissingHeaders   = errors.New("missing webhook-id, webhook-timestamp or webhook-signature header")
	ErrBadTimestamp     = errors.New("timestamp outside tolerance")
	ErrInvalidSignature = errors.New("no matching signature")
)

func decodeSecret(secret string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
	if err != nil {
		return nil, fmt.Errorf("invalid signing secret: %w", err)
	}
	return key, nil
}

// Sign returns the webhook-signature header value: "v1," + base64(HMAC-SHA256).
// The signed content is "{msgID}.{unix timestamp}.{body}", so changing any of
// the three invalidates the signature.
func Sign(secret, msgID string, ts time.Time, body []byte) (string, error) {
	key, err := decodeSecret(secret)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	fmt.Fprintf(mac, "%s.%d.", msgID, ts.Unix())
	mac.Write(body)
	return "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil)), nil
}

// Verify is what a receiver runs. The signature header can hold several
// space-separated signatures, which is how secret rotation works: during the
// switch, the sender signs with both the old and the new secret.
func Verify(secret string, h http.Header, body []byte, now time.Time) error {
	id, tsStr, sigs := h.Get("webhook-id"), h.Get("webhook-timestamp"), h.Get("webhook-signature")
	if id == "" || tsStr == "" || sigs == "" {
		return ErrMissingHeaders
	}
	unix, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil {
		return ErrBadTimestamp
	}
	ts := time.Unix(unix, 0)
	if now.Sub(ts) > Tolerance || ts.Sub(now) > Tolerance {
		return ErrBadTimestamp
	}
	expected, err := Sign(secret, id, ts, body)
	if err != nil {
		return err
	}
	for _, s := range strings.Fields(sigs) {
		// hmac.Equal is constant-time, for the same reason as the admin token check.
		if hmac.Equal([]byte(s), []byte(expected)) {
			return nil
		}
	}
	return ErrInvalidSignature
}
