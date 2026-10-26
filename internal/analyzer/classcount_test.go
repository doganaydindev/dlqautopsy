package analyzer

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/doganaydindev/dlqautopsy/internal/model"
	"github.com/doganaydindev/dlqautopsy/internal/storage"
)

type row struct {
	class, cause, severity, verdict string
	n                               int
}

func seedRows(t *testing.T, db *storage.DB, run string, rows []row) {
	t.Helper()
	now := time.Date(2026, 2, 24, 10, 0, 0, 0, time.UTC)
	id := 0
	for _, r := range rows {
		for i := 0; i < r.n; i++ {
			id++
			ev := model.RawEvent{ID: fmt.Sprintf("m-%d", id), Body: `{"error":"x"}`, Provider: "SQS", FirstSeen: now, LastSeen: now, ReceiveCount: 1}
			if err := db.InsertRawEvent(run, ev); err != nil {
				t.Fatal(err)
			}
			fr := model.FailureRecord{
				RawEventID: ev.ID, ErrorSignature: fmt.Sprintf("sig-%s", r.cause), NormalizedError: "x",
				FailureClass: r.class, Classification: r.cause, Severity: r.severity, RetryVerdict: r.verdict,
			}
			if err := db.InsertFailureRecord(run, fr); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// A failure class with several causes is one class, not several. The report
// used to say "15 failure classes" for 7 classes.
func TestClassCountsAreDistinctClasses(t *testing.T) {
	db, err := storage.NewDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	seedRows(t, db, "r", []row{
		{"Downstream Dependency", "timeout", "warning", "safe_to_retry", 4},
		{"Downstream Dependency", "http 5xx", "warning", "safe_to_retry", 3},
		{"Downstream Dependency", "refused", "warning", "safe_to_retry", 2},
		{"Configuration Error", "dns", "critical", "do_not_retry", 2},
		{"Configuration Error", "tls", "critical", "needs_investigation", 1},
		{"Poison Message", "unmarshal", "critical", "do_not_retry", 1},
	})

	s, err := NewAnalyzer(db, "r").Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.TotalClasses != 3 {
		t.Errorf("TotalClasses = %d, want 3", s.TotalClasses)
	}
	if s.TotalGroups != 6 {
		t.Errorf("TotalGroups = %d, want 6", s.TotalGroups)
	}
	if s.CriticalCount != 2 {
		t.Errorf("CriticalCount = %d, want 2 (Configuration Error, Poison Message)", s.CriticalCount)
	}
	if s.SafeToRetryCount != 1 || s.DoNotRetryCount != 2 {
		t.Errorf("SafeToRetryCount=%d DoNotRetryCount=%d, want 1 and 2", s.SafeToRetryCount, s.DoNotRetryCount)
	}
	if !strings.HasPrefix(s.ExecutiveSummary, "13 messages across 3 failure classes, split into 6 groups by cause (window: ") {
		t.Errorf("unexpected executive summary: %s", s.ExecutiveSummary)
	}
}

func TestExecutiveSummarySingular(t *testing.T) {
	a := &Analyzer{}
	got := a.buildExecutiveSummary(&ReportSummary{
		TotalMessages: 1, TotalClasses: 1, TotalGroups: 1, WindowDuration: "< 1s",
		Timelines: []Timeline{{FailureClass: "Poison Message", Count: 1, Percentage: 100}},
	})
	if !strings.HasPrefix(got, "1 message across 1 failure class (window: < 1s).") {
		t.Errorf("unexpected singular summary: %s", got)
	}
	if strings.Contains(got, "split into") {
		t.Errorf("should not mention groups when there is one per class: %s", got)
	}
}

func TestPluralize(t *testing.T) {
	for _, tt := range []struct {
		n    int
		want string
	}{{0, "0 classes"}, {1, "1 class"}, {2, "2 classes"}} {
		if got := Pluralize(tt.n, "class", "classes"); got != tt.want {
			t.Errorf("Pluralize(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

// Rows with the same count are ordered by class name, then cause, so two
// report invocations over the same data print the same thing.
func TestTimelineOrderBreaksTiesByName(t *testing.T) {
	db, err := storage.NewDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	seedRows(t, db, "r", []row{
		{"Zeta", "b", "info", "safe_to_retry", 2},
		{"Alpha", "z", "info", "safe_to_retry", 2},
		{"Alpha", "a", "info", "safe_to_retry", 2},
		{"Big", "a", "info", "safe_to_retry", 5},
	})
	s, err := NewAnalyzer(db, "r").Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, tl := range s.Timelines {
		got = append(got, tl.FailureClass+"/"+tl.RecommendedAction)
	}
	want := "Big/a,Alpha/a,Alpha/z,Zeta/b"
	if strings.Join(got, ",") != want {
		t.Errorf("order = %s, want %s", strings.Join(got, ","), want)
	}

	sums, err := db.GetClassSummaries("r")
	if err != nil {
		t.Fatal(err)
	}
	got = got[:0]
	for _, c := range sums {
		got = append(got, c.FailureClass+"/"+c.Classification)
	}
	if strings.Join(got, ",") != want {
		t.Errorf("GetClassSummaries order = %s, want %s", strings.Join(got, ","), want)
	}
}
