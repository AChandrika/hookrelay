// Package diagnosis explains why a webhook delivery is failing. It prepares
// redacted attempt data, asks a local LLM (via Ollama) for a structured
// diagnosis, validates it, and falls back to simple rules when the model is
// unavailable or returns something invalid.
package diagnosis

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// AttemptInput is one delivery attempt as the model sees it.
type AttemptInput struct {
	Number     int               `json:"attempt"`
	StatusCode *int              `json:"http_status,omitempty"`
	DurationMS int               `json:"duration_ms"`
	Headers    map[string]string `json:"response_headers,omitempty"`
	Body       string            `json:"response_body,omitempty"`
	Error      string            `json:"error,omitempty"`
}

type Input struct {
	EndpointURL string         `json:"endpoint_url"`
	EventType   string         `json:"event_type"`
	Attempts    []AttemptInput `json:"attempts"` // newest first
}

type Result struct {
	Category     string   `json:"category"`
	Summary      string   `json:"summary"`
	LikelyCause  string   `json:"likely_cause"`
	SuggestedFix string   `json:"suggested_fix"`
	Confidence   string   `json:"confidence"`
	Evidence     []string `json:"evidence"`
}

type Category struct {
	Name    string
	Meaning string
}

// Categories is a closed list on purpose: a fixed label can be counted,
// charted and evaluated for accuracy, while free text can't.
var Categories = []Category{
	{"auth_failure", "the receiver rejected the signature or credentials (wrong signing secret, signature verification failing, or the receiver's clock being off so the timestamp check fails)"},
	{"endpoint_not_found", "the URL or route doesn't exist on the receiver, or it has moved (404, missing POST handler, redirect to another URL)"},
	{"endpoint_gone", "the receiver says the endpoint was permanently removed (410)"},
	{"payload_rejected", "the receiver refused the body itself (400, 413, 415, 422: validation errors, size limits, content type)"},
	{"rate_limited", "the receiver is throttling requests (429)"},
	{"receiver_error", "the receiver's own code or a dependency such as its database failed (500, stack traces)"},
	{"receiver_overloaded", "a proxy or load balancer answered because the app behind it is down, restarting, in maintenance or overloaded (502, 503, 504)"},
	{"firewall_blocked", "a firewall, WAF or bot protection in front of the receiver blocked the request"},
	{"timeout", "the receiver did not respond before the timeout"},
	{"connection_refused", "nothing accepted the connection at that host and port"},
	{"dns_failure", "the hostname could not be resolved"},
	{"tls_error", "the TLS handshake failed (expired, self-signed or mismatched certificate)"},
	{"blocked_destination", "hookrelay itself refused to connect because the URL points to a private or reserved address"},
	{"unknown", "the evidence doesn't clearly support any category above"},
}

var Confidences = []string{"low", "medium", "high"}

func CategoryNames() []string {
	names := make([]string, len(Categories))
	for i, c := range Categories {
		names[i] = c.Name
	}
	return names
}

func ValidCategory(name string) bool { return slices.Contains(CategoryNames(), name) }

// Validate checks everything the JSON schema can't guarantee. Constrained
// decoding makes the output parse, but a small model can still return empty
// strings or an essay, so the application checks again.
func (r Result) Validate() error {
	var errs []error
	if !ValidCategory(r.Category) {
		errs = append(errs, fmt.Errorf("category %q is not an allowed value", r.Category))
	}
	if !slices.Contains(Confidences, r.Confidence) {
		errs = append(errs, fmt.Errorf("confidence %q must be low, medium or high", r.Confidence))
	}
	for name, v := range map[string]string{"summary": r.Summary, "likely_cause": r.LikelyCause, "suggested_fix": r.SuggestedFix} {
		if strings.TrimSpace(v) == "" {
			errs = append(errs, fmt.Errorf("%s is empty", name))
		} else if len(v) > 800 {
			errs = append(errs, fmt.Errorf("%s is too long (%d characters, max 800)", name, len(v)))
		}
	}
	if len(r.Evidence) > 5 {
		errs = append(errs, fmt.Errorf("evidence has %d items, max 5", len(r.Evidence)))
	}
	return errors.Join(errs...)
}
