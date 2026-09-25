package diagnosis

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

// ErrorClass buckets an attempt into a short label.
func ErrorClass(a AttemptInput) string {
	if a.StatusCode != nil {
		return fmt.Sprintf("http_%d", *a.StatusCode)
	}
	e := strings.ToLower(a.Error)
	switch {
	case strings.Contains(e, "private or reserved address"):
		return "blocked_destination"
	case strings.Contains(e, "timeout"), strings.Contains(e, "deadline exceeded"):
		return "timeout"
	case strings.Contains(e, "connection refused"), strings.Contains(e, "actively refused"):
		return "connection_refused"
	case strings.Contains(e, "no such host"), strings.Contains(e, "server misbehaving"):
		return "dns_failure"
	case strings.Contains(e, "x509"), strings.Contains(e, "tls"), strings.Contains(e, "certificate"):
		return "tls_error"
	default:
		return "other"
	}
}

var digitsOrHex = regexp.MustCompile(`[0-9a-f]{6,}|\d+`)

// Fingerprint identifies "the same failure": same endpoint, same error class,
// and the same response body once numbers and IDs are blanked out. Identical
// failures reuse one diagnosis instead of calling the model again. Including
// the endpoint ID also keeps the cache inside one tenant.
func Fingerprint(endpointID string, in Input) string {
	if len(in.Attempts) == 0 {
		return ""
	}
	latest := in.Attempts[0]
	body := strings.ToLower(latest.Body)
	if len(body) > 200 {
		body = body[:200]
	}
	body = digitsOrHex.ReplaceAllString(body, "#")
	sum := sha256.Sum256([]byte(endpointID + "|" + ErrorClass(latest) + "|" + body))
	return hex.EncodeToString(sum[:16])
}

// Rules is the baseline: a lookup on the newest attempt's status code or
// error type. It's the fallback when the model is down, and the bar the model
// has to beat in evals to justify its cost.
func Rules(in Input) Result {
	if len(in.Attempts) == 0 {
		return Result{Category: "unknown", Confidence: "low", Summary: "No attempts to analyze.",
			LikelyCause: "The delivery hasn't been attempted yet.", SuggestedFix: "Wait for the first attempt."}
	}
	latest := in.Attempts[0]
	class := ErrorClass(latest)
	cat := "unknown"

	if latest.StatusCode != nil {
		code := *latest.StatusCode
		switch {
		case code == 401 || code == 403:
			cat = "auth_failure"
		case code == 404:
			cat = "endpoint_not_found"
		case code == 410:
			cat = "endpoint_gone"
		case code == 429:
			cat = "rate_limited"
		case code == 502 || code == 503 || code == 504:
			cat = "receiver_overloaded"
		case code >= 500:
			cat = "receiver_error"
		case code >= 400:
			cat = "payload_rejected"
		}
	} else if class != "other" {
		cat = class
	}

	evidence := []string{}
	if latest.StatusCode != nil {
		evidence = append(evidence, fmt.Sprintf("HTTP %d", *latest.StatusCode))
	} else if latest.Error != "" {
		e := latest.Error
		if len(e) > 120 {
			e = e[:120]
		}
		evidence = append(evidence, e)
	}

	t := ruleText[cat]
	return Result{
		Category: cat, Confidence: "medium", Evidence: evidence,
		Summary: t[0], LikelyCause: t[1], SuggestedFix: t[2],
	}
}

// summary, likely cause, suggested fix
var ruleText = map[string][3]string{
	"auth_failure":        {"The receiver rejected the request as unauthorized.", "The receiver's signing secret or credentials don't match what hookrelay sends.", "Check that the receiver verifies signatures with this endpoint's current signing secret."},
	"endpoint_not_found":  {"The receiver has no handler at this URL.", "The URL path is wrong or the route was removed.", "Confirm the URL and that the route accepts POST requests."},
	"endpoint_gone":       {"The receiver says this endpoint was removed.", "The endpoint was deleted on the receiver's side.", "Register the new URL, or delete this endpoint."},
	"payload_rejected":    {"The receiver refused the request body.", "The receiver's validation, size limit or content-type check rejected the payload.", "Compare the receiver's expected format with the payload hookrelay sends."},
	"rate_limited":        {"The receiver is throttling requests.", "Too many requests are arriving for the receiver's rate limit.", "Raise the receiver's rate limit for webhook traffic; retries will honor Retry-After."},
	"receiver_error":      {"The receiver crashed while handling the request.", "A bug or failing dependency in the receiver's handler.", "Check the receiver's application logs at the attempt time."},
	"receiver_overloaded": {"The receiver's proxy reported the app as unavailable.", "The application behind the proxy is down, restarting or overloaded.", "Check the receiver's health and capacity; retries will continue."},
	"timeout":             {"The receiver didn't respond in time.", "The handler does slow work before responding.", "Respond 2xx immediately and process the webhook asynchronously."},
	"connection_refused":  {"Nothing accepted the connection.", "The receiving server is down or not listening on that port.", "Check that the server is running and reachable on that host and port."},
	"dns_failure":         {"The hostname couldn't be resolved.", "The domain doesn't exist or its DNS records were removed.", "Check the hostname and its DNS records."},
	"tls_error":           {"The TLS handshake failed.", "The receiver's certificate is expired, self-signed or doesn't match the hostname.", "Renew or fix the receiver's TLS certificate."},
	"blocked_destination": {"hookrelay refused to connect to this address.", "The URL points to a private or reserved IP address.", "Use a publicly reachable URL for the endpoint."},
	"firewall_blocked":    {"A firewall blocked the request.", "A WAF or bot protection in front of the receiver rejected hookrelay's requests.", "Allowlist hookrelay's requests in the receiver's firewall."},
	"unknown":             {"The failure doesn't match a known pattern.", "Not enough information to tell.", "Inspect the attempts below for details."},
}
