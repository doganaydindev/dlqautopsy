package classifier

import "testing"

// Every keyword must be reachable: classifying the keyword on its own has to
// land on the rule that declares it. A failure here means an earlier rule
// shadows it (first match wins), which is how "lock wait timeout" used to end
// up as a downstream timeout.
func TestRulesHaveNoShadowedKeywords(t *testing.T) {
	c := &Classifier{}
	for i, r := range rules {
		for _, kw := range r.keywords {
			got := c.classify(kw)
			if got != r.result {
				t.Errorf("rule %d keyword %q is shadowed: got %q (%s), want %q (%s)",
					i, kw, got.class, got.classification, r.result.class, r.result.classification)
			}
		}
	}
}

func TestClassifyRealisticErrors(t *testing.T) {
	c := &Classifier{}

	tests := []struct {
		name               string
		normalizedError    string
		wantClass          string
		wantClassification string
		wantVerdict        string
	}{
		{
			name:               "lock wait timeout is lock contention, not a downstream timeout",
			normalizedError:    "Error 1205 (HY000): Lock wait timeout exceeded; try restarting transaction",
			wantClass:          ClassTransientError,
			wantClassification: "Database deadlock or lock contention. Safe to retry with backoff.",
			wantVerdict:        VerdictRetryWithBackoff,
		},
		{
			name:               "postgres deadlock",
			normalizedError:    "pq: deadlock detected: process 12847 waits for ShareLock on transaction 98712",
			wantClass:          ClassTransientError,
			wantClassification: "Database deadlock or lock contention. Safe to retry with backoff.",
			wantVerdict:        VerdictRetryWithBackoff,
		},
		{
			name:               "504 gateway timeout uses the HTTP rule",
			normalizedError:    "HTTP 504 Gateway Timeout from upstream",
			wantClass:          ClassDownstreamDependency,
			wantClassification: "Downstream HTTP error. Service temporarily unavailable.",
			wantVerdict:        VerdictSafeToRetry,
		},
		{
			name:               "context deadline exceeded",
			normalizedError:    "rpc error: code = DeadlineExceeded desc = context deadline exceeded",
			wantClass:          ClassDownstreamDependency,
			wantClassification: "Downstream timeout. Safe to retry once dependency recovers.",
			wantVerdict:        VerdictSafeToRetry,
		},
		{
			name:               "connection refused after IP normalization",
			normalizedError:    "dial tcp [IP]: connect: connection refused",
			wantClass:          ClassDownstreamDependency,
			wantClassification: "Downstream refused connection. Service may be down.",
			wantVerdict:        VerdictSafeToRetry,
		},
		{
			name:               "matching is case-insensitive",
			normalizedError:    "JSON: CANNOT UNMARSHAL string into Go struct field Order.Amount",
			wantClass:          ClassPoisonMessage,
			wantClassification: "Deserialization failure. Payload is structurally invalid.",
			wantVerdict:        VerdictDoNotRetry,
		},
		{
			name:               "validation beats everything after it",
			normalizedError:    "validation failed: required field 'email' is missing",
			wantClass:          ClassPoisonMessage,
			wantClassification: "Schema validation failure. Payload missing mandatory fields.",
			wantVerdict:        VerdictDoNotRetry,
		},
		{
			name:               "invalid character from a JSON decoder",
			normalizedError:    "invalid character 'x' looking for beginning of value",
			wantClass:          ClassPoisonMessage,
			wantClassification: "Malformed input. Cannot be parsed by any consumer version.",
			wantVerdict:        VerdictDoNotRetry,
		},
		{
			name:               "IAM AccessDeniedException",
			normalizedError:    "AccessDeniedException: User: arn:aws:iam::123456789012:role/x is not authorized to perform: ses:SendEmail",
			wantClass:          ClassAuthFailure,
			wantClassification: "Authentication or authorization failure. Check credentials and permissions.",
			wantVerdict:        VerdictNeedsInvestigation,
		},
		{
			name:               "expired certificate",
			normalizedError:    "x509: certificate has expired or is not yet valid",
			wantClass:          ClassConfigurationError,
			wantClassification: "TLS/certificate error. Check certificate chain and expiry.",
			wantVerdict:        VerdictNeedsInvestigation,
		},
		{
			name:               "dns lookup failure",
			normalizedError:    "dial tcp: lookup smtp.mail.example.invalid on [IP]: no such host",
			wantClass:          ClassConfigurationError,
			wantClassification: "DNS resolution failure. Host does not exist or is misconfigured.",
			wantVerdict:        VerdictDoNotRetry,
		},
		{
			name:               "connection reset by peer",
			normalizedError:    "read tcp [IP]->[IP]: read: connection reset by peer",
			wantClass:          ClassNetworkError,
			wantClassification: "Network-level connection failure. Connection dropped mid-flight.",
			wantVerdict:        VerdictRetryWithBackoff,
		},
		{
			name:               "disk full",
			normalizedError:    "write /var/log/archive.tar.gz: no space left on device",
			wantClass:          ClassResourceExhaustion,
			wantClassification: "Disk space exhaustion. Free storage on the consumer host.",
			wantVerdict:        VerdictNeedsInvestigation,
		},
		{
			name:               "file descriptors",
			normalizedError:    "accept tcp: too many open files",
			wantClass:          ClassResourceExhaustion,
			wantClassification: "File descriptor exhaustion. Consumer process exceeded ulimit.",
			wantVerdict:        VerdictNeedsInvestigation,
		},
		{
			name:               "throttling",
			normalizedError:    "ProvisionedThroughputExceededException: Throttled",
			wantClass:          ClassTransientError,
			wantClassification: "Rate limited by upstream. Retry with exponential backoff.",
			wantVerdict:        VerdictRetryWithBackoff,
		},
		{
			name:               "empty error text",
			normalizedError:    "",
			wantClass:          ClassUnknown,
			wantClassification: "No matching rule. Manual investigation required.",
			wantVerdict:        VerdictNeedsInvestigation,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := c.classify(tt.normalizedError)
			if got.class != tt.wantClass {
				t.Errorf("class = %q, want %q", got.class, tt.wantClass)
			}
			if got.classification != tt.wantClassification {
				t.Errorf("classification = %q, want %q", got.classification, tt.wantClassification)
			}
			if got.retryVerdict != tt.wantVerdict {
				t.Errorf("verdict = %q, want %q", got.retryVerdict, tt.wantVerdict)
			}
		})
	}
}
