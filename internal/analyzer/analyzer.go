package analyzer

import (
	"context"
	"fmt"
	"time"

	"github.com/dgnydn/dlqautopsy/internal/storage"
)

// Timeline represents the analytical aggregation of a failure class.
type Timeline struct {
	FailureClass      string    `json:"failure_class"`
	Severity          string    `json:"severity"`
	RetryVerdict      string    `json:"retry_verdict"`
	Count             int       `json:"count"`
	Percentage        float64   `json:"percentage"` // percentage of total messages
	UniqueSignatures  int       `json:"unique_signatures"`
	FirstOccurrence   time.Time `json:"first_occurrence"`
	LastOccurrence    time.Time `json:"last_occurrence"`
	Duration          string    `json:"duration"`
	RetryPattern      string    `json:"retry_pattern"`
	AvgReceiveCount   float64   `json:"avg_receive_count"`
	MaxReceiveCount   int       `json:"max_receive_count"`
	PeakHourlySpike   int       `json:"peak_hourly_spike"`
	RecommendedAction string    `json:"recommended_action"`
	SamplePayload     string    `json:"sample_payload"`
	SampleError       string    `json:"sample_error"`
	PayloadShapes     []string  `json:"payload_shapes,omitempty"`
	BurstAnalysis     string    `json:"burst_analysis,omitempty"` // human-readable burst assessment
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
	TotalClasses     int           `json:"total_classes"`
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
	for i := range timelines {
		summary.TotalClasses++
		t := &timelines[i]

		// Percentage of total
		if summary.TotalMessages > 0 {
			t.Percentage = float64(t.Count) / float64(summary.TotalMessages) * 100
		}

		if t.Severity == "critical" {
			summary.CriticalCount++
		}
		switch t.RetryVerdict {
		case "safe_to_retry", "retry_with_backoff":
			summary.SafeToRetryCount++
			summary.SafeToRetryMsgs += t.Count
		case "do_not_retry":
			summary.DoNotRetryCount++
			summary.DoNotRetryMsgs += t.Count
		}

		if summary.WindowStart.IsZero() || t.FirstOccurrence.Before(summary.WindowStart) {
			summary.WindowStart = t.FirstOccurrence
		}
		if t.LastOccurrence.After(summary.WindowEnd) {
			summary.WindowEnd = t.LastOccurrence
		}

		// Enrich with payload shapes from storage
		shapes, err := a.db.GetPayloadShapes(a.runName, t.FailureClass)
		if err == nil && len(shapes) > 0 {
			for shape := range shapes {
				t.PayloadShapes = append(t.PayloadShapes, shape)
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
			WHERE f2.run_name = ? AND f2.failure_class = f.failure_class
			GROUP BY strftime('%Y-%m-%d %H', r2.first_seen)
			ORDER BY COUNT(*) DESC 
			LIMIT 1
		), 0) as peak_hourly_spike,
		SUM(CASE WHEN r.receive_count <= 1 THEN 1 ELSE 0 END) as instant_death_count,
		SUM(CASE WHEN r.receive_count > 1 THEN 1 ELSE 0 END) as exhaustion_count
	FROM failure_records f
	JOIN raw_events r ON f.run_name = r.run_name AND f.raw_event_id = r.id
	WHERE f.run_name = ?
	GROUP BY f.failure_class, f.classification, f.severity, f.retry_verdict
	ORDER BY count DESC
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
		var peakHourlySpike int
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
			&peakHourlySpike,
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
		t.PeakHourlySpike = peakHourlySpike
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

// analyzeBurst detects whether a failure class represents a concentrated burst.
func analyzeBurst(duration time.Duration, count int) string {
	if count < 5 {
		return ""
	}
	if duration < 2*time.Minute {
		return fmt.Sprintf("Concentrated burst: %d failures in %s. Likely a single event (deploy, config push, dependency crash).", count, formatDuration(duration))
	}
	if duration < 15*time.Minute {
		return fmt.Sprintf("Short window: %d failures over %s. Consistent with a dependency outage or rolling deploy.", count, formatDuration(duration))
	}
	if duration < time.Hour {
		return fmt.Sprintf("Medium window: %d failures over %s.", count, formatDuration(duration))
	}
	rate := float64(count) / duration.Hours()
	if rate > 100 {
		return fmt.Sprintf("Sustained high rate: %.0f failures/hour over %s.", rate, formatDuration(duration))
	}
	return ""
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

	parts = append(parts, fmt.Sprintf(
		"%d messages across %d failure classes (window: %s).",
		s.TotalMessages, s.TotalClasses, s.WindowDuration,
	))

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

	// Biggest class
	if len(s.Timelines) > 0 {
		top := s.Timelines[0]
		parts = append(parts, fmt.Sprintf(
			"Largest class: \"%s\" with %d messages (%.0f%%).",
			top.FailureClass, top.Count, top.Percentage,
		))
	}

	return joinSentences(parts)
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
