package analyzer

import (
	"context"
	"fmt"
	"time"

	"github.com/doganaydindev/dlqautopsy/internal/storage"
)

// Timeline represents the analytical aggregation of a failure class.
type Timeline struct {
	FailureClass     string    `json:"failure_class"`
	Severity         string    `json:"severity"`
	RetryVerdict     string    `json:"retry_verdict"`
	Count            int       `json:"count"`
	Percentage       float64   `json:"percentage"` // percentage of total messages
	UniqueSignatures int       `json:"unique_signatures"`
	FirstOccurrence  time.Time `json:"first_occurrence"`
	LastOccurrence   time.Time `json:"last_occurrence"`
	Duration         string    `json:"duration"`
	RetryPattern     string    `json:"retry_pattern"`
	AvgReceiveCount  float64   `json:"avg_receive_count"`
	MaxReceiveCount  int       `json:"max_receive_count"`
	// BusiestHourCount is the number of messages in the group whose
	// SentTimestamp falls in the busiest UTC clock hour (e.g. 10:00-10:59).
	// It was called peak_hourly_spike / "Peak Rate" before v0.1.1.
	BusiestHourCount  int      `json:"busiest_hour_count"`
	RecommendedAction string   `json:"recommended_action"`
	SamplePayload     string   `json:"sample_payload"`
	SampleError       string   `json:"sample_error"`
	PayloadShapes     []string `json:"payload_shapes,omitempty"`
	// BurstAnalysis states how close together the group's messages are, in
	// plain numbers. The window runs from the earliest SQS SentTimestamp to
	// the latest ingestion time, so it does not say why they failed.
	BurstAnalysis string `json:"burst_analysis,omitempty"`
}

// TriageGroup groups failure classes by urgency for the executive summary.
type TriageGroup struct {
	Label    string     `json:"label"`
	Classes  []Timeline `json:"classes"`
	Messages int        `json:"total_messages"`
}

// ReportSummary holds top-level aggregate stats across the entire run.
type ReportSummary struct {
	RunName          string        `json:"run_name"`
	TotalMessages    int           `json:"total_messages"`
	TotalClasses     int           `json:"total_classes"` // distinct failure classes
	TotalGroups      int           `json:"total_groups"`  // rows in "classes": one per (failure class, cause)
	CriticalCount    int           `json:"critical_count"`
	SafeToRetryCount int           `json:"safe_to_retry_count"`
	SafeToRetryMsgs  int           `json:"safe_to_retry_messages"`
	DoNotRetryCount  int           `json:"do_not_retry_count"`
	DoNotRetryMsgs   int           `json:"do_not_retry_messages"`
	WindowStart      time.Time     `json:"window_start"`
	WindowEnd        time.Time     `json:"window_end"`
	WindowDuration   string        `json:"window_duration"`
	ExecutiveSummary string        `json:"executive_summary"`
	Triage           []TriageGroup `json:"triage"`
	Timelines        []Timeline    `json:"classes"`
}

// Analyzer builds temporal views and detects overarching failure patterns.
type Analyzer struct {
	db      *storage.DB
	runName string
}

func NewAnalyzer(db *storage.DB, runName string) *Analyzer {
	return &Analyzer{db: db, runName: runName}
}

// Run analyzes classified failures and produces a full report summary.
func (a *Analyzer) Run(ctx context.Context) (*ReportSummary, error) {
	timelines, err := a.buildTimelines(ctx)
	if err != nil {
		return nil, err
	}

	summary := &ReportSummary{
		RunName:   a.runName,
		Timelines: timelines,
	}

	// Compute aggregate stats
	for i := range timelines {
		summary.TotalMessages += timelines[i].Count
	}
	// One failure class (e.g. "Downstream Dependency") can appear as several
	// rows, one per cause. Class counts are over distinct class names; the
	// number of rows is reported separately as TotalGroups.
	seen := map[string]bool{}
	critical := map[string]bool{}
	safe := map[string]bool{}
	doNot := map[string]bool{}
	for i := range timelines {
		t := &timelines[i]
		summary.TotalGroups++
		if !seen[t.FailureClass] {
			seen[t.FailureClass] = true
			summary.TotalClasses++
		}

		// Percentage of total
		if summary.TotalMessages > 0 {
			t.Percentage = float64(t.Count) / float64(summary.TotalMessages) * 100
		}

		if t.Severity == "critical" && !critical[t.FailureClass] {
			critical[t.FailureClass] = true
			summary.CriticalCount++
		}
		switch t.RetryVerdict {
		case "safe_to_retry", "retry_with_backoff":
			if !safe[t.FailureClass] {
				safe[t.FailureClass] = true
				summary.SafeToRetryCount++
			}
			summary.SafeToRetryMsgs += t.Count
		case "do_not_retry":
			if !doNot[t.FailureClass] {
				doNot[t.FailureClass] = true
				summary.DoNotRetryCount++
			}
			summary.DoNotRetryMsgs += t.Count
		}

		if summary.WindowStart.IsZero() || t.FirstOccurrence.Before(summary.WindowStart) {
			summary.WindowStart = t.FirstOccurrence
		}
		if t.LastOccurrence.After(summary.WindowEnd) {
			summary.WindowEnd = t.LastOccurrence
		}

		// Enrich with payload shapes from storage. Scoped to this row's
		// classification: one failure class (e.g. "Downstream Dependency")
		// can appear as several rows, and each row should only list the
		// shapes of its own messages.
		shapes, err := a.db.GetPayloadShapesForClassification(a.runName, t.FailureClass, t.RecommendedAction)
		if err == nil {
			for _, s := range shapes {
				t.PayloadShapes = append(t.PayloadShapes, s.Shape)
			}
		}
	}

	if !summary.WindowStart.IsZero() && !summary.WindowEnd.IsZero() {
		summary.WindowDuration = formatDuration(summary.WindowEnd.Sub(summary.WindowStart))
	}

	// Build triage groups
	summary.Triage = a.buildTriageGroups(timelines)

	// Build executive summary
	summary.ExecutiveSummary = a.buildExecutiveSummary(summary)

	return summary, nil
}

func (a *Analyzer) buildTimelines(ctx context.Context) ([]Timeline, error) {
	query := `
	SELECT 
		f.failure_class,
		f.classification,
		f.severity,
		f.retry_verdict,
		COUNT(f.raw_event_id) as count,
		COUNT(DISTINCT f.error_signature) as unique_signatures,
		MIN(r.first_seen) as first_occurrence,
		MAX(r.last_seen) as last_occurrence,
		AVG(r.receive_count) as avg_receive_count,
		MAX(r.receive_count) as max_receive_count,
		MIN(r.body) as sample_payload,
		MIN(f.normalized_error) as sample_error,
		COALESCE((
			SELECT COUNT(*) 
			FROM failure_records f2 
			JOIN raw_events r2 ON f2.run_name = r2.run_name AND f2.raw_event_id = r2.id
			WHERE f2.run_name = ? AND f2.failure_class = f.failure_class AND f2.classification = f.classification
			GROUP BY strftime('%Y-%m-%d %H', r2.first_seen)
			ORDER BY COUNT(*) DESC 
			LIMIT 1
		), 0) as busiest_hour_count,
		SUM(CASE WHEN r.receive_count <= 1 THEN 1 ELSE 0 END) as instant_death_count,
		SUM(CASE WHEN r.receive_count > 1 THEN 1 ELSE 0 END) as exhaustion_count
	FROM failure_records f
	JOIN raw_events r ON f.run_name = r.run_name AND f.raw_event_id = r.id
	WHERE f.run_name = ?
	GROUP BY f.failure_class, f.classification, f.severity, f.retry_verdict
	ORDER BY count DESC, f.failure_class ASC, f.classification ASC
	`
	rows, err := a.db.GetDB().QueryContext(ctx, query, a.runName, a.runName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var timelines []Timeline

	for rows.Next() {
		var t Timeline
		var classification string
		var avgReceiveCount float64
		var maxReceiveCount int
		var firstOccAny, lastOccAny any
		var samplePayload, sampleError string
		var busiestHourCount int
		var instantDeathCount, exhaustionCount int
		var uniqueSignatures int

		err := rows.Scan(
			&t.FailureClass,
			&classification,
			&t.Severity,
			&t.RetryVerdict,
			&t.Count,
			&uniqueSignatures,
			&firstOccAny,
			&lastOccAny,
			&avgReceiveCount,
			&maxReceiveCount,
			&samplePayload,
			&sampleError,
			&busiestHourCount,
			&instantDeathCount,
			&exhaustionCount,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan timeline row: %w", err)
		}

		t.FirstOccurrence = parseSQLiteTime(firstOccAny)
		t.LastOccurrence = parseSQLiteTime(lastOccAny)
		t.UniqueSignatures = uniqueSignatures
		t.AvgReceiveCount = avgReceiveCount
		t.MaxReceiveCount = maxReceiveCount
		t.SamplePayload = samplePayload
		t.SampleError = sampleError
		t.BusiestHourCount = busiestHourCount
		t.RecommendedAction = classification

		// Compute duration
		if !t.FirstOccurrence.IsZero() && !t.LastOccurrence.IsZero() {
			dur := t.LastOccurrence.Sub(t.FirstOccurrence)
			t.Duration = formatDuration(dur)
			t.BurstAnalysis = analyzeBurst(dur, t.Count)
		}

		t.RetryPattern = classifyRetryPattern(instantDeathCount, exhaustionCount, t.Count)

		timelines = append(timelines, t)
	}

	return timelines, rows.Err()
}

// analyzeBurst describes how close together a group's messages are. The
// duration is measured from the earliest SQS SentTimestamp in the group to
// the latest time dlqautopsy ingested one of its messages (last_seen is set
// by the collector at ingestion). It therefore includes time spent waiting in
// the queues and the ingestion itself, and is no evidence of a single cause,
// so the text only states the numbers.
func analyzeBurst(duration time.Duration, count int) string {
	if count < 2 {
		return ""
	}
	within := "within " + formatDuration(duration)
	if duration < time.Second {
		within = "within less than 1s"
	}
	return fmt.Sprintf("%d messages sent and ingested %s (earliest SentTimestamp to latest ingestion)", count, within)
}

// buildTriageGroups sorts failure classes into actionable triage buckets.
func (a *Analyzer) buildTriageGroups(timelines []Timeline) []TriageGroup {
	groups := map[string]*TriageGroup{
		"immediate":   {Label: "Immediate Action Required"},
		"retry":       {Label: "Safe to Retry"},
		"investigate": {Label: "Needs Investigation"},
	}

	for _, t := range timelines {
		switch {
		case t.RetryVerdict == "do_not_retry":
			g := groups["immediate"]
			g.Classes = append(g.Classes, t)
			g.Messages += t.Count
		case t.RetryVerdict == "safe_to_retry" || t.RetryVerdict == "retry_with_backoff":
			g := groups["retry"]
			g.Classes = append(g.Classes, t)
			g.Messages += t.Count
		default:
			g := groups["investigate"]
			g.Classes = append(g.Classes, t)
			g.Messages += t.Count
		}
	}

	// Return in priority order, skip empties
	var result []TriageGroup
	for _, key := range []string{"immediate", "retry", "investigate"} {
		g := groups[key]
		if len(g.Classes) > 0 {
			result = append(result, *g)
		}
	}
	return result
}

// buildExecutiveSummary generates a plain-text executive summary.
func (a *Analyzer) buildExecutiveSummary(s *ReportSummary) string {
	if s.TotalMessages == 0 {
		return "No messages found for this run."
	}

	var parts []string

	first := fmt.Sprintf("%s across %s", Pluralize(s.TotalMessages, "message", "messages"), Pluralize(s.TotalClasses, "failure class", "failure classes"))
	if s.TotalGroups > s.TotalClasses {
		first += fmt.Sprintf(", split into %d groups by cause", s.TotalGroups)
	}
	if s.WindowDuration != "" {
		first += fmt.Sprintf(" (window: %s)", s.WindowDuration)
	}
	parts = append(parts, first+".")

	if s.SafeToRetryMsgs > 0 {
		pct := float64(s.SafeToRetryMsgs) / float64(s.TotalMessages) * 100
		parts = append(parts, fmt.Sprintf(
			"%.0f%% (%d messages) are safe to retry now.",
			pct, s.SafeToRetryMsgs,
		))
	}

	if s.DoNotRetryMsgs > 0 {
		pct := float64(s.DoNotRetryMsgs) / float64(s.TotalMessages) * 100
		parts = append(parts, fmt.Sprintf(
			"%.0f%% (%d messages) should NOT be retried (poison messages or config errors).",
			pct, s.DoNotRetryMsgs,
		))
	}

	needsInvestigation := s.TotalMessages - s.SafeToRetryMsgs - s.DoNotRetryMsgs
	if needsInvestigation > 0 {
		pct := float64(needsInvestigation) / float64(s.TotalMessages) * 100
		parts = append(parts, fmt.Sprintf(
			"%.0f%% (%d messages) need manual investigation.",
			pct, needsInvestigation,
		))
	}

	// Biggest class, summed over all of its rows (a class can have several
	// causes). Ties go to the class name that sorts first.
	if len(s.Timelines) > 0 {
		totals := map[string]int{}
		for _, t := range s.Timelines {
			totals[t.FailureClass] += t.Count
		}
		topName, topCount := "", -1
		for name, n := range totals {
			if n > topCount || (n == topCount && name < topName) {
				topName, topCount = name, n
			}
		}
		parts = append(parts, fmt.Sprintf(
			"Largest class: \"%s\" with %d messages (%.0f%%).",
			topName, topCount, float64(topCount)/float64(s.TotalMessages)*100,
		))
	}

	return joinSentences(parts)
}

// Pluralize returns "1 message" or "3 messages".
func Pluralize(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func joinSentences(parts []string) string {
	result := ""
	for i, p := range parts {
		if i > 0 {
			result += " "
		}
		result += p
	}
	return result
}

func classifyRetryPattern(instantDeathCount, exhaustionCount, total int) string {
	if total == 0 {
		return "Unknown"
	}
	instantRatio := float64(instantDeathCount) / float64(total)
	exhaustionRatio := float64(exhaustionCount) / float64(total)

	if instantRatio >= 0.8 {
		return "Instant Death"
	}
	if exhaustionRatio >= 0.8 {
		return "Exhaustion"
	}
	if instantRatio > 0.3 && exhaustionRatio > 0.3 {
		return "Mixed"
	}
	if instantRatio > exhaustionRatio {
		return "Mostly Instant Death"
	}
	return "Mostly Exhaustion"
}

func formatDuration(d time.Duration) string {
	if d < time.Second {
		return "< 1s"
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm %ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

func parseSQLiteTime(val any) time.Time {
	if t, ok := val.(time.Time); ok {
		return t
	}
	if s, ok := val.(string); ok {
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return t
		}
		if t, err := time.Parse("2006-01-02 15:04:05.999999999-07:00", s); err == nil {
			return t
		}
		if t, err := time.Parse("2006-01-02 15:04:05-07:00", s); err == nil {
			return t
		}
		if t, err := time.Parse("2006-01-02T15:04:05Z", s); err == nil {
			return t
		}
	}
	return time.Time{}
}
