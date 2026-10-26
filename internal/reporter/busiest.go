package reporter

import "github.com/doganaydindev/dlqautopsy/internal/analyzer"

const busiestHourNote = "by SentTimestamp, busiest UTC clock hour"

// showBusiestHour reports whether the busiest-hour line adds information.
// When every message of a group was sent in the same clock hour, the number
// is just the group size, so the line is left out of text and markdown.
// JSON always carries busiest_hour_count.
func showBusiestHour(t analyzer.Timeline) bool {
	return t.BusiestHourCount > 0 && t.BusiestHourCount < t.Count
}
