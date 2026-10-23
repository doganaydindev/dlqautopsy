package reporter

import (
	"testing"

	"github.com/doganaydindev/dlqautopsy/internal/storage"
)

func TestAggregateByClass(t *testing.T) {
	tests := []struct {
		name         string
		in           []storage.DiffClassSummary
		wantCounts   map[string]int
		wantSeverity map[string]string
	}{
		{
			name:       "empty",
			in:         nil,
			wantCounts: map[string]int{},
		},
		{
			// The same class can show up in several rows (one per
			// classification text); diff works on the class as a whole.
			name: "rows with the same class are summed",
			in: []storage.DiffClassSummary{
				{FailureClass: "Downstream Dependency", Classification: "timeout", Severity: "warning", Count: 7},
				{FailureClass: "Downstream Dependency", Classification: "refused", Severity: "warning", Count: 3},
				{FailureClass: "Poison Message", Severity: "critical", Count: 2},
			},
			wantCounts:   map[string]int{"Downstream Dependency": 10, "Poison Message": 2},
			wantSeverity: map[string]string{"Downstream Dependency": "warning", "Poison Message": "critical"},
		},
		{
			name: "worst severity is kept",
			in: []storage.DiffClassSummary{
				{FailureClass: "Transient Error", Severity: "info", Count: 4},
				{FailureClass: "Transient Error", Severity: "warning", Count: 1},
			},
			wantCounts:   map[string]int{"Transient Error": 5},
			wantSeverity: map[string]string{"Transient Error": "warning"},
		},
		{
			name: "a lower severity does not downgrade",
			in: []storage.DiffClassSummary{
				{FailureClass: "X", Severity: "critical", Count: 1},
				{FailureClass: "X", Severity: "info", Count: 1},
			},
			wantCounts:   map[string]int{"X": 2},
			wantSeverity: map[string]string{"X": "critical"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := aggregateByClass(tt.in)
			if len(got) != len(tt.wantCounts) {
				t.Fatalf("got %d classes, want %d", len(got), len(tt.wantCounts))
			}
			for class, want := range tt.wantCounts {
				if got[class].Count != want {
					t.Errorf("%s count = %d, want %d", class, got[class].Count, want)
				}
			}
			for class, want := range tt.wantSeverity {
				if got[class].Severity != want {
					t.Errorf("%s severity = %q, want %q", class, got[class].Severity, want)
				}
			}
		})
	}
}

func TestSeverityRank(t *testing.T) {
	order := []string{"", "info", "warning", "critical"}
	for i := 1; i < len(order); i++ {
		if severityRank(order[i]) <= severityRank(order[i-1]) {
			t.Errorf("severityRank(%q) should be greater than severityRank(%q)", order[i], order[i-1])
		}
	}
	if severityRank("bogus") != 0 {
		t.Errorf("unknown severity should rank 0")
	}
}
