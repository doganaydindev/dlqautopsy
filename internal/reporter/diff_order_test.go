package reporter

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/doganaydindev/dlqautopsy/internal/storage"
)

func diffFixture() (base, compare []storage.DiffClassSummary) {
	base = []storage.DiffClassSummary{
		{FailureClass: "Zeta", Count: 5, Severity: "warning"},     // resolved
		{FailureClass: "Alpha", Count: 5, Severity: "warning"},    // resolved
		{FailureClass: "Big Gone", Count: 9, Severity: "warning"}, // resolved
		{FailureClass: "Up Small", Count: 10, Severity: "warning"},
		{FailureClass: "Up Big", Count: 10, Severity: "critical"},
		{FailureClass: "Down Small", Count: 10, Severity: "info"},
		{FailureClass: "Down Big", Count: 10, Severity: "info"},
		{FailureClass: "Same", Count: 3, Severity: "info"},
	}
	compare = []storage.DiffClassSummary{
		{FailureClass: "New B", Count: 2, Severity: "info"},
		{FailureClass: "New A", Count: 2, Severity: "info"},
		{FailureClass: "New Big", Count: 7, Severity: "critical"},
		{FailureClass: "Up Small", Count: 11, Severity: "warning"},
		{FailureClass: "Up Big", Count: 30, Severity: "critical"},
		{FailureClass: "Down Small", Count: 9, Severity: "info"},
		{FailureClass: "Down Big", Count: 1, Severity: "info"},
		{FailureClass: "Same", Count: 3, Severity: "info"},
	}
	return base, compare
}

func names(s []storage.DiffClassSummary) []string {
	var out []string
	for _, c := range s {
		out = append(out, c.FailureClass)
	}
	return out
}

func deltaNames(s []ClassDelta) []string {
	var out []string
	for _, c := range s {
		out = append(out, c.FailureClass)
	}
	return out
}

func TestComputeDiffOrderIsStable(t *testing.T) {
	base, compare := diffFixture()
	want := struct{ newC, resolved, worse, better string }{
		"New Big,New A,New B",
		"Big Gone,Alpha,Zeta",
		"Up Big,Up Small",
		"Down Big,Down Small",
	}
	// Map iteration is random; run many times to catch unstable ordering.
	for i := 0; i < 200; i++ {
		d := computeDiff(base, compare)
		got := struct{ newC, resolved, worse, better string }{
			strings.Join(names(d.New), ","),
			strings.Join(names(d.Resolved), ","),
			strings.Join(deltaNames(d.Worsening), ","),
			strings.Join(deltaNames(d.Improving), ","),
		}
		if got != want {
			t.Fatalf("iteration %d: got %+v, want %+v", i, got, want)
		}
	}
}

func TestDiffTextAndMarkdownAreDeterministic(t *testing.T) {
	base, compare := diffFixture()
	firstText := captureStdout(func() { WriteDiffText("a", "b", base, compare) })
	var firstMD bytes.Buffer
	writeDiffMarkdown(&firstMD, "a", "b", base, compare)
	for i := 0; i < 50; i++ {
		if out := captureStdout(func() { WriteDiffText("a", "b", base, compare) }); out != firstText {
			t.Fatalf("text diff output changed between calls:\n%s\n---\n%s", firstText, out)
		}
		var md bytes.Buffer
		writeDiffMarkdown(&md, "a", "b", base, compare)
		if md.String() != firstMD.String() {
			t.Fatalf("markdown diff output changed between calls")
		}
	}
}

func TestWriteDiffJSONHasComputedSections(t *testing.T) {
	base, compare := diffFixture()
	out := captureStdout(func() { WriteDiffJSON("a", "b", base, compare) })

	var got struct {
		New       []storage.DiffClassSummary `json:"new_classes"`
		Resolved  []storage.DiffClassSummary `json:"resolved_classes"`
		Worsening []ClassDelta               `json:"worsening"`
		Improving []ClassDelta               `json:"improving"`
		Base      []storage.DiffClassSummary `json:"base_classes"`
		Compare   []storage.DiffClassSummary `json:"compare_classes"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if len(got.New) != 3 || len(got.Resolved) != 3 || len(got.Worsening) != 2 || len(got.Improving) != 2 {
		t.Fatalf("unexpected section sizes: new=%d resolved=%d worse=%d better=%d",
			len(got.New), len(got.Resolved), len(got.Worsening), len(got.Improving))
	}
	w := got.Worsening[0]
	if w.FailureClass != "Up Big" || w.BaseCount != 10 || w.CompareCount != 30 || w.Change != 20 || w.ChangePct != 200 {
		t.Errorf("unexpected worsening[0]: %+v", w)
	}
	if len(got.Base) != len(base) || len(got.Compare) != len(compare) {
		t.Errorf("raw class lists not kept")
	}

	// No differences: sections are empty arrays, not null.
	out = captureStdout(func() { WriteDiffJSON("a", "a", base, base) })
	for _, key := range []string{`"new_classes": []`, `"resolved_classes": []`, `"worsening": []`, `"improving": []`} {
		if !strings.Contains(out, key) {
			t.Errorf("missing %s in:\n%s", key, out)
		}
	}
}
