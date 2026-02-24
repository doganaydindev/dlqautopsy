package classifier

import (
	"context"
	"fmt"
	"strings"

	"github.com/dgnydn/dlqautopsy/internal/storage"
)

// Failure class constants.
const (
	ClassPoisonMessage        = "Poison Message"
	ClassDownstreamDependency = "Downstream Dependency"
	ClassTransientError       = "Transient Error"
	ClassAuthFailure          = "Auth Failure"
	ClassResourceExhaustion   = "Resource Exhaustion"
	ClassConfigurationError   = "Configuration Error"
	ClassNetworkError         = "Network Error"
	ClassUnknown              = "Unknown"
)

// Severity levels.
const (
	SeverityCritical = "critical"
	SeverityWarning  = "warning"
	SeverityInfo     = "info"
)

// Retry verdicts.
const (
	VerdictDoNotRetry         = "do_not_retry"
	VerdictSafeToRetry        = "safe_to_retry"
	VerdictRetryWithBackoff   = "retry_with_backoff"
	VerdictNeedsInvestigation = "needs_investigation"
)

// classificationResult holds the full output of a classification decision.
type classificationResult struct {
	class          string
	classification string
	severity       string
	retryVerdict   string
}

// rule defines a deterministic matching rule.
type rule struct {
	keywords []string
	result   classificationResult
}

// rules are evaluated in order. First match wins.
var rules = []rule{
	// Poison Messages: structural payload problems that will never self-heal.
	{
		keywords: []string{"unmarshal", "json: cannot unmarshal", "json.syntaxerror"},
		result: classificationResult{
			class:          ClassPoisonMessage,
			classification: "Deserialization failure. Payload is structurally invalid.",
			severity:       SeverityCritical,
			retryVerdict:   VerdictDoNotRetry,
		},
	},
	{
		keywords: []string{"validation failed", "required field", "missing required"},
		result: classificationResult{
			class:          ClassPoisonMessage,
			classification: "Schema validation failure. Payload missing mandatory fields.",
			severity:       SeverityCritical,
			retryVerdict:   VerdictDoNotRetry,
		},
	},
	{
		keywords: []string{"syntax error", "unexpected token", "invalid character"},
		result: classificationResult{
			class:          ClassPoisonMessage,
			classification: "Malformed input. Cannot be parsed by any consumer version.",
			severity:       SeverityCritical,
			retryVerdict:   VerdictDoNotRetry,
		},
	},
	{
		keywords: []string{"index out of range", "nil pointer dereference", "nil pointer", "null pointer"},
		result: classificationResult{
			class:          ClassPoisonMessage,
			classification: "Runtime panic triggered by payload content. Code fix required.",
			severity:       SeverityCritical,
			retryVerdict:   VerdictDoNotRetry,
		},
	},
	// Auth Failures: credential or permission issues.
	{
		keywords: []string{"401", "403", "unauthorized", "forbidden", "access denied", "accessdeniedexception", "not authorized to perform", "invalid token", "token expired", "signature expired"},
		result: classificationResult{
			class:          ClassAuthFailure,
			classification: "Authentication or authorization failure. Check credentials and permissions.",
			severity:       SeverityCritical,
			retryVerdict:   VerdictNeedsInvestigation,
		},
	},
	// Configuration Errors: misconfiguration that requires human intervention.
	{
		keywords: []string{"no such host", "unknown host", "name resolution", "dns", "could not resolve"},
		result: classificationResult{
			class:          ClassConfigurationError,
			classification: "DNS resolution failure. Host does not exist or is misconfigured.",
			severity:       SeverityCritical,
			retryVerdict:   VerdictDoNotRetry,
		},
	},
	{
		keywords: []string{"certificate", "tls", "x509", "ssl handshake"},
		result: classificationResult{
			class:          ClassConfigurationError,
			classification: "TLS/certificate error. Check certificate chain and expiry.",
			severity:       SeverityCritical,
			retryVerdict:   VerdictNeedsInvestigation,
		},
	},
	// Downstream Dependency: external service or database issues, generally transient.
	{
		keywords: []string{"connection timeout", "timeout", "deadline exceeded", "context deadline"},
		result: classificationResult{
			class:          ClassDownstreamDependency,
			classification: "Downstream timeout. Safe to retry once dependency recovers.",
			severity:       SeverityWarning,
			retryVerdict:   VerdictSafeToRetry,
		},
	},
	{
		keywords: []string{"connection refused", "connect: connection refused"},
		result: classificationResult{
			class:          ClassDownstreamDependency,
			classification: "Downstream refused connection. Service may be down.",
			severity:       SeverityWarning,
			retryVerdict:   VerdictSafeToRetry,
		},
	},
	{
		keywords: []string{"503", "service unavailable", "502", "bad gateway", "504", "gateway timeout"},
		result: classificationResult{
			class:          ClassDownstreamDependency,
			classification: "Downstream HTTP error. Service temporarily unavailable.",
			severity:       SeverityWarning,
			retryVerdict:   VerdictSafeToRetry,
		},
	},
	{
		keywords: []string{"broken pipe", "connection reset", "connection closed", "eof"},
		result: classificationResult{
			class:          ClassNetworkError,
			classification: "Network-level connection failure. Connection dropped mid-flight.",
			severity:       SeverityWarning,
			retryVerdict:   VerdictRetryWithBackoff,
		},
	},
	// Resource Exhaustion: system-level pressure.
	{
		keywords: []string{"out of memory", "oom", "cannot allocate", "memory limit"},
		result: classificationResult{
			class:          ClassResourceExhaustion,
			classification: "Memory exhaustion. Consumer ran out of memory processing this message.",
			severity:       SeverityCritical,
			retryVerdict:   VerdictNeedsInvestigation,
		},
	},
	{
		keywords: []string{"disk full", "no space left", "disk quota"},
		result: classificationResult{
			class:          ClassResourceExhaustion,
			classification: "Disk space exhaustion. Free storage on the consumer host.",
			severity:       SeverityCritical,
			retryVerdict:   VerdictNeedsInvestigation,
		},
	},
	{
		keywords: []string{"too many open files", "file descriptor"},
		result: classificationResult{
			class:          ClassResourceExhaustion,
			classification: "File descriptor exhaustion. Consumer process exceeded ulimit.",
			severity:       SeverityCritical,
			retryVerdict:   VerdictNeedsInvestigation,
		},
	},
	// Transient Errors: concurrency or throttling issues.
	{
		keywords: []string{"deadlock", "lock wait timeout"},
		result: classificationResult{
			class:          ClassTransientError,
			classification: "Database deadlock or lock contention. Safe to retry with backoff.",
			severity:       SeverityWarning,
			retryVerdict:   VerdictRetryWithBackoff,
		},
	},
	{
		keywords: []string{"rate limit", "throttl", "429", "too many requests"},
		result: classificationResult{
			class:          ClassTransientError,
			classification: "Rate limited by upstream. Retry with exponential backoff.",
			severity:       SeverityInfo,
			retryVerdict:   VerdictRetryWithBackoff,
		},
	},
}

// Classifier groups messages into deterministic failure classes.
type Classifier struct {
	db      *storage.DB
	runName string
}

func NewClassifier(db *storage.DB, runName string) *Classifier {
	return &Classifier{db: db, runName: runName}
}

// Run processes normalized records and assigns Failure Classes.
func (c *Classifier) Run(ctx context.Context) error {
	records, err := c.db.GetUnclassifiedRecords(c.runName)
	if err != nil {
		return fmt.Errorf("failed to fetch unclassified records: %w", err)
	}

	fmt.Printf("Classifying %d records...\n", len(records))
	classifiedCount := 0

	for _, r := range records {
		result := c.classify(r.NormalizedError)

		class := result.class
		if class == ClassUnknown {
			class = fmt.Sprintf("Unknown-%s", r.ErrorSignature[:8])
		}

		err := c.db.UpdateFailureClass(c.runName, r.RawEventID, class, result.classification, result.severity, result.retryVerdict)
		if err != nil {
			fmt.Printf("Error updating failure class for %s: %v\n", r.RawEventID, err)
		} else {
			classifiedCount++
		}
	}

	fmt.Printf("Classification complete. %d records updated.\n", classifiedCount)
	return nil
}

// classify deterministically assigns a failure class by evaluating rules in order.
func (c *Classifier) classify(normalizedError string) classificationResult {
	lower := strings.ToLower(normalizedError)

	for _, r := range rules {
		for _, kw := range r.keywords {
			if strings.Contains(lower, kw) {
				return r.result
			}
		}
	}

	return classificationResult{
		class:          ClassUnknown,
		classification: "No matching rule. Manual investigation required.",
		severity:       SeverityWarning,
		retryVerdict:   VerdictNeedsInvestigation,
	}
}
