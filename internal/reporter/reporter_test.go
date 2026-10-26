package reporter

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/doganaydindev/dlqautopsy/internal/analyzer"
	"github.com/doganaydindev/dlqautopsy/internal/storage"
)

// Helper to capture stdout and restore it
func captureStdout(f func()) string {
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	f()

	w.Close()
	os.Stdout = old
	var buf bytes.Buffer
	io.Copy(&buf, r)
	return buf.String()
}

func buildDummySummary() *analyzer.ReportSummary {
	now := time.Now()
	return &analyzer.ReportSummary{
		RunName:          "test-run",
		TotalMessages:    100,
		TotalClasses:     2,
		CriticalCount:    1,
		SafeToRetryCount: 1,
		SafeToRetryMsgs:  60,
		DoNotRetryCount:  1,
		DoNotRetryMsgs:   30,
		WindowStart:      now,
		WindowEnd:        now.Add(2 * time.Hour),
		WindowDuration:   "2h",
		ExecutiveSummary: "Test executive summary.",
		Triage: []analyzer.TriageGroup{
			{
				Label: "Immediate Action Required",
				Classes: []analyzer.Timeline{
					{
						FailureClass:      "Poison",
						Severity:          "critical",
						RetryVerdict:      "do_not_retry",
						Count:             30,
						Percentage:        30.0,
						UniqueSignatures:  2,
						FirstOccurrence:   now,
						LastOccurrence:    now,
						RecommendedAction: "Fix poison",
					},
				},
				Messages: 30,
			},
		},
		Timelines: []analyzer.Timeline{
			{
				FailureClass:      "Timeout",
				Severity:          "warning",
				RetryVerdict:      "safe_to_retry",
				Count:             70,
				Percentage:        70.0,
				UniqueSignatures:  1,
				FirstOccurrence:   now,
				LastOccurrence:    now,
				Duration:          "2h",
				RetryPattern:      "Exhaustion",
				BusiestHourCount:  10,
				RecommendedAction: "Retry it later",
				SampleError:       "long error text to test truncation",
				PayloadShapes:     []string{"shape1", "shape2", "shape3", "shape4"},
				BurstAnalysis:     "High rate",
			},
		},
	}
}

func TestWriteJSON(t *testing.T) {
	summary := buildDummySummary()

	out := captureStdout(func() {
		WriteJSON(summary)
	})

	if !strings.Contains(out, `"run_name": "test-run"`) {
		t.Errorf("expected string in json output, got %s", out)
	}
	if !strings.Contains(out, `"total_messages": 100`) {
		t.Errorf("expected string in json output")
	}
}

func TestWriteText(t *testing.T) {
	summary := buildDummySummary()

	out := captureStdout(func() {
		WriteText(summary)
	})

	if !strings.Contains(out, "test-run") {
		t.Errorf("expected test-run in output")
	}
	if !strings.Contains(out, "DEAD LETTER AUTOPSY REPORT") {
		t.Errorf("missing header")
	}
	if !strings.Contains(out, "Test executive summary") {
		t.Errorf("missing executive summary")
	}
	if !strings.Contains(out, "Poison") {
		t.Errorf("missing class name in triage")
	}
	if !strings.Contains(out, "Timeout") {
		t.Errorf("missing class name in detail")
	}
}

func TestWriteDetailText(t *testing.T) {
	records := []storage.DetailRecord{
		{
			MessageID:       "msg-1",
			Body:            "this is a sample body which could be very long over 300 chars to test truncating later though this is short",
			ReceiveCount:    1,
			FirstSeen:       time.Now(),
			LastSeen:        time.Now(),
			ErrorSignature:  "0123456789abcdef0123456789", // test truncation > 16
			NormalizedError: "long normalized error",
			PayloadShape:    "error.msg",
			FailureClass:    "Poison",
			Severity:        "critical",
			RetryVerdict:    "do_not_retry",
		},
	}

	out := captureStdout(func() {
		WriteDetailText(records, "Poison")
	})

	if !strings.Contains(out, "DETAIL: Poison") {
		t.Errorf("missing header")
	}
	if !strings.Contains(out, "msg-1") {
		t.Errorf("missing message id")
	}
	if !strings.Contains(out, "0123456789abcdef") {
		t.Errorf("missing signature truncated")
	}
	if !strings.Contains(out, "error.msg") {
		t.Errorf("missing payload shape")
	}
}

func TestWriteDetailJSON(t *testing.T) {
	records := []storage.DetailRecord{{MessageID: "msg-123"}}

	out := captureStdout(func() {
		WriteDetailJSON(records)
	})

	if !strings.Contains(out, `"MessageID": "msg-123"`) {
		t.Errorf("expected output, got %s", out)
	}
}

func TestWriteDiffText(t *testing.T) {
	baseClasses := []storage.DiffClassSummary{
		{FailureClass: "A", Count: 10, Severity: "warning"},
		{FailureClass: "B", Count: 20, Severity: "critical"},
	}
	compareClasses := []storage.DiffClassSummary{
		{FailureClass: "A", Count: 15, Severity: "warning"}, // Worsening
		{FailureClass: "C", Count: 5, Severity: "info"},     // New
		// B is resolved
	}

	out := captureStdout(func() {
		WriteDiffText("run-1", "run-2", baseClasses, compareClasses)
	})

	if !strings.Contains(out, "NEW FAILURE CLASSES") || !strings.Contains(out, "C") {
		t.Errorf("did not report new class")
	}
	if !strings.Contains(out, "RESOLVED") || !strings.Contains(out, "B") {
		t.Errorf("did not report resolved class")
	}
	if !strings.Contains(out, "WORSENING") || !strings.Contains(out, "A") {
		t.Errorf("did not report worsening class")
	}

	// Test improving
	baseClasses = []storage.DiffClassSummary{{FailureClass: "D", Count: 50}}
	compareClasses = []storage.DiffClassSummary{{FailureClass: "D", Count: 10}}
	out = captureStdout(func() {
		WriteDiffText("run-1", "run-2", baseClasses, compareClasses)
	})
	if !strings.Contains(out, "IMPROVING") || !strings.Contains(out, "D") {
		t.Errorf("did not report improving class")
	}

	// Test no diff
	out = captureStdout(func() {
		WriteDiffText("run-1", "run-2", baseClasses, baseClasses)
	})
	if !strings.Contains(out, "No differences found") {
		t.Errorf("should report no differences")
	}
}

func TestWriteDiffJSON(t *testing.T) {
	baseClasses := []storage.DiffClassSummary{{FailureClass: "A", Count: 10}}
	compareClasses := []storage.DiffClassSummary{{FailureClass: "A", Count: 15}}

	out := captureStdout(func() {
		WriteDiffJSON("run-1", "run-2", baseClasses, compareClasses)
	})

	if !strings.Contains(out, `"base_run": "run-1"`) {
		t.Errorf("missing base run")
	}
}

func TestWriteClassifySummary(t *testing.T) {
	// Let's use an empty one because it requires *storage.DB,
	// but we can pass an empty DB if we open it
	db, _ := storage.NewDB(":memory:")
	defer db.Close()

	out := captureStdout(func() {
		WriteClassifySummary(db, "test-run")
	})

	if !strings.Contains(out, "Classification Complete") {
		t.Errorf("missing completion msg")
	}
}

func TestWriteRunsText(t *testing.T) {
	now := time.Now()
	runs := []storage.RunInfo{
		{Name: "run1", MessageCount: 10, FirstSeen: now, LastSeen: now},
	}

	out := captureStdout(func() {
		WriteRunsText(runs, "test.db")
	})

	if !strings.Contains(out, "Stored Inspection Runs") {
		t.Errorf("missing header")
	}
	if !strings.Contains(out, "run1") {
		t.Errorf("missing run1")
	}

	// Empty
	out = captureStdout(func() {
		WriteRunsText([]storage.RunInfo{}, "test.db")
	})
	if !strings.Contains(out, "No runs found") {
		t.Errorf("missing empty msg")
	}
}

func TestDashboardClassWording(t *testing.T) {
	s := buildDummySummary()
	s.TotalClasses, s.TotalGroups, s.CriticalCount = 7, 15, 1
	out := captureStdout(func() { WriteText(s) })
	if !strings.Contains(out, "7 (15 groups)") {
		t.Errorf("text dashboard should show classes and groups:\n%s", out)
	}
	if !strings.Contains(out, "1 class\n") || strings.Contains(out, "1 classes") {
		t.Errorf("text dashboard should say \"1 class\":\n%s", out)
	}
	md := captureStdout(func() { WriteMarkdown(s) })
	if !strings.Contains(md, "| Failure Classes | **7 (15 groups)** |") || !strings.Contains(md, "| Critical | **1** class |") {
		t.Errorf("markdown dashboard wording wrong:\n%s", md)
	}
}

func TestIndentContinuation(t *testing.T) {
	if got := indentContinuation("one line", 4); got != "one line" {
		t.Errorf("single line changed: %q", got)
	}
	got := indentContinuation("panic: boom\n\tgoroutine 1 [running]:\nmain.main()\n", 4)
	want := "panic: boom\n    \tgoroutine 1 [running]:\n    main.main()"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Multi-line sample errors must not print lines starting at column 0.
func TestWriteTextIndentsMultiLineErrors(t *testing.T) {
	s := buildDummySummary()
	s.Timelines[0].SampleError = "runtime error: nil pointer dereference\n\tgoroutine 42 [running]:\nmain.(*H).Send([ADDR])"
	out := captureStdout(func() { WriteText(s) })
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "main.") || strings.HasPrefix(line, "\tgoroutine") {
			t.Errorf("continuation line not indented: %q", line)
		}
	}
	if !strings.Contains(out, "\n                      main.(*H).Send([ADDR])") {
		t.Errorf("continuation line not aligned under the sample error:\n%s", out)
	}
}

func TestSignatureCountWording(t *testing.T) {
	s := buildDummySummary()
	second := s.Timelines[0]
	second.FailureClass = "Second"
	second.UniqueSignatures = 3
	s.Timelines[0].UniqueSignatures = 1
	s.Timelines = append(s.Timelines, second)
	for name, out := range map[string]string{
		"text":     captureStdout(func() { WriteText(s) }),
		"markdown": captureStdout(func() { WriteMarkdown(s) }),
	} {
		if strings.Contains(out, "1 unique error hashes") {
			t.Errorf("%s: says \"1 unique error hashes\"", name)
		}
		if !strings.Contains(out, "1 unique error hash") || !strings.Contains(out, "3 unique error hashes") {
			t.Errorf("%s: missing singular/plural signature counts:\n%s", name, out)
		}
	}
}
