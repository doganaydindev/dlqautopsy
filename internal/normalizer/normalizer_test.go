package normalizer

import (
	"context"
	"testing"
	"time"

	"github.com/doganaydindev/dlqautopsy/internal/model"
	"github.com/doganaydindev/dlqautopsy/internal/storage"
)

func TestExtractErrorAndShape(t *testing.T) {
	n := &Normalizer{}

	tests := []struct {
		name      string
		rawBody   string
		wantError string
		wantShape string
	}{
		{
			name:      "Not JSON",
			rawBody:   "Just a plain text error message",
			wantError: "Just a plain text error message",
			wantShape: "",
		},
		{
			name:      "JSON with 'error' field",
			rawBody:   `{"id": 123, "error": "timeout occurred", "user": {"name": "Test"}}`,
			wantError: "timeout occurred",
			wantShape: "error,id,user.name",
		},
		{
			name:      "JSON with nested error object",
			rawBody:   `{"errorMessage": {"message": "nested error text", "code": 500}}`,
			wantError: "nested error text",
			wantShape: "errorMessage.code,errorMessage.message",
		},
		{
			name:      "JSON error object without message field",
			rawBody:   `{"err": {"code": 123, "reason": "unknown"}}`,
			wantError: `{"code":123,"reason":"unknown"}`,
			wantShape: "err.code,err.reason",
		},
		{
			name:      "JSON with no known error fields",
			rawBody:   `{"action": "test", "status": "failed"}`,
			wantError: `{"action": "test", "status": "failed"}`,
			wantShape: "action,status",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotError, gotShape := n.extractErrorAndShape(tt.rawBody)
			if gotError != tt.wantError {
				t.Errorf("extractErrorAndShape() gotError = %v, want %v", gotError, tt.wantError)
			}
			if gotShape != tt.wantShape {
				t.Errorf("extractErrorAndShape() gotShape = %v, want %v", gotShape, tt.wantShape)
			}
		})
	}
}

func TestNormalize(t *testing.T) {
	n := &Normalizer{}

	tests := []struct {
		name      string
		errorText string
		wantNorm  string
	}{
		{
			name:      "Strip memory addresses",
			errorText: "panic at 0xc0001a2000 and 0x0",
			wantNorm:  "panic at [ADDR] and [ADDR]",
		},
		{
			name:      "Strip UUIDs",
			errorText: "user a1b2c3d4-e5f6-7890-abcd-ef1234567890 not found",
			wantNorm:  "user [UUID] not found",
		},
		{
			name:      "Strip timestamps",
			errorText: "timeout at 2026-02-24T10:15:22Z or 2026-02-24 10:15:22",
			wantNorm:  "timeout at [TIMESTAMP] or [TIMESTAMP]",
		},
		{
			name:      "Strip IPs",
			errorText: "connection refused from 192.168.1.1 or 10.0.0.1:8080",
			wantNorm:  "connection refused from [IP] or [IP]",
		},
		{
			name:      "Normalize Go file:line",
			errorText: "main.go:123 failed",
			wantNorm:  "main.go:[LINE] failed",
		},
		{
			name:      "Combined",
			errorText: "error at 2026-02-24T10:15:22Z from 10.0.0.1:8080 uuid a1b2c3d4-e5f6-7890-abcd-ef1234567890 file main.go:88 addr 0x123abc",
			wantNorm:  "error at [TIMESTAMP] from [IP] uuid [UUID] file main.go:[LINE] addr [ADDR]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotNorm, gotSignature := n.normalize(tt.errorText)
			if gotNorm != tt.wantNorm {
				t.Errorf("normalize() gotNorm = %v, want %v", gotNorm, tt.wantNorm)
			}
			if gotSignature == "" {
				t.Errorf("normalize() signature should not be empty")
			}
		})
	}
}

func TestNormalizer_Run(t *testing.T) {
	// Setup in-memory DB
	db, err := storage.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create in-memory db: %v", err)
	}
	defer db.Close()

	runName := "test-run"
	n := NewNormalizer(db, runName)

	// Insert some raw events
	rawEvent := model.RawEvent{
		ID:                "msg-1",
		Body:              `{"error": "timeout at 2026-02-24T10:15:22Z"}`,
		Provider:          "test",
		Attributes:        nil,
		FirstSeen:         time.Now(),
		LastSeen:          time.Now(),
		ReceiveCount:      1,
		OriginalQueueName: "q1",
	}

	if err := db.InsertRawEvent(runName, rawEvent); err != nil {
		t.Fatalf("failed to insert raw event: %v", err)
	}

	// Run normalizer
	err = n.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	// Verify that a failure record was created
	unclassified, err := db.GetUnclassifiedRecords(runName)
	if err != nil {
		t.Fatalf("failed to get unclassified records: %v", err)
	}

	if len(unclassified) != 1 {
		t.Fatalf("expected 1 unclassified record, got %d", len(unclassified))
	}

	fr := unclassified[0]
	if fr.RawEventID != "msg-1" {
		t.Errorf("expected RawEventID msg-1, got %s", fr.RawEventID)
	}
	if fr.PayloadShape != "error" {
		t.Errorf("expected payload shape 'error', got %s", fr.PayloadShape)
	}
	expectedNorm := "timeout at [TIMESTAMP]"
	if fr.NormalizedError != expectedNorm {
		t.Errorf("expected NormalizedError '%s', got '%s'", expectedNorm, fr.NormalizedError)
	}
}
