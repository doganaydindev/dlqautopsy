package reporter

import (
	"strings"
	"testing"
)

func TestBusiestHourLine(t *testing.T) {
	s := buildDummySummary()
	tl := &s.Timelines[0]

	tl.Count, tl.BusiestHourCount = 10, 4
	for name, out := range map[string]string{
		"text":     captureStdout(func() { WriteText(s) }),
		"markdown": captureStdout(func() { WriteMarkdown(s) }),
	} {
		if !strings.Contains(out, "Busiest Hour") || !strings.Contains(out, "4 messages (by SentTimestamp, busiest UTC clock hour)") {
			t.Errorf("%s: expected busiest hour line, got:\n%s", name, out)
		}
		if strings.Contains(out, "Peak Rate") || strings.Contains(out, "messages/hour") {
			t.Errorf("%s: still prints the old Peak Rate wording", name)
		}
	}

	// All messages in one clock hour: the number would equal the group size.
	for i := range s.Timelines {
		s.Timelines[i].BusiestHourCount = s.Timelines[i].Count
	}
	for name, out := range map[string]string{
		"text":     captureStdout(func() { WriteText(s) }),
		"markdown": captureStdout(func() { WriteMarkdown(s) }),
	} {
		if strings.Contains(out, "Busiest Hour") {
			t.Errorf("%s: busiest hour line should be hidden when it equals the group size:\n%s", name, out)
		}
	}
}
