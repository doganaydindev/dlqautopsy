package classifier

import (
	"strings"
	"testing"

	"github.com/doganaydindev/dlqautopsy/internal/normalizer"
)

// The classifier runs on the normalized error, and the normalizer replaces
// runs of normalizer.MinVolatileDigits or more digits with [NUM]. A numeric
// keyword that long could never match, so keep all of them shorter.
func TestNumericKeywordsSurviveNumberNormalization(t *testing.T) {
	for i, r := range rules {
		for _, kw := range r.keywords {
			digits := 0
			for _, ch := range kw {
				if ch >= '0' && ch <= '9' {
					digits++
				} else {
					digits = 0
				}
				if digits >= normalizer.MinVolatileDigits {
					t.Errorf("rule %d keyword %q has a run of %d+ digits; the normalizer would replace it", i, kw, normalizer.MinVolatileDigits)
				}
			}
		}
	}
}

// Status-code rules still fire on normalized text that also contains long
// numbers, and a long number that merely contains a status code does not.
func TestClassifyAfterNormalization(t *testing.T) {
	c := &Classifier{}
	tests := []struct {
		raw       string
		wantClass string
	}{
		{"HTTP 503 Service Unavailable (request 88231907)", ClassDownstreamDependency},
		{"HTTP 401 Unauthorized for account 4401928", ClassAuthFailure},
		{"upstream answered 429 for batch 5512093", ClassTransientError},
		{"invoice 14030 is already closed", ClassUnknown},
		{"order 50312 rejected", ClassUnknown},
	}
	for _, tt := range tests {
		normalized, _ := normalizer.NormalizeError(tt.raw)
		got := c.classify(normalized)
		if got.class != tt.wantClass {
			t.Errorf("classify(normalize(%q)) = %q (normalized: %q), want %q", tt.raw, got.class, normalized, tt.wantClass)
		}
		if strings.Contains(tt.raw, "5512093") && !strings.Contains(normalized, "[NUM]") {
			t.Errorf("expected long number to be normalized in %q", normalized)
		}
	}
}
