package classifier

import (
	"context"
	"testing"
	"time"

	"github.com/doganaydindev/dlqautopsy/internal/model"
	"github.com/doganaydindev/dlqautopsy/internal/storage"
)

func TestClassify(t *testing.T) {
	c := &Classifier{}

	tests := []struct {
		name         string
		errorText    string
		wantClass    string
		wantSeverity string
		wantVerdict  string
	}{
		{
			name:         "Poison Message - Unmarshal",
			errorText:    "json: cannot unmarshal string into Go struct field",
			wantClass:    ClassPoisonMessage,
			wantSeverity: SeverityCritical,
			wantVerdict:  VerdictDoNotRetry,
		},
		{
			name:         "Poison Message - Index out of range",
			errorText:    "panic: runtime error: index out of range [1] with length 1",
			wantClass:    ClassPoisonMessage,
			wantSeverity: SeverityCritical,
			wantVerdict:  VerdictDoNotRetry,
		},
		{
			name:         "Auth Failure - 401",
			errorText:    "HTTP 401: token expired",
			wantClass:    ClassAuthFailure,
			wantSeverity: SeverityCritical,
			wantVerdict:  VerdictNeedsInvestigation,
		},
		{
			name:         "Configuration Error - DNS",
			errorText:    "dial tcp: lookup my-service.internal: no such host",
			wantClass:    ClassConfigurationError,
			wantSeverity: SeverityCritical,
			wantVerdict:  VerdictDoNotRetry,
		},
		{
			name:         "Downstream Dependency - Timeout",
			errorText:    "context deadline exceeded",
			wantClass:    ClassDownstreamDependency,
			wantSeverity: SeverityWarning,
			wantVerdict:  VerdictSafeToRetry,
		},
		{
			name:         "Network Error - Broken Pipe",
			errorText:    "write: broken pipe",
			wantClass:    ClassNetworkError,
			wantSeverity: SeverityWarning,
			wantVerdict:  VerdictRetryWithBackoff,
		},
		{
			name:         "Resource Exhaustion - OOM",
			errorText:    "fatal error: out of memory",
			wantClass:    ClassResourceExhaustion,
			wantSeverity: SeverityCritical,
			wantVerdict:  VerdictNeedsInvestigation,
		},
		{
			name:         "Transient Error - Rate Limit",
			errorText:    "429 Too Many Requests",
			wantClass:    ClassTransientError,
			wantSeverity: SeverityInfo,
			wantVerdict:  VerdictRetryWithBackoff,
		},
		{
			name:         "Unknown Error",
			errorText:    "some weird random error that doesn't match rules",
			wantClass:    ClassUnknown,
			wantSeverity: SeverityWarning,
			wantVerdict:  VerdictNeedsInvestigation,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := c.classify(tt.errorText)
			if got.class != tt.wantClass {
				t.Errorf("classify() got class = %v, want %v", got.class, tt.wantClass)
			}
			if got.severity != tt.wantSeverity {
				t.Errorf("classify() got severity = %v, want %v", got.severity, tt.wantSeverity)
			}
			if got.retryVerdict != tt.wantVerdict {
				t.Errorf("classify() got verdict = %v, want %v", got.retryVerdict, tt.wantVerdict)
			}
		})
	}
}

func TestClassifier_Run(t *testing.T) {
	db, err := storage.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create in-memory db: %v", err)
	}
	defer db.Close()

	runName := "test-run"
	c := NewClassifier(db, runName)

	// Insert raw events
	rawEvent1 := model.RawEvent{
		ID:           "msg-1",
		Body:         "body1",
		FirstSeen:    time.Now(),
		LastSeen:     time.Now(),
		ReceiveCount: 1,
	}
	rawEvent2 := model.RawEvent{
		ID:           "msg-2",
		Body:         "body2",
		FirstSeen:    time.Now(),
		LastSeen:     time.Now(),
		ReceiveCount: 1,
	}
	db.InsertRawEvent(runName, rawEvent1)
	db.InsertRawEvent(runName, rawEvent2)

	// Insert failure records without classes
	fr1 := model.FailureRecord{
		RawEventID:      "msg-1",
		ErrorSignature:  "ab12cd34ef56", // length needs to be >= 8 for ClassUnknown test
		NormalizedError: "timeout occurred",
	}
	fr2 := model.FailureRecord{
		RawEventID:      "msg-2",
		ErrorSignature:  "1234567890abcdef",
		NormalizedError: " completely weird error not matching anything",
	}
	db.InsertFailureRecord(runName, fr1)
	db.InsertFailureRecord(runName, fr2)

	err = c.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	// Verify classifications were applied
	// "msg-1" should be "Downstream Dependency"
	// "msg-2" should be "Unknown-12345678"
	var class1, class2 string
	query := "SELECT raw_event_id, failure_class FROM failure_records WHERE run_name = ?"
	rows, err := db.GetDB().Query(query, runName)
	if err != nil {
		t.Fatalf("failed to query records: %v", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var id, fc string
		rows.Scan(&id, &fc)
		if id == "msg-1" {
			class1 = fc
		} else if id == "msg-2" {
			class2 = fc
		}
		count++
	}

	if count != 2 {
		t.Fatalf("expected 2 failure records, got %d", count)
	}
	if class1 != ClassDownstreamDependency {
		t.Errorf("msg-1 classified as '%s', expected '%s'", class1, ClassDownstreamDependency)
	}
	if class2 != "Unknown-12345678" {
		t.Errorf("msg-2 classified as '%s', expected 'Unknown-12345678'", class2)
	}
}
