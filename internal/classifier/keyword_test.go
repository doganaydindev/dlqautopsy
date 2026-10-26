package classifier

import "testing"

func TestContainsKeywordBoundaries(t *testing.T) {
	tests := []struct {
		s, kw string
		want  bool
	}{
		// HTTP status codes must be whole numbers.
		{"http 403 forbidden", "403", true},
		{"status=403", "403", true},
		{"403", "403", true},
		{"(403)", "403", true},
		{"order 14030 failed", "403", false},
		{"id 4030", "403", false},
		{"port 45031", "503", false},
		{"retry after 5030ms", "503", false},
		{"took 4011ms", "401", false},
		{"http 429 too many requests", "429", true},
		{"error 14290", "429", false},

		// Short keywords need a word boundary at the start.
		{"oomkilled", "oom", true},
		{"container killed: oom", "oom", true},
		{"no room left in batch", "oom", false},
		{"unexpected eof", "eof", true},
		{"the thereof clause", "eof", false},
		{"dns lookup failed", "dns", true},
		{"mdns responder down", "dns", false},
		{"tls: handshake failure", "tls", true},
		{"tlsv1 alert", "tls", true},
		{"bottls", "tls", false},

		// Longer keywords stay plain substrings.
		{"throttled by upstream", "throttl", true},
		{"json.unmarshal failed", "unmarshal", true},
		{"deadlocked", "deadlock", true},
	}
	for _, tt := range tests {
		if got := containsKeyword(tt.s, tt.kw); got != tt.want {
			t.Errorf("containsKeyword(%q, %q) = %v, want %v", tt.s, tt.kw, got, tt.want)
		}
	}
}

// Numbers inside IDs, ports or sizes must not be read as HTTP status codes.
func TestClassifyIgnoresStatusCodesInsideNumbers(t *testing.T) {
	c := &Classifier{}
	for _, e := range []string{
		"order 14030 rejected by legacy handler",
		"batch 45031 skipped",
		"job 4011 exited with code 2",
	} {
		if got := c.classify(e); got.class != ClassUnknown {
			t.Errorf("classify(%q) = %q, want %q", e, got.class, ClassUnknown)
		}
	}
	if got := c.classify("upstream returned 403 for order 14030"); got.class != ClassAuthFailure {
		t.Errorf("real 403 not detected: got %q", got.class)
	}
	if got := c.classify("no room in the batch"); got.class != ClassUnknown {
		t.Errorf("\"room\" matched a rule: got %q", got.class)
	}
}
