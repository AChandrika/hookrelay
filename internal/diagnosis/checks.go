package diagnosis

import (
	"fmt"
	"regexp"
	"strings"
)

// The model is good at reading response bodies and bad at respecting hard
// facts it can see. These checks run in code after every model answer, so the
// facts win: a status code the model contradicted, or evidence it invented,
// never reaches the user.

var networkCategories = map[string]bool{
	"timeout": true, "connection_refused": true, "dns_failure": true,
	"tls_error": true, "blocked_destination": true,
}

// Plausible reports whether a category can be true given the newest attempt.
// Categories that depend on reading the body (auth_failure, firewall_blocked)
// stay open, because that's where the model adds value. Categories that are
// defined by the status code itself are pinned to it.
func Plausible(category string, in Input) (bool, string) {
	if len(in.Attempts) == 0 || category == "unknown" {
		return true, ""
	}
	a := in.Attempts[0]

	if a.StatusCode == nil {
		if !networkCategories[category] {
			return false, "an attempt that got no HTTP response"
		}
		// Network errors are unambiguous in the error text, so the category
		// must match what the text says when we can classify it.
		if class := ErrorClass(a); class != "other" && class != category {
			return false, "the " + strings.ReplaceAll(class, "_", " ") + " error on the latest attempt"
		}
		return true, ""
	}

	code := *a.StatusCode
	var ok bool
	switch category {
	case "endpoint_gone":
		ok = code == 410
	case "rate_limited":
		ok = code == 429
	case "payload_rejected":
		ok = code >= 400 && code < 500
	case "endpoint_not_found":
		ok = code == 404 || code == 405 || (code >= 300 && code < 400)
	case "receiver_error", "receiver_overloaded":
		ok = code >= 500
	case "auth_failure", "firewall_blocked":
		ok = code >= 400
	case "timeout":
		ok = code == 408
	default: // the remaining network categories need "no HTTP response"
		ok = false
	}
	if !ok {
		return false, fmt.Sprintf("an HTTP %d response", code)
	}
	return true, ""
}

var (
	nonWord  = regexp.MustCompile(`[^\p{L}\p{N}]+`)
	ellipsis = regexp.MustCompile(`\.\.\.|…`)
)

func loose(s string) string {
	return strings.TrimSpace(nonWord.ReplaceAllString(strings.ToLower(s), " "))
}

// EvidenceMatch classifies a quoted snippet against the text the model saw:
//
//	"exact": appears as written (ignoring case and spacing)
//	"loose": the same words in order, with punctuation, JSON quoting or "..." changed
//	"":      not in the input, so it can't be shown as evidence
//
// The eval showed why both levels matter: most mismatches were the model
// rewriting JSON quotes, but some were phrases that appeared nowhere.
func EvidenceMatch(snippet, rendered string) string {
	if EvidenceIsGrounded(snippet, rendered) {
		return "exact"
	}
	haystack := " " + loose(rendered) + " "
	matched, chars := 0, 0
	for _, part := range ellipsis.Split(snippet, -1) {
		p := loose(part)
		if p == "" {
			continue
		}
		if !strings.Contains(haystack, " "+p+" ") {
			return ""
		}
		matched++
		chars += len(p)
	}
	if matched == 0 || chars < 4 {
		return ""
	}
	return "loose"
}

// FilterEvidence keeps only snippets that can be found in the input.
func FilterEvidence(evidence []string, rendered string) (kept []string, dropped int) {
	kept = []string{}
	for _, e := range evidence {
		if EvidenceMatch(e, rendered) != "" {
			kept = append(kept, e)
		} else {
			dropped++
		}
	}
	return kept, dropped
}

// Check applies both checks to a model result. It returns the result to show,
// notes explaining any changes, and whether the rules replaced the model.
func Check(in Input, r Result) (Result, []string, bool) {
	var notes []string
	kept, dropped := FilterEvidence(r.Evidence, RenderInput(in))
	r.Evidence = kept
	if dropped == 1 {
		notes = append(notes, "Removed 1 quoted snippet that doesn't appear in the attempts.")
	} else if dropped > 1 {
		notes = append(notes, fmt.Sprintf("Removed %d quoted snippets that don't appear in the attempts.", dropped))
	}

	if ok, what := Plausible(r.Category, in); !ok {
		notes = append(notes, fmt.Sprintf("The model's answer (%s) doesn't fit %s, so this is the rule-based diagnosis.", r.Category, what))
		return Rules(in), notes, true
	}
	return r, notes, false
}
