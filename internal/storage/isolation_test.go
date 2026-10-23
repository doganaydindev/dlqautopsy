package storage

import (
	"testing"
	"time"

	"github.com/doganaydindev/dlqautopsy/internal/model"
)

// The same SQS message id can be ingested under two run names without the
// runs bleeding into each other.
func TestRunIsolation(t *testing.T) {
	db := setupTestDB(t)
	now := time.Date(2026, 2, 24, 10, 0, 0, 0, time.UTC)

	ev := model.RawEvent{ID: "m-1", Body: "b", Provider: "SQS", FirstSeen: now, LastSeen: now, ReceiveCount: 1, OriginalQueueName: "q"}
	for _, run := range []string{"monday", "tuesday"} {
		if err := db.InsertRawEvent(run, ev); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.InsertFailureRecord("monday", model.FailureRecord{RawEventID: "m-1", ErrorSignature: "aaaa", NormalizedError: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateFailureClass("monday", "m-1", "Poison Message", "c", "critical", "do_not_retry"); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		run             string
		wantUnnormal    int
		wantClasses     int
		wantClassCounts int
	}{
		{run: "monday", wantUnnormal: 0, wantClasses: 1, wantClassCounts: 1},
		{run: "tuesday", wantUnnormal: 1, wantClasses: 0, wantClassCounts: 0},
		{run: "never-ingested", wantUnnormal: 0, wantClasses: 0, wantClassCounts: 0},
	}
	for _, tt := range tests {
		t.Run(tt.run, func(t *testing.T) {
			ev, err := db.GetUnnormalizedEvents(tt.run)
			if err != nil {
				t.Fatal(err)
			}
			if len(ev) != tt.wantUnnormal {
				t.Errorf("unnormalized = %d, want %d", len(ev), tt.wantUnnormal)
			}
			classes, err := db.ListFailureClasses(tt.run)
			if err != nil {
				t.Fatal(err)
			}
			if len(classes) != tt.wantClasses {
				t.Errorf("classes = %d, want %d", len(classes), tt.wantClasses)
			}
			sums, err := db.GetClassSummaries(tt.run)
			if err != nil {
				t.Fatal(err)
			}
			if len(sums) != tt.wantClassCounts {
				t.Errorf("class summaries = %d, want %d", len(sums), tt.wantClassCounts)
			}
		})
	}

	runs, err := db.GetRuns()
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 {
		t.Fatalf("GetRuns = %d runs, want 2", len(runs))
	}
}

// GetDetailsBySignature matches on a prefix, which is what the CLI's
// --signature flag documents.
func TestGetDetailsBySignature_Prefix(t *testing.T) {
	db := setupTestDB(t)
	now := time.Date(2026, 2, 24, 10, 0, 0, 0, time.UTC)
	for i, sig := range []string{"e3b0c442aa", "e3b0c442bb", "ffff0000"} {
		id := string(rune('a' + i))
		if err := db.InsertRawEvent("r", model.RawEvent{ID: id, Body: "b", Provider: "SQS", FirstSeen: now, LastSeen: now, OriginalQueueName: "q"}); err != nil {
			t.Fatal(err)
		}
		if err := db.InsertFailureRecord("r", model.FailureRecord{RawEventID: id, ErrorSignature: sig, NormalizedError: "x", FailureClass: "C", Classification: "c"}); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		prefix string
		limit  int
		want   int
	}{
		{"e3b0c442", 20, 2},
		{"e3b0c442a", 20, 1},
		{"ffff", 20, 1},
		{"", 20, 3},
		{"e3b0c442", 1, 1},
		{"0000", 20, 0},
	}
	for _, tt := range tests {
		got, err := db.GetDetailsBySignature("r", tt.prefix, tt.limit)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != tt.want {
			t.Errorf("prefix %q limit %d: got %d records, want %d", tt.prefix, tt.limit, len(got), tt.want)
		}
	}
}
