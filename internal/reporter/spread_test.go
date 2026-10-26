package reporter

import (
	"strings"
	"testing"
)

func TestSpreadLabel(t *testing.T) {
	s := buildDummySummary()
	s.Timelines[0].BurstAnalysis = "10 messages sent and ingested within 14s (earliest SentTimestamp to latest ingestion)"
	outputs := map[string]string{
		"text":     captureStdout(func() { WriteText(s) }),
		"markdown": captureStdout(func() { WriteMarkdown(s) }),
	}
	for name, out := range outputs {
		if !strings.Contains(out, "Spread") || !strings.Contains(out, "sent and ingested within 14s") {
			t.Errorf("%s: missing Spread line:\n%s", name, out)
		}
		if strings.Contains(out, "Burst") {
			t.Errorf("%s: still uses the Burst label", name)
		}
	}
}
