package analyzer

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/doganaydindev/dlqautopsy/internal/model"
	"github.com/doganaydindev/dlqautopsy/internal/storage"
)

func TestAnalyzeBurst(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		count    int
		want     string
	}{
		{"single message", time.Minute, 1, ""},
		{"short window", 14 * time.Second, 35, "35 messages sent and ingested within 14s (earliest SentTimestamp to latest ingestion)"},
		{"sub-second", 300 * time.Millisecond, 2, "2 messages sent and ingested within less than 1s (earliest SentTimestamp to latest ingestion)"},
		{"long window", 2 * time.Hour, 300, "300 messages sent and ingested within 2h 0m 0s (earliest SentTimestamp to latest ingestion)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := analyzeBurst(tt.duration, tt.count)
			if got != tt.want {
				t.Errorf("analyzeBurst(%s, %d) = %q, want %q", tt.duration, tt.count, got, tt.want)
			}
		})
	}
}

// The window is based on ingestion time, so the text must not claim a cause.
func TestAnalyzeBurstMakesNoCausalClaims(t *testing.T) {
	for _, d := range []time.Duration{time.Second, 5 * time.Minute, 30 * time.Minute, 3 * time.Hour} {
		got := strings.ToLower(analyzeBurst(d, 500))
		for _, banned := range []string{"likely", "single event", "deploy", "outage", "incident", "burst"} {
			if strings.Contains(got, banned) {
				t.Errorf("analyzeBurst(%s) = %q contains %q", d, got, banned)
			}
		}
	}
}

func TestClassifyRetryPattern(t *testing.T) {
	tests := []struct {
		name       string
		instant    int
		exhaustion int
		total      int
		want       string
	}{
		{"Zero total", 0, 0, 0, "Unknown"},
		{"Instant Death", 80, 10, 100, "Instant Death"},
		{"Exhaustion", 10, 80, 100, "Exhaustion"},
		{"Mixed", 40, 40, 100, "Mixed"},
		{"Mostly Instant Death", 50, 20, 100, "Mostly Instant Death"},
		{"Mostly Exhaustion", 20, 50, 100, "Mostly Exhaustion"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyRetryPattern(tt.instant, tt.exhaustion, tt.total)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{500 * time.Millisecond, "< 1s"},
		{5 * time.Second, "5s"},
		{65 * time.Second, "1m 5s"},
		{3665 * time.Second, "1h 1m 5s"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := formatDuration(tt.d)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseSQLiteTime(t *testing.T) {
	now := time.Now()
	if parseSQLiteTime(now) != now {
		t.Errorf("expected time.Time val to pass through")
	}

	validStrings := []string{
		"2026-02-24T10:15:22Z",
		"2026-02-24 10:15:22-07:00",
	}

	for _, s := range validStrings {
		parsed := parseSQLiteTime(s)
		if parsed.IsZero() {
			t.Errorf("failed to parse %q", s)
		}
	}

	invalidData := 123
	if !parseSQLiteTime(invalidData).IsZero() {
		t.Errorf("expected zero time for invalid type")
	}
}

func TestBuildTriageGroups(t *testing.T) {
	a := &Analyzer{}
	timelines := []Timeline{
		{FailureClass: "A", RetryVerdict: "do_not_retry", Count: 10},
		{FailureClass: "B", RetryVerdict: "safe_to_retry", Count: 20},
		{FailureClass: "C", RetryVerdict: "needs_investigation", Count: 5},
	}
	groups := a.buildTriageGroups(timelines)

	if len(groups) != 3 {
		t.Fatalf("expected 3 groups, got %d", len(groups))
	}
	if groups[0].Label != "Immediate Action Required" || groups[0].Messages != 10 {
		t.Errorf("group 0 mismatch")
	}
	if groups[1].Label != "Safe to Retry" || groups[1].Messages != 20 {
		t.Errorf("group 1 mismatch")
	}
	if groups[2].Label != "Needs Investigation" || groups[2].Messages != 5 {
		t.Errorf("group 2 mismatch")
	}
}

func TestBuildExecutiveSummary(t *testing.T) {
	a := &Analyzer{}
	emptySummary := &ReportSummary{TotalMessages: 0}
	if got := a.buildExecutiveSummary(emptySummary); got != "No messages found for this run." {
		t.Errorf("unexpected empty summary: %s", got)
	}

	summary := &ReportSummary{
		TotalMessages:   100,
		TotalClasses:    2,
		WindowDuration:  "2h",
		SafeToRetryMsgs: 60,
		DoNotRetryMsgs:  30, // 10 left for investigate
		Timelines: []Timeline{
			{FailureClass: "TopIssue", Count: 60, Percentage: 60.0},
		},
	}

	execSum := a.buildExecutiveSummary(summary)
	if !strings.Contains(execSum, "100 messages across 2 failure classes") {
		t.Errorf("missing total msg string")
	}
	if !strings.Contains(execSum, "60% (60 messages) are safe to retry now") {
		t.Errorf("missing retry string")
	}
	if !strings.Contains(execSum, "30% (30 messages) should NOT be retried") {
		t.Errorf("missing do not retry string")
	}
	if !strings.Contains(execSum, "10% (10 messages) need manual investigation") {
		t.Errorf("missing investigate string")
	}
	if !strings.Contains(execSum, "Largest class: \"TopIssue\"") {
		t.Errorf("missing largest class string")
	}
}

func TestAnalyzer_Run(t *testing.T) {
	db, err := storage.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create in-memory db: %v", err)
	}
	defer db.Close()

	runName := "test-run"
	a := NewAnalyzer(db, runName)

	now := time.Now().Round(time.Second)

	// Insert raw events
	re1 := model.RawEvent{ID: "msg-1", Body: `{"error":"e1"}`, ReceiveCount: 1, FirstSeen: now, LastSeen: now}
	re2 := model.RawEvent{ID: "msg-2", Body: `{"error":"e1"}`, ReceiveCount: 5, FirstSeen: now.Add(time.Minute), LastSeen: now.Add(time.Minute)}
	re3 := model.RawEvent{ID: "msg-3", Body: `{"error":"e2"}`, ReceiveCount: 2, FirstSeen: now, LastSeen: now}

	db.InsertRawEvent(runName, re1)
	db.InsertRawEvent(runName, re2)
	db.InsertRawEvent(runName, re3)

	// Insert failure records
	fr1 := model.FailureRecord{
		RawEventID:      "msg-1",
		FailureClass:    "ClassA",
		Severity:        "critical",
		RetryVerdict:    "do_not_retry",
		ErrorSignature:  "sig1",
		NormalizedError: "e1",
		PayloadShape:    "error",
		Classification:  "A serious error",
	}
	fr2 := model.FailureRecord{
		RawEventID:      "msg-2",
		FailureClass:    "ClassA",
		Severity:        "critical",
		RetryVerdict:    "do_not_retry",
		ErrorSignature:  "sig1",
		NormalizedError: "e1",
		PayloadShape:    "error",
		Classification:  "A serious error",
	}
	fr3 := model.FailureRecord{
		RawEventID:      "msg-3",
		FailureClass:    "ClassB",
		Severity:        "warning",
		RetryVerdict:    "safe_to_retry",
		ErrorSignature:  "sig2",
		NormalizedError: "e2",
		Classification:  "A transient error",
	}

	db.InsertFailureRecord(runName, fr1)
	db.InsertFailureRecord(runName, fr2)
	db.InsertFailureRecord(runName, fr3)

	summary, err := a.Run(context.Background())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if summary.TotalMessages != 3 {
		t.Errorf("expected 3 total messages, got %d", summary.TotalMessages)
	}
	if summary.TotalClasses != 2 {
		t.Errorf("expected 2 total classes, got %d", summary.TotalClasses)
	}
	if summary.CriticalCount != 1 {
		t.Errorf("expected 1 critical class, got %d", summary.CriticalCount)
	}
	if summary.SafeToRetryMsgs != 1 {
		t.Errorf("expected 1 safe to retry message, got %d", summary.SafeToRetryMsgs)
	}
	if summary.DoNotRetryMsgs != 2 {
		t.Errorf("expected 2 do not retry messages, got %d", summary.DoNotRetryMsgs)
	}

	if len(summary.Timelines) != 2 {
		t.Fatalf("expected 2 timelines, got %d", len(summary.Timelines))
	}

	// First timeline should be ClassA (2 messages)
	t1 := summary.Timelines[0]
	if t1.FailureClass != "ClassA" {
		t.Errorf("expected ClassA timeline first, got %s", t1.FailureClass)
	}
	if t1.PeakHourlySpike != 2 {
		t.Errorf("expected peak hourly spike 2, got %d", t1.PeakHourlySpike)
	}
	if len(t1.PayloadShapes) != 1 || t1.PayloadShapes[0] != "error" {
		t.Errorf("expected 'error' payload shape, got %v", t1.PayloadShapes)
	}
}
