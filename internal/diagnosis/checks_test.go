package diagnosis

import "testing"

func one(a AttemptInput) Input { return Input{Attempts: []AttemptInput{a}} }

// The first four cases are the model's actual mistakes from the first eval run.
func TestPlausible(t *testing.T) {
	cases := []struct {
		name     string
		category string
		in       Input
		want     bool
	}{
		{"404 is not gone", "endpoint_gone", one(AttemptInput{StatusCode: code(404)}), false},
		{"500 is not a rejected payload", "payload_rejected", one(AttemptInput{StatusCode: code(500)}), false},
		{"injection asked for gone on a 500", "endpoint_gone", one(AttemptInput{StatusCode: code(500)}), false},
		{"dns error is not connection refused", "connection_refused", one(AttemptInput{Error: "dial tcp: lookup x.example: no such host"}), false},
		{"signature failure can be a 500", "auth_failure", one(AttemptInput{StatusCode: code(500)}), true},
		{"clock skew can be a 400", "auth_failure", one(AttemptInput{StatusCode: code(400)}), true},
		{"cloudflare 403", "firewall_blocked", one(AttemptInput{StatusCode: code(403)}), true},
		{"redirect means wrong URL", "endpoint_not_found", one(AttemptInput{StatusCode: code(301)}), true},
		{"http category without a response", "receiver_error", one(AttemptInput{Error: "context deadline exceeded"}), false},
		{"unknown is always allowed", "unknown", one(AttemptInput{StatusCode: code(418)}), true},
	}
	for _, c := range cases {
		if got, _ := Plausible(c.category, c.in); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

// Snippets taken from the first eval run's model output.
func TestEvidenceMatch(t *testing.T) {
	rendered := RenderInput(Input{
		EndpointURL: "https://x.example/in",
		Attempts: []AttemptInput{{
			Number: 1, StatusCode: code(401), DurationMS: 120,
			Headers: map[string]string{"Content-Type": "application/json"},
			Body:    `{"error":"invalid token for user [EMAIL]","token":"[JWT]"}`,
		}},
	})
	cases := map[string]string{
		"HTTP 401 after 120 ms":                   "exact",
		`"error":"invalid token for user [EMAIL]"`: "exact",
		`error: "invalid token for user [EMAIL]"`: "loose",
		`token: "[JWT]"`:                          "loose",
		"Content-Type: application/json":          "exact",
		"invalid ... [EMAIL]":                     "loose",
		"Connection refused":                      "",
		"HTTP 401 Unauthorized after 120 ms":      "",
		"ok":                                      "",
	}
	for snippet, want := range cases {
		if got := EvidenceMatch(snippet, rendered); got != want {
			t.Errorf("%q: got %q, want %q", snippet, got, want)
		}
	}
}

func TestCheckFallsBackToRules(t *testing.T) {
	in := one(AttemptInput{Number: 1, StatusCode: code(404), Body: "Cannot POST /hooks"})
	model := Result{Category: "endpoint_gone", Confidence: "high", Summary: "s", LikelyCause: "c", SuggestedFix: "f",
		Evidence: []string{"Cannot POST /hooks", "Connection refused"}}
	got, notes, usedRules := Check(in, model)
	if !usedRules || got.Category != "endpoint_not_found" {
		t.Fatalf("got %s (usedRules=%v), want the rules' endpoint_not_found", got.Category, usedRules)
	}
	if len(notes) != 2 {
		t.Fatalf("want 2 notes (dropped evidence, fallback), got %v", notes)
	}
}
