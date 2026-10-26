package reporter

import (
	"sort"

	"github.com/doganaydindev/dlqautopsy/internal/storage"
)

// ClassDelta is a failure class present in both runs whose count changed.
type ClassDelta struct {
	FailureClass string  `json:"failure_class"`
	Severity     string  `json:"severity"`
	BaseCount    int     `json:"base_count"`
	CompareCount int     `json:"compare_count"`
	Change       int     `json:"change"`
	ChangePct    float64 `json:"change_pct"`
}

// ClassCount is one failure class with its total message count across all
// of its causes, and the worst severity among them.
type ClassCount struct {
	FailureClass string `json:"failure_class"`
	Severity     string `json:"severity"`
	Count        int    `json:"count"`
}

func toClassCount(c storage.DiffClassSummary) ClassCount {
	return ClassCount{FailureClass: c.FailureClass, Severity: c.Severity, Count: c.Count}
}

// runDiff is the comparison of two runs, aggregated by failure class.
// Every slice has a fixed order so text, markdown and JSON output are
// stable between invocations (Go map iteration order is random).
type runDiff struct {
	New       []ClassCount // in compare, not in base; count desc, then name
	Resolved  []ClassCount // in base, not in compare; count desc, then name
	Worsening []ClassDelta // count went up; largest increase first, then name
	Improving []ClassDelta // count went down; largest decrease first, then name
}

func (d runDiff) empty() bool {
	return len(d.New) == 0 && len(d.Resolved) == 0 && len(d.Worsening) == 0 && len(d.Improving) == 0
}

func computeDiff(baseClasses, compareClasses []storage.DiffClassSummary) runDiff {
	baseMap := aggregateByClass(baseClasses)
	compareMap := aggregateByClass(compareClasses)

	var d runDiff
	for name, c := range compareMap {
		if _, found := baseMap[name]; !found {
			d.New = append(d.New, toClassCount(c))
		}
	}
	for name, c := range baseMap {
		cc, found := compareMap[name]
		if !found {
			d.Resolved = append(d.Resolved, toClassCount(c))
			continue
		}
		delta := ClassDelta{
			FailureClass: c.FailureClass,
			Severity:     cc.Severity,
			BaseCount:    c.Count,
			CompareCount: cc.Count,
			Change:       cc.Count - c.Count,
		}
		if c.Count > 0 {
			delta.ChangePct = float64(delta.Change) / float64(c.Count) * 100
		}
		switch {
		case delta.Change > 0:
			d.Worsening = append(d.Worsening, delta)
		case delta.Change < 0:
			d.Improving = append(d.Improving, delta)
		}
	}

	byCount := func(s []ClassCount) {
		sort.Slice(s, func(i, j int) bool {
			if s[i].Count != s[j].Count {
				return s[i].Count > s[j].Count
			}
			return s[i].FailureClass < s[j].FailureClass
		})
	}
	byChange := func(s []ClassDelta) {
		sort.Slice(s, func(i, j int) bool {
			ai, aj := abs(s[i].Change), abs(s[j].Change)
			if ai != aj {
				return ai > aj
			}
			return s[i].FailureClass < s[j].FailureClass
		})
	}
	byCount(d.New)
	byCount(d.Resolved)
	byChange(d.Worsening)
	byChange(d.Improving)
	return d
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
