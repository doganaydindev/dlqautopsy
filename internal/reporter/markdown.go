package reporter

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/doganaydindev/dlqautopsy/internal/analyzer"
	"github.com/doganaydindev/dlqautopsy/internal/storage"
)

// WriteMarkdown outputs the full autopsy report as a Markdown document.
func WriteMarkdown(summary *analyzer.ReportSummary) error {
	return writeMarkdown(os.Stdout, summary)
}

func writeMarkdown(w io.Writer, summary *analyzer.ReportSummary) error {

	fmt.Fprintln(w, "# Dead Letter Autopsy Report")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "**Run:** `%s`  \n", summary.RunName)
	fmt.Fprintf(w, "**Window:** %s → %s (%s)\n",
		summary.WindowStart.Format("2006-01-02 15:04:05"),
		summary.WindowEnd.Format("2006-01-02 15:04:05"),
		summary.WindowDuration,
	)
	fmt.Fprintln(w)

	// Executive Summary
	fmt.Fprintln(w, "## Executive Summary")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "> %s\n", summary.ExecutiveSummary)
	fmt.Fprintln(w)

	// Dashboard
	fmt.Fprintln(w, "## Dashboard")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| Metric | Value |")
	fmt.Fprintln(w, "|--------|-------|")
	fmt.Fprintf(w, "| Total Messages | **%d** |\n", summary.TotalMessages)
	fmt.Fprintf(w, "| Failure Classes | **%d** |\n", summary.TotalClasses)
	fmt.Fprintf(w, "| Critical | **%d** classes |\n", summary.CriticalCount)

	if summary.TotalMessages > 0 {
		retryPct := float64(summary.SafeToRetryMsgs) / float64(summary.TotalMessages) * 100
		poisonPct := float64(summary.DoNotRetryMsgs) / float64(summary.TotalMessages) * 100
		fmt.Fprintf(w, "| Safe to Retry | %d msgs (%.0f%%) |\n", summary.SafeToRetryMsgs, retryPct)
		fmt.Fprintf(w, "| Do Not Retry | %d msgs (%.0f%%) |\n", summary.DoNotRetryMsgs, poisonPct)
	}
	fmt.Fprintln(w)

	// Triage
	for _, tg := range summary.Triage {
		writeTriageSectionMarkdown(w, tg, summary.TotalMessages)
	}

	// Detailed Breakdown
	fmt.Fprintln(w, "## Detailed Breakdown")
	fmt.Fprintln(w)

	for i, t := range summary.Timelines {
		writeTimelineDetailMarkdown(w, i+1, t)
	}

	fmt.Fprintf(w, "---\n\n")
	fmt.Fprintf(w, "_Use `dlqautopsy detail --name %s --class \"<name>\"` to drill in._\n", summary.RunName)

	return nil
}

func triageIcon(label string) string {
	switch {
	case strings.Contains(label, "Immediate"):
		return "🔴"
	case strings.Contains(label, "Safe"):
		return "🟢"
	case strings.Contains(label, "Investigation"):
		return "🟡"
	default:
		return "▸"
	}
}

func writeTriageSectionMarkdown(w io.Writer, tg analyzer.TriageGroup, total int) {
	icon := triageIcon(tg.Label)
	fmt.Fprintf(w, "### %s %s\n", icon, tg.Label)
	fmt.Fprintln(w)

	fmt.Fprintln(w, "| Severity | Failure Class | Count | % | Verdict | Action |")
	fmt.Fprintln(w, "|----------|---------------|------:|--:|---------|--------|")

	for _, t := range tg.Classes {
		pct := float64(t.Count) / float64(total) * 100
		action := t.RecommendedAction
		if len(action) > 60 {
			action = action[:57] + "..."
		}
		fmt.Fprintf(w, "| `%s` | **%s** | %d | %.1f%% | %s | %s |\n",
			strings.ToUpper(t.Severity),
			t.FailureClass,
			t.Count,
			pct,
			formatVerdict(t.RetryVerdict),
			action,
		)
	}

	fmt.Fprintf(w, "\n> **Total:** %d messages\n\n", tg.Messages)
}

func writeTimelineDetailMarkdown(w io.Writer, index int, t analyzer.Timeline) {
	sevBadge := severityBadge(t.Severity)
	verdictBadge := verdictMarkdownBadge(t.RetryVerdict)

	fmt.Fprintf(w, "### %d. %s %s %s\n", index, t.FailureClass, sevBadge, verdictBadge)
	fmt.Fprintln(w)

	fmt.Fprintf(w, "| Field | Value |\n")
	fmt.Fprintf(w, "|-------|-------|\n")
	fmt.Fprintf(w, "| Count | **%d** messages (%.1f%% of total) |\n", t.Count, t.Percentage)
	fmt.Fprintf(w, "| Signatures | %d unique error hashes |\n", t.UniqueSignatures)
	fmt.Fprintf(w, "| Timeline | %s → %s (%s) |\n",
		t.FirstOccurrence.Format("2006-01-02 15:04:05"),
		t.LastOccurrence.Format("2006-01-02 15:04:05"),
		t.Duration,
	)
	fmt.Fprintf(w, "| Retry Pattern | %s (avg %.1f receives, max %d) |\n", t.RetryPattern, t.AvgReceiveCount, t.MaxReceiveCount)

	if t.PeakHourlySpike > 0 {
		fmt.Fprintf(w, "| Peak Rate | ⚠️ %d messages/hour |\n", t.PeakHourlySpike)
	}
	if t.BurstAnalysis != "" {
		fmt.Fprintf(w, "| Burst | %s |\n", t.BurstAnalysis)
	}
	fmt.Fprintf(w, "| Action | %s |\n", t.RecommendedAction)
	fmt.Fprintln(w)

	// Sample error
	sample := t.SampleError
	if len(sample) > 300 {
		sample = sample[:297] + "..."
	}
	fmt.Fprintln(w, "<details>")
	fmt.Fprintln(w, "<summary>Sample Error</summary>")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "```")
	fmt.Fprintln(w, sample)
	fmt.Fprintln(w, "```")
	fmt.Fprintln(w, "</details>")

	// Payload shapes
	if len(t.PayloadShapes) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "<details>")
		fmt.Fprintln(w, "<summary>Payload Shapes</summary>")
		fmt.Fprintln(w)
		for i, shape := range t.PayloadShapes {
			if i >= 5 {
				fmt.Fprintf(w, "- _...and %d more_\n", len(t.PayloadShapes)-5)
				break
			}
			fmt.Fprintf(w, "- `%s`\n", shape)
		}
		fmt.Fprintln(w, "</details>")
	}

	fmt.Fprintln(w)
}

func severityBadge(severity string) string {
	switch severity {
	case "critical":
		return "🔴 **CRITICAL**"
	case "warning":
		return "🟡 **WARNING**"
	case "info":
		return "🔵 **INFO**"
	default:
		return ""
	}
}

func verdictMarkdownBadge(verdict string) string {
	switch verdict {
	case "safe_to_retry":
		return "✅ SAFE TO RETRY"
	case "do_not_retry":
		return "🚫 DO NOT RETRY"
	case "retry_with_backoff":
		return "⏳ RETRY WITH BACKOFF"
	case "needs_investigation":
		return "🔍 NEEDS INVESTIGATION"
	default:
		return strings.ToUpper(verdict)
	}
}

// WriteDetailMarkdown outputs individual message details as Markdown.
func WriteDetailMarkdown(records []storage.DetailRecord, label string) error {
	return writeDetailMarkdown(os.Stdout, records, label)
}

func writeDetailMarkdown(w io.Writer, records []storage.DetailRecord, label string) error {

	fmt.Fprintf(w, "# Detail: %s\n\n", label)
	fmt.Fprintf(w, "Showing **%d** messages.\n\n", len(records))

	for i, r := range records {
		sig := r.ErrorSignature
		if len(sig) > 16 {
			sig = sig[:16]
		}

		fmt.Fprintf(w, "## %d. Message `%s`\n\n", i+1, r.MessageID)

		fmt.Fprintln(w, "| Field | Value |")
		fmt.Fprintln(w, "|-------|-------|")
		fmt.Fprintf(w, "| Receives | **%d** |\n", r.ReceiveCount)
		fmt.Fprintf(w, "| First Seen | %s |\n", r.FirstSeen.Format("2006-01-02 15:04:05"))
		fmt.Fprintf(w, "| Last Seen | %s |\n", r.LastSeen.Format("2006-01-02 15:04:05"))
		fmt.Fprintf(w, "| Signature | `%s` |\n", sig)
		fmt.Fprintf(w, "| Severity | %s |\n", severityBadge(r.Severity))
		fmt.Fprintf(w, "| Verdict | %s |\n", verdictMarkdownBadge(r.RetryVerdict))
		if r.PayloadShape != "" {
			fmt.Fprintf(w, "| Shape | `%s` |\n", r.PayloadShape)
		}
		fmt.Fprintln(w)

		normErr := r.NormalizedError
		if len(normErr) > 300 {
			normErr = normErr[:297] + "..."
		}
		fmt.Fprintln(w, "<details>")
		fmt.Fprintln(w, "<summary>Error</summary>")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "```")
		fmt.Fprintln(w, normErr)
		fmt.Fprintln(w, "```")
		fmt.Fprintln(w, "</details>")
		fmt.Fprintln(w)

		body := r.Body
		if len(body) > 500 {
			body = body[:497] + "..."
		}
		fmt.Fprintln(w, "<details>")
		fmt.Fprintln(w, "<summary>Body</summary>")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "```json")
		fmt.Fprintln(w, body)
		fmt.Fprintln(w, "```")
		fmt.Fprintln(w, "</details>")
		fmt.Fprintln(w)

		fmt.Fprintln(w, "---")
		fmt.Fprintln(w)
	}

	return nil
}

// WriteDiffMarkdown outputs a run-to-run comparison as Markdown.
func WriteDiffMarkdown(baseRun, compareRun string, baseClasses, compareClasses []storage.DiffClassSummary) error {
	return writeDiffMarkdown(os.Stdout, baseRun, compareRun, baseClasses, compareClasses)
}

func writeDiffMarkdown(w io.Writer, baseRun, compareRun string, baseClasses, compareClasses []storage.DiffClassSummary) error {

	baseMap := aggregateByClass(baseClasses)
	compareMap := aggregateByClass(compareClasses)

	fmt.Fprintln(w, "# Run Comparison")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "**Base:** `%s`  \n", baseRun)
	fmt.Fprintf(w, "**Compare:** `%s`\n", compareRun)
	fmt.Fprintln(w)

	// New classes
	var newClasses []storage.DiffClassSummary
	for name, c := range compareMap {
		if _, found := baseMap[name]; !found {
			newClasses = append(newClasses, c)
		}
	}
	if len(newClasses) > 0 {
		fmt.Fprintf(w, "## 🆕 New Failure Classes (appeared in `%s`)\n\n", compareRun)
		fmt.Fprintln(w, "| Failure Class | Count | Severity |")
		fmt.Fprintln(w, "|---------------|------:|----------|")
		for _, c := range newClasses {
			fmt.Fprintf(w, "| **%s** | %d | %s |\n", c.FailureClass, c.Count, severityBadge(c.Severity))
		}
		fmt.Fprintln(w)
	}

	// Resolved classes
	var resolvedClasses []storage.DiffClassSummary
	for name, c := range baseMap {
		if _, found := compareMap[name]; !found {
			resolvedClasses = append(resolvedClasses, c)
		}
	}
	if len(resolvedClasses) > 0 {
		fmt.Fprintf(w, "## ✅ Resolved (gone from `%s`)\n\n", compareRun)
		fmt.Fprintln(w, "| Failure Class | Was |")
		fmt.Fprintln(w, "|---------------|----:|")
		for _, c := range resolvedClasses {
			fmt.Fprintf(w, "| ~~%s~~ | %d msgs |\n", c.FailureClass, c.Count)
		}
		fmt.Fprintln(w)
	}

	// Changed classes
	type delta struct {
		class     string
		severity  string
		baseCount int
		newCount  int
		change    int
		changePct float64
	}
	var worsening, improving []delta
	for name, bc := range baseMap {
		if cc, found := compareMap[name]; found {
			d := delta{
				class:     bc.FailureClass,
				severity:  cc.Severity,
				baseCount: bc.Count,
				newCount:  cc.Count,
				change:    cc.Count - bc.Count,
			}
			if bc.Count > 0 {
				d.changePct = float64(d.change) / float64(bc.Count) * 100
			}
			if d.change > 0 {
				worsening = append(worsening, d)
			} else if d.change < 0 {
				improving = append(improving, d)
			}
		}
	}

	if len(worsening) > 0 {
		fmt.Fprintln(w, "## 📈 Worsening")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "| Failure Class | Before | After | Change |")
		fmt.Fprintln(w, "|---------------|-------:|------:|-------:|")
		for _, d := range worsening {
			fmt.Fprintf(w, "| **%s** | %d | %d | **+%d** (+%.0f%%) |\n", d.class, d.baseCount, d.newCount, d.change, d.changePct)
		}
		fmt.Fprintln(w)
	}

	if len(improving) > 0 {
		fmt.Fprintln(w, "## 📉 Improving")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "| Failure Class | Before | After | Change |")
		fmt.Fprintln(w, "|---------------|-------:|------:|-------:|")
		for _, d := range improving {
			fmt.Fprintf(w, "| **%s** | %d | %d | %d (%.0f%%) |\n", d.class, d.baseCount, d.newCount, d.change, d.changePct)
		}
		fmt.Fprintln(w)
	}

	if len(newClasses) == 0 && len(resolvedClasses) == 0 && len(worsening) == 0 && len(improving) == 0 {
		fmt.Fprintln(w, "> ✅ No differences found between the two runs.")
		fmt.Fprintln(w)
	}

	return nil
}
