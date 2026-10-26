package normalizer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/doganaydindev/dlqautopsy/internal/model"
	"github.com/doganaydindev/dlqautopsy/internal/storage"
)

var (
	memAddrRegex = regexp.MustCompile(`0x[0-9a-fA-F]+`)
	uuidRegex    = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	// Optional fractional seconds and zone suffix are consumed too, so that
	// "...:22.123Z" and "...:22.456Z" normalize to the same signature.
	timestampRegex  = regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:?\d{2})?`)
	ipRegex         = regexp.MustCompile(`\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}(:\d+)?`)
	lineNumberRegex = regexp.MustCompile(`\.go:\d+`)
	// Runs of five or more digits are treated as volatile values (process
	// ids, row ids, offsets, byte sizes, Unix timestamps). Shorter numbers are
	// kept on purpose: HTTP status codes (503), MySQL error codes (1213) and
	// most ports carry meaning, and the classifier's status-code keywords are
	// three digits long. See docs/failure-model.md.
	longNumberRegex = regexp.MustCompile(`\d{5,}`)
)

// MinVolatileDigits is the length from which a run of digits is replaced by
// [NUM] in the error signature.
const MinVolatileDigits = 5

// Common field names that typically contain error information.
// Checked in priority order -- first match wins.
var errorFieldNames = []string{
	"error", "errorMessage", "error_message",
	"Error", "ErrorMessage",
	"err", "message", "msg",
	"cause", "reason", "detail",
	"stackTrace", "stack_trace", "stacktrace",
	"exception", "Exception",
}

// Normalizer cleans stacktraces, normalizes payloads, and hashes error signatures.
type Normalizer struct {
	db      *storage.DB
	runName string
}

func NewNormalizer(db *storage.DB, runName string) *Normalizer {
	return &Normalizer{db: db, runName: runName}
}

// Run iterates over all un-normalized raw events and generates normalized versions.
func (n *Normalizer) Run(ctx context.Context) error {
	events, err := n.db.GetUnnormalizedEvents(n.runName)
	if err != nil {
		return fmt.Errorf("failed to get unnormalized events: %w", err)
	}

	fmt.Printf("Normalizing %d raw events...\n", len(events))
	normalizedCount := 0

	for _, event := range events {
		errorText, payloadShape := n.extractErrorAndShape(event.Body)
		normalizedError, signature := n.normalize(errorText)

		fr := model.FailureRecord{
			RawEventID:      event.ID,
			ErrorSignature:  signature,
			NormalizedError: normalizedError,
			PayloadShape:    payloadShape,
			FailureClass:    "", // To be populated by Classifier
			Classification:  "", // To be populated by Classifier
		}

		if err := n.db.InsertFailureRecord(n.runName, fr); err != nil {
			fmt.Printf("Error inserting failure record for %s: %v\n", event.ID, err)
		} else {
			normalizedCount++
		}
	}

	fmt.Printf("Normalization complete. %d records inserted.\n", normalizedCount)
	return nil
}

// extractErrorAndShape parses the message body as JSON, extracts the error
// field specifically, and computes a payload shape fingerprint from the key structure.
// If the body is not JSON, the full body is treated as the error text.
func (n *Normalizer) extractErrorAndShape(rawBody string) (errorText string, payloadShape string) {
	trimmed := strings.TrimSpace(rawBody)

	// Try to parse as JSON object
	var parsed map[string]any
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		// Not JSON -- treat entire body as error text, no shape
		return trimmed, ""
	}

	// Compute payload shape from JSON key structure
	payloadShape = computePayloadShape(parsed, "")

	// Try to extract error text from known error field names
	for _, field := range errorFieldNames {
		if val, ok := parsed[field]; ok {
			switch v := val.(type) {
			case string:
				if v != "" {
					return v, payloadShape
				}
			case map[string]any:
				// Nested error object -- try message inside it
				if msg, ok := v["message"]; ok {
					if s, ok := msg.(string); ok && s != "" {
						return s, payloadShape
					}
				}
				// Serialize the error object
				b, _ := json.Marshal(v)
				return string(b), payloadShape
			}
		}
	}

	// No known error field found. Fall back to the full body.
	return trimmed, payloadShape
}

// computePayloadShape recursively extracts sorted key paths from a JSON object.
// Example: {"error": "x", "payload": {"action": "y", "id": 1}} => "error,payload.action,payload.id"
func computePayloadShape(obj map[string]any, prefix string) string {
	var keys []string
	for k, v := range obj {
		fullKey := k
		if prefix != "" {
			fullKey = prefix + "." + k
		}
		switch child := v.(type) {
		case map[string]any:
			// Recurse into nested objects
			nested := computePayloadShape(child, fullKey)
			if nested != "" {
				keys = append(keys, nested)
			}
		default:
			keys = append(keys, fullKey)
		}
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

// NormalizeError returns the normalized error text and its signature. It is
// the same transformation the pipeline applies before classification.
func NormalizeError(errorText string) (normalized string, signature string) {
	return (&Normalizer{}).normalize(errorText)
}

// normalize processes the extracted error text into a stable signature.
// This operates on the error string only (not the full payload), so the
// error message keywords remain intact for classification.
func (n *Normalizer) normalize(errorText string) (normalized string, signature string) {
	normalized = errorText

	// Strip memory addresses
	normalized = memAddrRegex.ReplaceAllString(normalized, "[ADDR]")

	// Strip UUIDs
	normalized = uuidRegex.ReplaceAllString(normalized, "[UUID]")

	// Strip timestamps
	normalized = timestampRegex.ReplaceAllString(normalized, "[TIMESTAMP]")

	// Strip IP addresses
	normalized = ipRegex.ReplaceAllString(normalized, "[IP]")

	// Normalize Go file:line references (goroutine.go:123 -> goroutine.go:[LINE])
	normalized = lineNumberRegex.ReplaceAllString(normalized, ".go:[LINE]")

	// Collapse long numbers last, so the patterns above (timestamps, IPs,
	// line numbers) see the original digits.
	normalized = longNumberRegex.ReplaceAllString(normalized, "[NUM]")

	normalized = strings.TrimSpace(normalized)

	hash := sha256.Sum256([]byte(normalized))
	signature = hex.EncodeToString(hash[:])

	return normalized, signature
}
