package reporter

import (
	"strings"
	"testing"

	"github.com/doganaydindev/dlqautopsy/internal/storage"
)

func TestWriteMarkdown(t *testing.T) {
	summary := buildDummySummary()

	out := captureStdout(func() {
		WriteMarkdown(summary)
	})

	if !strings.Contains(out, "# Dead Letter Autopsy Report") {
		t.Errorf("missing header")
	}
	if !strings.Contains(out, "test-run") {
		t.Errorf("missing run name")
	}
	if !strings.Contains(out, "Executive Summary") {
		t.Errorf("missing exec summary section")
	}
	if !strings.Contains(out, "Total Messages") {
		t.Errorf("missing table headers")
	}
}

func TestWriteDetailMarkdown(t *testing.T) {
	records := []storage.DetailRecord{
		{
			MessageID:      "msg1",
			ErrorSignature: "sig1",
			FailureClass:   "Timeout",
			Body:           "{\"status\":\"error\"}",
		},
	}

	out := captureStdout(func() {
		WriteDetailMarkdown(records, "Timeout")
	})

	if !strings.Contains(out, "# Detail: Timeout") {
		t.Errorf("missing detail markdown header")
	}
	if !strings.Contains(out, "msg1") {
		t.Errorf("missing record")
	}
	if !strings.Contains(out, "```json") {
		t.Errorf("expected json block for body")
	}
}

func TestWriteDiffMarkdown(t *testing.T) {
	baseClasses := []storage.DiffClassSummary{
		{FailureClass: "A", Count: 10, Severity: "warning"},
		{FailureClass: "B", Count: 20, Severity: "critical"}, // resolved
	}
	compareClasses := []storage.DiffClassSummary{
		{FailureClass: "A", Count: 15, Severity: "warning"}, // worsening
		{FailureClass: "C", Count: 5, Severity: "info"},     // new
	}

	out := captureStdout(func() {
		WriteDiffMarkdown("base", "compare", baseClasses, compareClasses)
	})

	if !strings.Contains(out, "# Run Comparison") {
		t.Errorf("missing diff markdown header")
	}
	if !strings.Contains(out, "## 🆕 New Failure Classes") {
		t.Errorf("missing new classes section")
	}
	if !strings.Contains(out, "## ✅ Resolved") {
		t.Errorf("missing resolved section")
	}
	if !strings.Contains(out, "## 📈 Worsening") {
		t.Errorf("missing worsening section")
	}
	if strings.Contains(out, "## 📉 Improving") {
		t.Errorf("should not have improving section")
	}

	// Test no change
	out = captureStdout(func() {
		WriteDiffMarkdown("b", "c", baseClasses, baseClasses)
	})
	if !strings.Contains(out, "✅ No differences found") {
		t.Errorf("expected no differences message")
	}
}
