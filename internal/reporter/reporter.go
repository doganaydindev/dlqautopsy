package reporter

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/doganaydindev/dlqautopsy/internal/analyzer"
	"github.com/doganaydindev/dlqautopsy/internal/storage"
	"github.com/fatih/color"
)

// Color definitions — used throughout the reporter for consistent styling.
var (
	bold       = color.New(color.Bold)
	dim        = color.New(color.Faint)
	header     = color.New(color.Bold, color.FgHiWhite)
	subheader  = color.New(color.Bold, color.FgWhite)
	success    = color.New(color.FgGreen)
	warn       = color.New(color.FgYellow)
	danger     = color.New(color.FgRed, color.Bold)
	info       = color.New(color.FgCyan)
	muted      = color.New(color.Faint)
	number     = color.New(color.FgHiCyan, color.Bold)
	labelStyle = color.New(color.FgWhite)
)

// WriteJSON outputs the report summary as structured JSON.
func WriteJSON(summary *analyzer.ReportSummary) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(summary)
}

// WriteText outputs the report summary as a beautiful human-readable CLI report.
func WriteText(summary *analyzer.ReportSummary) error {
	w := os.Stdout

	// Top banner
	fmt.Fprintln(w)
	header.Fprintln(w, "  ╔══════════════════════════════════════════════════════╗")
	header.Fprintln(w, "  ║          DEAD LETTER AUTOPSY REPORT                 ║")
	header.Fprintln(w, "  ╚══════════════════════════════════════════════════════╝")
	fmt.Fprintf(w, "  Run: ")
	bold.Fprintf(w, "%s\n", summary.RunName)
	fmt.Fprintf(w, "  Window: %s → %s ",
		summary.WindowStart.Format("2006-01-02 15:04:05"),
		summary.WindowEnd.Format("2006-01-02 15:04:05"),
	)
	muted.Fprintf(w, "(%s)\n", summary.WindowDuration)
	fmt.Fprintln(w)

	// Executive summary
	subheader.Fprintln(w, "  ── Executive Summary ──────────────────────────────────")
	fmt.Fprintf(w, "  %s\n", summary.ExecutiveSummary)
	fmt.Fprintln(w)

	// Stats dashboard
	subheader.Fprintln(w, "  ── Dashboard ─────────────────────────────────────────")
	printStat(w, "Total Messages", fmt.Sprintf("%d", summary.TotalMessages), number)
	printStat(w, "Failure Classes", classCountLabel(summary), number)
	printStat(w, "Critical", analyzer.Pluralize(summary.CriticalCount, "class", "classes"), danger)

	if summary.TotalMessages > 0 {
		retryPct := float64(summary.SafeToRetryMsgs) / float64(summary.TotalMessages) * 100
		poisonPct := float64(summary.DoNotRetryMsgs) / float64(summary.TotalMessages) * 100
		printStat(w, "Safe to Retry", fmt.Sprintf("%d msgs (%.0f%%)", summary.SafeToRetryMsgs, retryPct), success)
		printStat(w, "Do Not Retry", fmt.Sprintf("%d msgs (%.0f%%)", summary.DoNotRetryMsgs, poisonPct), danger)
	}

	// Triage sections
	for _, tg := range summary.Triage {
		fmt.Fprintln(w)
		writeTriageSection(w, tg, summary.TotalMessages)
	}

	// Detailed breakdown
	fmt.Fprintln(w)
	subheader.Fprintln(w, "  ── Detailed Breakdown ────────────────────────────────")

	for i, t := range summary.Timelines {
		fmt.Fprintln(w)
		writeTimelineDetail(w, i+1, t)
	}

	fmt.Fprintln(w)
	muted.Fprintln(w, "  ──────────────────────────────────────────────────────")
	muted.Fprintf(w, "  Use `dlqautopsy detail --name %s --class \"<name>\"` to drill in.\n", summary.RunName)
	fmt.Fprintln(w)
	return nil
}

// classCountLabel is "7" or, when classes are split by cause, "7 (15 groups)".
func classCountLabel(s *analyzer.ReportSummary) string {
	if s.TotalGroups > s.TotalClasses {
		return fmt.Sprintf("%d (%d groups)", s.TotalClasses, s.TotalGroups)
	}
	return fmt.Sprintf("%d", s.TotalClasses)
}

func classWord(n int) string {
	if n == 1 {
		return "class"
	}
	return "classes"
}

// indentContinuation indents every line after the first by width spaces, so
// multi-line errors (stack traces) stay inside their column instead of
// starting at column 0 and breaking the report layout.
func indentContinuation(s string, width int) string {
	s = strings.TrimRight(s, "\n")
	if !strings.Contains(s, "\n") {
		return s
	}
	return strings.ReplaceAll(s, "\n", "\n"+strings.Repeat(" ", width))
}

func printStat(w io.Writer, label, value string, style *color.Color) {
	fmt.Fprintf(w, "  %-18s ", label+":")
	style.Fprintf(w, "%s\n", value)
}

func writeTriageSection(w io.Writer, tg analyzer.TriageGroup, total int) {
	icon := "▸"
	style := subheader
	switch {
	case strings.Contains(tg.Label, "Immediate"):
		icon = "🔴"
		style = danger
	case strings.Contains(tg.Label, "Safe"):
		icon = "🟢"
		style = success
	case strings.Contains(tg.Label, "Investigation"):
		icon = "🟡"
		style = warn
	}

	style.Fprintf(w, "  %s %s\n", icon, strings.ToUpper(tg.Label))
	dim.Fprintf(w, "  %s\n", strings.Repeat("─", len(tg.Label)+4))

	for _, t := range tg.Classes {
		pct := float64(t.Count) / float64(total) * 100
		sevColor := severityColor(t.Severity)
		sevColor.Fprintf(w, "    [%s] ", severityIcon(t.Severity))
		bold.Fprintf(w, "%-28s", t.FailureClass)
		number.Fprintf(w, " %5d ", t.Count)
		muted.Fprintf(w, "msgs ")
		dim.Fprintf(w, "(%4.1f%%)  ", pct)
		verdictColor(t.RetryVerdict).Fprintf(w, "%s\n", formatVerdict(t.RetryVerdict))
		// Show recommended action to distinguish same-class entries
		action := t.RecommendedAction
		if len(action) > 72 {
			action = action[:69] + "..."
		}
		muted.Fprintf(w, "           → %s\n", action)
	}
	fmt.Fprintf(w, "  Total: ")
	number.Fprintf(w, "%d", tg.Messages)
	fmt.Fprintln(w, " messages")
}

func writeTimelineDetail(w io.Writer, index int, t analyzer.Timeline) {
	sevColor := severityColor(t.Severity)

	// Header line
	number.Fprintf(w, "  [%d] ", index)
	bold.Fprintf(w, "%s  ", t.FailureClass)
	sevColor.Fprintf(w, "[%s] ", strings.ToUpper(t.Severity))
	verdictColor(t.RetryVerdict).Fprintf(w, "[%s]\n", formatVerdict(t.RetryVerdict))

	// Stats
	labelStyle.Fprintf(w, "      Count:          ")
	number.Fprintf(w, "%d", t.Count)
	muted.Fprintf(w, " messages (%.1f%% of total)\n", t.Percentage)

	labelStyle.Fprintf(w, "      Signatures:     ")
	fmt.Fprintf(w, "%s\n", analyzer.Pluralize(t.UniqueSignatures, "unique error hash", "unique error hashes"))

	labelStyle.Fprintf(w, "      Timeline:       ")
	fmt.Fprintf(w, "%s → %s ",
		t.FirstOccurrence.Format("2006-01-02 15:04:05"),
		t.LastOccurrence.Format("2006-01-02 15:04:05"),
	)
	muted.Fprintf(w, "(%s)\n", t.Duration)

	labelStyle.Fprintf(w, "      Retry Pattern:  ")
	fmt.Fprintf(w, "%s ", t.RetryPattern)
	muted.Fprintf(w, "(avg %.1f receives, max %d)\n", t.AvgReceiveCount, t.MaxReceiveCount)

	if t.PeakHourlySpike > 0 {
		labelStyle.Fprintf(w, "      Peak Rate:      ")
		warn.Fprintf(w, "%d messages/hour\n", t.PeakHourlySpike)
	}

	if t.BurstAnalysis != "" {
		labelStyle.Fprintf(w, "      Spread:         ")
		warn.Fprintf(w, "%s\n", t.BurstAnalysis)
	}

	labelStyle.Fprintf(w, "      Action:         ")
	info.Fprintf(w, "%s\n", t.RecommendedAction)

	// Sample error
	sample := t.SampleError
	if len(sample) > 200 {
		sample = sample[:197] + "..."
	}
	const sampleLabel = "      Sample Error:   "
	labelStyle.Fprintf(w, sampleLabel)
	muted.Fprintf(w, "%s\n", indentContinuation(sample, len(sampleLabel)))

	// Payload shapes
	if len(t.PayloadShapes) > 0 {
		labelStyle.Fprintln(w, "      Payload Shapes:")
		for i, shape := range t.PayloadShapes {
			if i >= 3 {
				muted.Fprintf(w, "        ... and %d more\n", len(t.PayloadShapes)-3)
				break
			}
			muted.Fprintf(w, "        • %s\n", shape)
		}
	}
}

// WriteDetailText outputs individual message details for a failure class.
func WriteDetailText(records []storage.DetailRecord, label string) error {
	w := os.Stdout

	fmt.Fprintln(w)
	header.Fprintf(w, "  ╔══ DETAIL: %s ", label)
	header.Fprintln(w, strings.Repeat("═", max(2, 50-len(label))))
	fmt.Fprintf(w, "  Showing ")
	number.Fprintf(w, "%d", len(records))
	fmt.Fprintln(w, " messages")
	fmt.Fprintln(w)

	for i, r := range records {
		sevColor := severityColor(r.Severity)

		number.Fprintf(w, "  [%d] ", i+1)
		bold.Fprintf(w, "Message ID: %s\n", r.MessageID)

		labelStyle.Fprintf(w, "      Receives:    ")
		number.Fprintf(w, "%d\n", r.ReceiveCount)

		labelStyle.Fprintf(w, "      First Seen:  ")
		fmt.Fprintln(w, r.FirstSeen.Format("2006-01-02 15:04:05"))

		labelStyle.Fprintf(w, "      Last Seen:   ")
		fmt.Fprintln(w, r.LastSeen.Format("2006-01-02 15:04:05"))

		labelStyle.Fprintf(w, "      Signature:   ")
		sig := r.ErrorSignature
		if len(sig) > 16 {
			sig = sig[:16]
		}
		muted.Fprintln(w, sig)

		labelStyle.Fprintf(w, "      Severity:    ")
		sevColor.Fprintln(w, r.Severity)

		labelStyle.Fprintf(w, "      Verdict:     ")
		verdictColor(r.RetryVerdict).Fprintln(w, formatVerdict(r.RetryVerdict))

		if r.PayloadShape != "" {
			labelStyle.Fprintf(w, "      Shape:       ")
			muted.Fprintln(w, r.PayloadShape)
		}

		normErr := r.NormalizedError
		if len(normErr) > 200 {
			normErr = normErr[:197] + "..."
		}
		const errLabel = "      Error:       "
		labelStyle.Fprintf(w, errLabel)
		warn.Fprintln(w, indentContinuation(normErr, len(errLabel)))

		body := r.Body
		if len(body) > 300 {
			body = body[:297] + "..."
		}
		labelStyle.Fprintf(w, "      Body:        ")
		muted.Fprintln(w, body)
		dim.Fprintln(w, "      ─────────────────────────────────────────────")
	}

	fmt.Fprintln(w)
	return nil
}

// WriteDetailJSON outputs individual message details as JSON.
func WriteDetailJSON(records []storage.DetailRecord) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(records)
}

// WriteDiffText outputs a beautiful diff comparison between two runs.
// aggregateByClass merges DiffClassSummary rows that share the same FailureClass,
// summing counts and keeping the worst severity.
func aggregateByClass(classes []storage.DiffClassSummary) map[string]storage.DiffClassSummary {
	m := make(map[string]storage.DiffClassSummary)
	for _, c := range classes {
		if existing, ok := m[c.FailureClass]; ok {
			existing.Count += c.Count
			if severityRank(c.Severity) > severityRank(existing.Severity) {
				existing.Severity = c.Severity
			}
			m[c.FailureClass] = existing
		} else {
			m[c.FailureClass] = c
		}
	}
	return m
}

func severityRank(s string) int {
	switch s {
	case "critical":
		return 3
	case "warning":
		return 2
	case "info":
		return 1
	default:
		return 0
	}
}

func WriteDiffText(baseRun, compareRun string, baseClasses, compareClasses []storage.DiffClassSummary) error {
	w := os.Stdout

	d := computeDiff(baseClasses, compareClasses)

	fmt.Fprintln(w)
	header.Fprintln(w, "  ╔══════════════════════════════════════════════════════╗")
	header.Fprintln(w, "  ║              RUN COMPARISON                         ║")
	header.Fprintln(w, "  ╚══════════════════════════════════════════════════════╝")
	labelStyle.Fprintf(w, "  Base:    ")
	bold.Fprintln(w, baseRun)
	labelStyle.Fprintf(w, "  Compare: ")
	bold.Fprintln(w, compareRun)
	fmt.Fprintln(w)

	if len(d.New) > 0 {
		danger.Fprintf(w, "  🆕 NEW FAILURE CLASSES (appeared in %s)\n", compareRun)
		dim.Fprintln(w, "  ──────────────────────────────────────────")
		for _, c := range d.New {
			danger.Fprintf(w, "    + %-28s ", c.FailureClass)
			number.Fprintf(w, "%5d ", c.Count)
			muted.Fprintf(w, "msgs ")
			severityColor(c.Severity).Fprintf(w, "[%s]\n", strings.ToUpper(c.Severity))
		}
		fmt.Fprintln(w)
	}

	if len(d.Resolved) > 0 {
		success.Fprintf(w, "  ✅ RESOLVED (gone from %s)\n", compareRun)
		dim.Fprintln(w, "  ──────────────────────────────────────────")
		for _, c := range d.Resolved {
			success.Fprintf(w, "    ✓ %-28s ", c.FailureClass)
			muted.Fprintf(w, "was %d msgs\n", c.Count)
		}
		fmt.Fprintln(w)
	}

	if len(d.Worsening) > 0 {
		danger.Fprintln(w, "  📈 WORSENING")
		dim.Fprintln(w, "  ──────────────────────────────────────────")
		for _, c := range d.Worsening {
			danger.Fprintf(w, "    ▲ %-28s ", c.FailureClass)
			fmt.Fprintf(w, "%d → %d ", c.BaseCount, c.CompareCount)
			danger.Fprintf(w, "(+%d, +%.0f%%)\n", c.Change, c.ChangePct)
		}
		fmt.Fprintln(w)
	}

	if len(d.Improving) > 0 {
		success.Fprintln(w, "  📉 IMPROVING")
		dim.Fprintln(w, "  ──────────────────────────────────────────")
		for _, c := range d.Improving {
			success.Fprintf(w, "    ▼ %-28s ", c.FailureClass)
			fmt.Fprintf(w, "%d → %d ", c.BaseCount, c.CompareCount)
			success.Fprintf(w, "(%d, %.0f%%)\n", c.Change, c.ChangePct)
		}
		fmt.Fprintln(w)
	}

	if d.empty() {
		success.Fprintln(w, "  ✓ No differences found between the two runs.")
		fmt.Fprintln(w)
	}

	return nil
}

// WriteDiffJSON outputs a diff comparison as JSON.
func WriteDiffJSON(baseRun, compareRun string, baseClasses, compareClasses []storage.DiffClassSummary) error {
	d := computeDiff(baseClasses, compareClasses)
	type diffOutput struct {
		BaseRun    string                     `json:"base_run"`
		CompareRun string                     `json:"compare_run"`
		New        []ClassCount               `json:"new_classes"`
		Resolved   []ClassCount               `json:"resolved_classes"`
		Worsening  []ClassDelta               `json:"worsening"`
		Improving  []ClassDelta               `json:"improving"`
		Base       []storage.DiffClassSummary `json:"base_classes"`
		Compare    []storage.DiffClassSummary `json:"compare_classes"`
	}
	nonNil := func(s []ClassCount) []ClassCount {
		if s == nil {
			return []ClassCount{}
		}
		return s
	}
	nonNilDelta := func(s []ClassDelta) []ClassDelta {
		if s == nil {
			return []ClassDelta{}
		}
		return s
	}
	out := diffOutput{
		BaseRun:    baseRun,
		CompareRun: compareRun,
		New:        nonNil(d.New),
		Resolved:   nonNil(d.Resolved),
		Worsening:  nonNilDelta(d.Worsening),
		Improving:  nonNilDelta(d.Improving),
		Base:       baseClasses,
		Compare:    compareClasses,
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(out)
}

// WriteClassifySummary prints a beautiful summary after classification.
func WriteClassifySummary(db *storage.DB, runName string) error {
	classes, err := db.GetClassSummaries(runName)
	if err != nil {
		return err
	}

	total := 0
	for _, c := range classes {
		total += c.Count
	}

	w := os.Stdout
	fmt.Fprintln(w)
	header.Fprintf(w, "  Classification Complete — '%s'\n", runName)
	dim.Fprintf(w, "  %s\n", strings.Repeat("─", 56))

	for _, c := range classes {
		pct := float64(c.Count) / float64(total) * 100
		sevColor := severityColor(c.Severity)
		sevColor.Fprintf(w, "  [%s] ", severityIcon(c.Severity))
		bold.Fprintf(w, "%-28s", c.FailureClass)
		number.Fprintf(w, " %5d ", c.Count)
		muted.Fprintf(w, "msgs ")
		dim.Fprintf(w, "(%4.1f%%)  ", pct)
		verdictColor(c.RetryVerdict).Fprintf(w, "%s\n", formatVerdict(c.RetryVerdict))
		// Show classification to distinguish rows with the same class name
		classif := c.Classification
		if len(classif) > 70 {
			classif = classif[:67] + "..."
		}
		muted.Fprintf(w, "         → %s\n", classif)
	}

	dim.Fprintf(w, "  %s\n", strings.Repeat("─", 56))
	fmt.Fprintf(w, "  Total: ")
	number.Fprintf(w, "%d", total)
	fmt.Fprintln(w, " messages classified")
	fmt.Fprintln(w)
	return nil
}

// WriteRunsText prints the list of runs with nice formatting.
func WriteRunsText(runs []storage.RunInfo, dbPath string) error {
	w := os.Stdout

	if len(runs) == 0 {
		muted.Fprintln(w, "No runs found in database.")
		return nil
	}

	fmt.Fprintln(w)
	header.Fprintf(w, "  Stored Inspection Runs (%s)\n", dbPath)
	dim.Fprintf(w, "  %s\n", strings.Repeat("─", 62))

	for _, r := range runs {
		fmt.Fprintf(w, "  ")
		bold.Fprintf(w, "%-22s", r.Name)
		number.Fprintf(w, " %4d ", r.MessageCount)
		muted.Fprintf(w, "msgs   ")
		fmt.Fprintf(w, "%s → %s\n",
			r.FirstSeen.Format("2006-01-02 15:04"),
			r.LastSeen.Format("2006-01-02 15:04"),
		)
	}

	dim.Fprintf(w, "  %s\n", strings.Repeat("─", 62))
	fmt.Fprintln(w)
	return nil
}

func formatVerdict(verdict string) string {
	switch verdict {
	case "safe_to_retry":
		return "SAFE TO RETRY"
	case "do_not_retry":
		return "DO NOT RETRY"
	case "retry_with_backoff":
		return "RETRY WITH BACKOFF"
	case "needs_investigation":
		return "NEEDS INVESTIGATION"
	default:
		return strings.ToUpper(verdict)
	}
}

func severityIcon(severity string) string {
	switch severity {
	case "critical":
		return "CRIT"
	case "warning":
		return "WARN"
	case "info":
		return "INFO"
	default:
		return "    "
	}
}

func severityColor(severity string) *color.Color {
	switch severity {
	case "critical":
		return danger
	case "warning":
		return warn
	case "info":
		return info
	default:
		return muted
	}
}

func verdictColor(verdict string) *color.Color {
	switch verdict {
	case "safe_to_retry":
		return success
	case "do_not_retry":
		return danger
	case "retry_with_backoff":
		return warn
	case "needs_investigation":
		return warn
	default:
		return muted
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
