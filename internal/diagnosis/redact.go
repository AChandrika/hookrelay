package diagnosis

import (
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// Response bodies come from customers' servers and can contain anything:
// emails, tokens, card numbers. We strip the common shapes before any of it
// reaches the model. The model is local here, but the same code would matter
// even more with a hosted API, and logs and caches keep what they're given.
var redactions = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(`eyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}`), "[JWT]"},
	{regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{8,}`), "${1} [TOKEN]"},
	{regexp.MustCompile(`\b(?:whsec|hr|sk|pk|rk)_[A-Za-z0-9+/=_-]{8,}`), "[SECRET]"},
	{regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`), "[EMAIL]"},
	{regexp.MustCompile(`\b(?:\d[ -]?){12,18}\d\b`), "[NUMBER]"}, // card-length digit runs
	{regexp.MustCompile(`\b[A-Fa-f0-9]{32,}\b`), "[HEX]"},        // hashes, raw keys
}

const maxBodyChars = 1000

// Redact removes sensitive-looking substrings.
func Redact(s string) string {
	for _, r := range redactions {
		s = r.re.ReplaceAllString(s, r.repl)
	}
	return s
}

// RedactURL keeps the scheme, host and path (useful for diagnosis) but drops
// credentials and query values, which often hold tokens.
func RedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return Redact(raw)
	}
	u.User = nil
	if u.RawQuery != "" {
		keys := make([]string, 0)
		for k := range u.Query() {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = k + "=REDACTED"
		}
		u.RawQuery = strings.Join(parts, "&")
	}
	u.Fragment = ""
	return u.String()
}

// Prepare redacts and trims an input. Everything, including evals, goes
// through here, so what we evaluate is exactly what production sends.
func Prepare(in Input) Input {
	out := Input{EndpointURL: RedactURL(in.EndpointURL), EventType: in.EventType}
	for _, a := range in.Attempts {
		b := Redact(a.Body)
		if len(b) > maxBodyChars {
			b = b[:maxBodyChars] + " …[truncated]"
		}
		headers := map[string]string{}
		for k, v := range a.Headers {
			headers[k] = Redact(v)
		}
		out.Attempts = append(out.Attempts, AttemptInput{
			Number: a.Number, StatusCode: a.StatusCode, DurationMS: a.DurationMS,
			Headers: headers, Body: b, Error: Redact(a.Error),
		})
	}
	return out
}
