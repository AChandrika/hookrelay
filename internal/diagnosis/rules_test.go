package diagnosis

import "testing"

func code(n int) *int { return &n }

func TestRulesAndFingerprint(t *testing.T) {
	cases := []struct {
		a    AttemptInput
		want string
	}{
		{AttemptInput{StatusCode: code(401)}, "auth_failure"},
		{AttemptInput{StatusCode: code(422)}, "payload_rejected"},
		{AttemptInput{StatusCode: code(503)}, "receiver_overloaded"},
		{AttemptInput{Error: `Post "https://x": context deadline exceeded (Client.Timeout exceeded while awaiting headers)`}, "timeout"},
		{AttemptInput{Error: "connectex: No connection could be made because the target machine actively refused it."}, "connection_refused"},
		{AttemptInput{Error: "dial tcp: lookup nope.invalid: no such host"}, "dns_failure"},
		{AttemptInput{Error: "tls: failed to verify certificate: x509: certificate has expired"}, "tls_error"},
		{AttemptInput{StatusCode: code(301)}, "unknown"},
	}
	for _, c := range cases {
		got := Rules(Input{Attempts: []AttemptInput{c.a}})
		if got.Category != c.want {
			t.Errorf("%+v: got %s, want %s", c.a, got.Category, c.want)
		}
		if err := got.Validate(); err != nil {
			t.Errorf("rules produced an invalid result: %v", err)
		}
	}

	a := Input{Attempts: []AttemptInput{{StatusCode: code(500), Body: `{"error":"db timeout","request_id":"a1b2c3d4e5"}`}}}
	b := Input{Attempts: []AttemptInput{{StatusCode: code(500), Body: `{"error":"db timeout","request_id":"ffee998877"}`}}}
	c := Input{Attempts: []AttemptInput{{StatusCode: code(500), Body: `{"error":"null pointer"}`}}}
	if Fingerprint("ep1", a) != Fingerprint("ep1", b) {
		t.Error("same failure with different request IDs should share a fingerprint")
	}
	if Fingerprint("ep1", a) == Fingerprint("ep1", c) {
		t.Error("different failures should not share a fingerprint")
	}
	if Fingerprint("ep1", a) == Fingerprint("ep2", a) {
		t.Error("fingerprints must differ across endpoints (and so across tenants)")
	}
}
