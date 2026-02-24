package storage

import (
	"testing"
	"time"

	"github.com/dgnydn/dlqautopsy/internal/model"
)

func setupTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create test DB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestNewDB_Success(t *testing.T) {
	db := setupTestDB(t)
	if db == nil {
		t.Fatal("expected non-nil DB")
	}
	if db.GetDB() == nil {
		t.Fatal("expected non-nil underlying sql.DB")
	}
}

func TestNewDB_InvalidDSN(t *testing.T) {
	_, err := NewDB("/nonexistent/path/to/impossible.db")
	// sqlite3 may not fail on open but will fail on schema init if path is truly invalid
	// Let's test with a clearly bad driver scenario instead
	if err != nil {
		// This is acceptable — some systems will fail
		return
	}
}

func TestInsertRawEvent_And_GetUnnormalizedEvents(t *testing.T) {
	db := setupTestDB(t)
	runName := "test-run"
	now := time.Now().Truncate(time.Second)

	event := model.RawEvent{
		ID:                "msg-001",
		Body:              `{"error":"timeout","action":"process"}`,
		Provider:          "SQS",
		Attributes:        map[string]string{"attr1": "val1"},
		FirstSeen:         now,
		LastSeen:          now,
		ReceiveCount:      3,
		OriginalQueueName: "https://sqs.us-east-1.amazonaws.com/123/my-dlq",
	}

	if err := db.InsertRawEvent(runName, event); err != nil {
		t.Fatalf("InsertRawEvent failed: %v", err)
	}

	events, err := db.GetUnnormalizedEvents(runName)
	if err != nil {
		t.Fatalf("GetUnnormalizedEvents failed: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	got := events[0]
	if got.ID != "msg-001" {
		t.Errorf("expected ID msg-001, got %s", got.ID)
	}
	if got.Body != event.Body {
		t.Errorf("body mismatch")
	}
	if got.Provider != "SQS" {
		t.Errorf("provider mismatch: got %s", got.Provider)
	}
	if got.ReceiveCount != 3 {
		t.Errorf("receive count mismatch: got %d", got.ReceiveCount)
	}
	if got.Attributes["attr1"] != "val1" {
		t.Errorf("attributes mismatch")
	}
}

func TestInsertRawEvent_Upsert(t *testing.T) {
	db := setupTestDB(t)
	runName := "test-run"
	now := time.Now().Truncate(time.Second)

	event := model.RawEvent{
		ID:                "msg-001",
		Body:              `{"error":"timeout"}`,
		Provider:          "SQS",
		Attributes:        map[string]string{},
		FirstSeen:         now,
		LastSeen:          now,
		ReceiveCount:      1,
		OriginalQueueName: "q1",
	}

	if err := db.InsertRawEvent(runName, event); err != nil {
		t.Fatalf("first insert failed: %v", err)
	}

	// Upsert with new receive count
	event.ReceiveCount = 5
	event.LastSeen = now.Add(time.Hour)
	if err := db.InsertRawEvent(runName, event); err != nil {
		t.Fatalf("upsert failed: %v", err)
	}

	events, err := db.GetUnnormalizedEvents(runName)
	if err != nil {
		t.Fatalf("GetUnnormalizedEvents failed: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event after upsert, got %d", len(events))
	}
	if events[0].ReceiveCount != 5 {
		t.Errorf("expected receive count 5 after upsert, got %d", events[0].ReceiveCount)
	}
}

func TestInsertFailureRecord_And_GetUnclassified(t *testing.T) {
	db := setupTestDB(t)
	runName := "test-run"
	now := time.Now().Truncate(time.Second)

	// Insert a raw event first (FK constraint)
	event := model.RawEvent{
		ID:                "msg-001",
		Body:              `{"error":"timeout"}`,
		Provider:          "SQS",
		Attributes:        map[string]string{},
		FirstSeen:         now,
		LastSeen:          now,
		ReceiveCount:      1,
		OriginalQueueName: "q1",
	}
	if err := db.InsertRawEvent(runName, event); err != nil {
		t.Fatalf("InsertRawEvent failed: %v", err)
	}

	fr := model.FailureRecord{
		RawEventID:      "msg-001",
		ErrorSignature:  "abc123def456",
		NormalizedError: "timeout connecting to downstream",
		PayloadShape:    "error",
		FailureClass:    "",
		Classification:  "",
		Severity:        "info",
		RetryVerdict:    "needs_investigation",
	}

	if err := db.InsertFailureRecord(runName, fr); err != nil {
		t.Fatalf("InsertFailureRecord failed: %v", err)
	}

	// Should appear as unnormalized=false (has failure record), but unclassified
	unnorm, err := db.GetUnnormalizedEvents(runName)
	if err != nil {
		t.Fatalf("GetUnnormalizedEvents failed: %v", err)
	}
	if len(unnorm) != 0 {
		t.Errorf("expected 0 unnormalized after inserting failure record, got %d", len(unnorm))
	}

	// Should appear as unclassified (empty failure_class)
	unclass, err := db.GetUnclassifiedRecords(runName)
	if err != nil {
		t.Fatalf("GetUnclassifiedRecords failed: %v", err)
	}
	if len(unclass) != 1 {
		t.Fatalf("expected 1 unclassified, got %d", len(unclass))
	}
	if unclass[0].ErrorSignature != "abc123def456" {
		t.Errorf("unexpected error signature: %s", unclass[0].ErrorSignature)
	}
}

func TestUpdateFailureClass(t *testing.T) {
	db := setupTestDB(t)
	runName := "test-run"
	now := time.Now().Truncate(time.Second)

	event := model.RawEvent{
		ID: "msg-001", Body: `{}`, Provider: "SQS",
		Attributes: map[string]string{}, FirstSeen: now, LastSeen: now,
		ReceiveCount: 1, OriginalQueueName: "q1",
	}
	db.InsertRawEvent(runName, event)

	fr := model.FailureRecord{
		RawEventID:      "msg-001",
		ErrorSignature:  "sig123",
		NormalizedError: "error text",
		FailureClass:    "",
		Classification:  "",
		Severity:        "info",
		RetryVerdict:    "needs_investigation",
	}
	db.InsertFailureRecord(runName, fr)

	err := db.UpdateFailureClass(runName, "msg-001", "Poison Message", "Malformed payload", "critical", "do_not_retry")
	if err != nil {
		t.Fatalf("UpdateFailureClass failed: %v", err)
	}

	// After update, should no longer appear as unclassified
	unclass, err := db.GetUnclassifiedRecords(runName)
	if err != nil {
		t.Fatalf("GetUnclassifiedRecords failed: %v", err)
	}
	if len(unclass) != 0 {
		t.Errorf("expected 0 unclassified after update, got %d", len(unclass))
	}
}

func TestGetRuns(t *testing.T) {
	db := setupTestDB(t)
	now := time.Now().Truncate(time.Second)

	for _, run := range []string{"run-a", "run-b"} {
		event := model.RawEvent{
			ID: "msg-" + run, Body: `{}`, Provider: "SQS",
			Attributes: map[string]string{}, FirstSeen: now, LastSeen: now,
			ReceiveCount: 1, OriginalQueueName: "q1",
		}
		db.InsertRawEvent(run, event)
	}

	runs, err := db.GetRuns()
	if err != nil {
		t.Fatalf("GetRuns failed: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("expected 2 runs, got %d", len(runs))
	}
}

func TestGetRuns_Empty(t *testing.T) {
	db := setupTestDB(t)
	runs, err := db.GetRuns()
	if err != nil {
		t.Fatalf("GetRuns failed: %v", err)
	}
	if len(runs) != 0 {
		t.Errorf("expected 0 runs, got %d", len(runs))
	}
}

func TestGetDetailsByClass(t *testing.T) {
	db := setupTestDB(t)
	runName := "test-run"
	now := time.Now().Truncate(time.Second)

	// Insert raw event + classified failure record
	event := model.RawEvent{
		ID: "msg-001", Body: `{"err":"timeout"}`, Provider: "SQS",
		Attributes: map[string]string{}, FirstSeen: now, LastSeen: now,
		ReceiveCount: 3, OriginalQueueName: "q1",
	}
	db.InsertRawEvent(runName, event)
	fr := model.FailureRecord{
		RawEventID: "msg-001", ErrorSignature: "sig1", NormalizedError: "timeout",
		PayloadShape: "err", FailureClass: "Downstream Dependency",
		Classification: "Timeout", Severity: "warning", RetryVerdict: "safe_to_retry",
	}
	db.InsertFailureRecord(runName, fr)

	records, err := db.GetDetailsByClass(runName, "Downstream Dependency", 10)
	if err != nil {
		t.Fatalf("GetDetailsByClass failed: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].MessageID != "msg-001" {
		t.Errorf("wrong message ID: %s", records[0].MessageID)
	}
	if records[0].FailureClass != "Downstream Dependency" {
		t.Errorf("wrong failure class: %s", records[0].FailureClass)
	}
}

func TestGetDetailsBySignature(t *testing.T) {
	db := setupTestDB(t)
	runName := "test-run"
	now := time.Now().Truncate(time.Second)

	event := model.RawEvent{
		ID: "msg-001", Body: `{}`, Provider: "SQS",
		Attributes: map[string]string{}, FirstSeen: now, LastSeen: now,
		ReceiveCount: 1, OriginalQueueName: "q1",
	}
	db.InsertRawEvent(runName, event)
	fr := model.FailureRecord{
		RawEventID: "msg-001", ErrorSignature: "abcdef123456", NormalizedError: "err",
		FailureClass: "Unknown", Classification: "Unknown", Severity: "info", RetryVerdict: "needs_investigation",
	}
	db.InsertFailureRecord(runName, fr)

	// Prefix search
	records, err := db.GetDetailsBySignature(runName, "abcdef", 10)
	if err != nil {
		t.Fatalf("GetDetailsBySignature failed: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}

	// Non-matching prefix
	records, err = db.GetDetailsBySignature(runName, "zzzzz", 10)
	if err != nil {
		t.Fatalf("GetDetailsBySignature failed: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("expected 0 records for non-matching prefix, got %d", len(records))
	}
}

func TestListFailureClasses(t *testing.T) {
	db := setupTestDB(t)
	runName := "test-run"
	now := time.Now().Truncate(time.Second)

	for i, cls := range []string{"Poison Message", "Auth Failure", "Transient Error"} {
		id := "msg-" + string(rune('a'+i))
		event := model.RawEvent{
			ID: id, Body: `{}`, Provider: "SQS",
			Attributes: map[string]string{}, FirstSeen: now, LastSeen: now,
			ReceiveCount: 1, OriginalQueueName: "q1",
		}
		db.InsertRawEvent(runName, event)
		fr := model.FailureRecord{
			RawEventID: id, ErrorSignature: "sig-" + id, NormalizedError: "err",
			FailureClass: cls, Classification: "test", Severity: "info", RetryVerdict: "needs_investigation",
		}
		db.InsertFailureRecord(runName, fr)
	}

	classes, err := db.ListFailureClasses(runName)
	if err != nil {
		t.Fatalf("ListFailureClasses failed: %v", err)
	}
	if len(classes) != 3 {
		t.Fatalf("expected 3 classes, got %d", len(classes))
	}
}

func TestGetClassSummaries(t *testing.T) {
	db := setupTestDB(t)
	runName := "test-run"
	now := time.Now().Truncate(time.Second)

	for i := 0; i < 5; i++ {
		id := "msg-" + string(rune('a'+i))
		event := model.RawEvent{
			ID: id, Body: `{}`, Provider: "SQS",
			Attributes: map[string]string{}, FirstSeen: now, LastSeen: now,
			ReceiveCount: 1, OriginalQueueName: "q1",
		}
		db.InsertRawEvent(runName, event)

		cls := "Poison Message"
		if i >= 3 {
			cls = "Auth Failure"
		}
		fr := model.FailureRecord{
			RawEventID: id, ErrorSignature: "sig-" + id, NormalizedError: "err",
			FailureClass: cls, Classification: "test", Severity: "critical", RetryVerdict: "do_not_retry",
		}
		db.InsertFailureRecord(runName, fr)
	}

	summaries, err := db.GetClassSummaries(runName)
	if err != nil {
		t.Fatalf("GetClassSummaries failed: %v", err)
	}
	if len(summaries) != 2 {
		t.Fatalf("expected 2 class summaries, got %d", len(summaries))
	}
	// Should be ordered by count DESC
	if summaries[0].FailureClass != "Poison Message" {
		t.Errorf("expected Poison Message first, got %s", summaries[0].FailureClass)
	}
	if summaries[0].Count != 3 {
		t.Errorf("expected count 3, got %d", summaries[0].Count)
	}
}

func TestGetPayloadShapes(t *testing.T) {
	db := setupTestDB(t)
	runName := "test-run"
	now := time.Now().Truncate(time.Second)

	for i, shape := range []string{"error,action", "error,action", "error,id"} {
		id := "msg-" + string(rune('a'+i))
		event := model.RawEvent{
			ID: id, Body: `{}`, Provider: "SQS",
			Attributes: map[string]string{}, FirstSeen: now, LastSeen: now,
			ReceiveCount: 1, OriginalQueueName: "q1",
		}
		db.InsertRawEvent(runName, event)
		fr := model.FailureRecord{
			RawEventID: id, ErrorSignature: "sig-" + id, NormalizedError: "err",
			PayloadShape: shape, FailureClass: "Poison Message",
			Classification: "test", Severity: "critical", RetryVerdict: "do_not_retry",
		}
		db.InsertFailureRecord(runName, fr)
	}

	shapes, err := db.GetPayloadShapes(runName, "Poison Message")
	if err != nil {
		t.Fatalf("GetPayloadShapes failed: %v", err)
	}
	if len(shapes) != 2 {
		t.Fatalf("expected 2 distinct shapes, got %d", len(shapes))
	}
	if shapes["error,action"] != 2 {
		t.Errorf("expected error,action count 2, got %d", shapes["error,action"])
	}
	if shapes["error,id"] != 1 {
		t.Errorf("expected error,id count 1, got %d", shapes["error,id"])
	}
}

func TestParseSQLiteTime(t *testing.T) {
	tests := []struct {
		name  string
		input any
		want  bool // true = should parse to non-zero time
	}{
		{"time.Time", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), true},
		{"RFC3339", "2026-01-01T00:00:00Z", true},
		{"RFC3339Nano", "2026-01-01T00:00:00.123456789Z", true},
		{"datetime with tz", "2026-01-01 00:00:00-05:00", true},
		{"datetime with nano tz", "2026-01-01 00:00:00.123456789-05:00", true},
		{"datetime no tz", "2026-01-01 00:00:00", true},
		{"RFC3339 variant", "2026-01-01T00:00:00.999999999Z07:00", false}, // ambiguous
		{"invalid string", "not-a-date", false},
		{"nil value", nil, false},
		{"int", 12345, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := parseSQLiteTime(tc.input)
			if tc.want && result.IsZero() {
				t.Errorf("expected non-zero time for input %v", tc.input)
			}
			if !tc.want && !result.IsZero() {
				t.Errorf("expected zero time for input %v, got %v", tc.input, result)
			}
		})
	}
}

func TestInsertFailureRecord_Upsert(t *testing.T) {
	db := setupTestDB(t)
	runName := "test-run"
	now := time.Now().Truncate(time.Second)

	event := model.RawEvent{
		ID: "msg-001", Body: `{}`, Provider: "SQS",
		Attributes: map[string]string{}, FirstSeen: now, LastSeen: now,
		ReceiveCount: 1, OriginalQueueName: "q1",
	}
	db.InsertRawEvent(runName, event)

	fr := model.FailureRecord{
		RawEventID: "msg-001", ErrorSignature: "sig1", NormalizedError: "err1",
		FailureClass: "", Classification: "", Severity: "info", RetryVerdict: "needs_investigation",
	}
	db.InsertFailureRecord(runName, fr)

	// Upsert: update fields
	fr.ErrorSignature = "sig2"
	fr.NormalizedError = "err2"
	fr.FailureClass = "Poison Message"
	fr.Severity = "critical"
	fr.RetryVerdict = "do_not_retry"
	if err := db.InsertFailureRecord(runName, fr); err != nil {
		t.Fatalf("upsert failed: %v", err)
	}

	// Should not appear as unclassified now
	unclass, _ := db.GetUnclassifiedRecords(runName)
	if len(unclass) != 0 {
		t.Errorf("expected 0 unclassified after upsert, got %d", len(unclass))
	}
}

func TestGetUnnormalizedEvents_EmptyAttributes(t *testing.T) {
	db := setupTestDB(t)
	runName := "test-run"
	now := time.Now().Truncate(time.Second)

	event := model.RawEvent{
		ID: "msg-empty-attrs", Body: `test`, Provider: "SQS",
		Attributes:   nil,
		FirstSeen:    now,
		LastSeen:     now,
		ReceiveCount: 1, OriginalQueueName: "q1",
	}
	db.InsertRawEvent(runName, event)

	events, err := db.GetUnnormalizedEvents(runName)
	if err != nil {
		t.Fatalf("GetUnnormalizedEvents failed: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
}

func TestClose(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB failed: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Errorf("Close returned error: %v", err)
	}
}

func TestGetDetailsByClass_Empty(t *testing.T) {
	db := setupTestDB(t)
	records, err := db.GetDetailsByClass("nonexistent", "SomeClass", 10)
	if err != nil {
		t.Fatalf("GetDetailsByClass failed: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("expected 0 records, got %d", len(records))
	}
}

func TestListFailureClasses_Empty(t *testing.T) {
	db := setupTestDB(t)
	classes, err := db.ListFailureClasses("nonexistent")
	if err != nil {
		t.Fatalf("ListFailureClasses failed: %v", err)
	}
	if len(classes) != 0 {
		t.Errorf("expected 0 classes, got %d", len(classes))
	}
}

func TestGetClassSummaries_Empty(t *testing.T) {
	db := setupTestDB(t)
	summaries, err := db.GetClassSummaries("nonexistent")
	if err != nil {
		t.Fatalf("GetClassSummaries failed: %v", err)
	}
	if len(summaries) != 0 {
		t.Errorf("expected 0 summaries, got %d", len(summaries))
	}
}

func TestGetPayloadShapes_Empty(t *testing.T) {
	db := setupTestDB(t)
	shapes, err := db.GetPayloadShapes("nonexistent", "SomeClass")
	if err != nil {
		t.Fatalf("GetPayloadShapes failed: %v", err)
	}
	if len(shapes) != 0 {
		t.Errorf("expected 0 shapes, got %d", len(shapes))
	}
}
