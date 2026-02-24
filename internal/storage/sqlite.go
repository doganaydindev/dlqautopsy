package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dgnydn/dlqautopsy/internal/model"
	_ "github.com/mattn/go-sqlite3"
)

// DB represents the local SQLite database.
type DB struct {
	conn *sql.DB
}

// NewDB initializes the SQLite connection and ensures the schema exists.
func NewDB(dsn string) (*DB, error) {
	conn, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite database: %w", err)
	}

	db := &DB{conn: conn}
	if err := db.initSchema(); err != nil {
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	return db, nil
}

// Close closes the underlying database connection.
func (db *DB) Close() error {
	return db.conn.Close()
}

func (db *DB) initSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS raw_events (
		run_name TEXT NOT NULL,
		id TEXT NOT NULL,
		body TEXT NOT NULL,
		provider TEXT NOT NULL,
		attributes JSON,
		first_seen DATETIME NOT NULL,
		last_seen DATETIME NOT NULL,
		receive_count INTEGER NOT NULL,
		original_queue_name TEXT NOT NULL,
		PRIMARY KEY (run_name, id)
	);

	CREATE TABLE IF NOT EXISTS failure_records (
		run_name TEXT NOT NULL,
		raw_event_id TEXT NOT NULL,
		error_signature TEXT NOT NULL,
		normalized_error TEXT NOT NULL,
		payload_shape TEXT NOT NULL DEFAULT '',
		failure_class TEXT NOT NULL,
		classification TEXT NOT NULL,
		severity TEXT NOT NULL DEFAULT 'info',
		retry_verdict TEXT NOT NULL DEFAULT 'needs_investigation',
		PRIMARY KEY (run_name, raw_event_id),
		FOREIGN KEY(run_name, raw_event_id) REFERENCES raw_events(run_name, id)
	);

	CREATE INDEX IF NOT EXISTS idx_failure_records_class ON failure_records(run_name, failure_class);
	CREATE INDEX IF NOT EXISTS idx_failure_records_signature ON failure_records(run_name, error_signature);
	`
	_, err := db.conn.Exec(schema)
	return err
}

// InsertRawEvent stores a new message from the queue within a specific run.
func (db *DB) InsertRawEvent(runName string, event model.RawEvent) error {
	attrs, err := json.Marshal(event.Attributes)
	if err != nil {
		return fmt.Errorf("failed to marshal attributes: %w", err)
	}

	query := `
	INSERT INTO raw_events (run_name, id, body, provider, attributes, first_seen, last_seen, receive_count, original_queue_name)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(run_name, id) DO UPDATE SET
		last_seen = excluded.last_seen,
		receive_count = excluded.receive_count
	`
	_, err = db.conn.Exec(query,
		runName,
		event.ID,
		event.Body,
		event.Provider,
		string(attrs),
		event.FirstSeen,
		event.LastSeen,
		event.ReceiveCount,
		event.OriginalQueueName,
	)
	return err
}

// GetUnnormalizedEvents returns raw events for a specific run that don't have a corresponding failure record.
func (db *DB) GetUnnormalizedEvents(runName string) ([]model.RawEvent, error) {
	query := `
	SELECT r.id, r.body, r.provider, r.attributes, r.first_seen, r.last_seen, r.receive_count, r.original_queue_name
	FROM raw_events r
	LEFT JOIN failure_records f ON r.run_name = f.run_name AND r.id = f.raw_event_id
	WHERE r.run_name = ? AND f.raw_event_id IS NULL
	`
	rows, err := db.conn.Query(query, runName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []model.RawEvent
	for rows.Next() {
		var e model.RawEvent
		var attrs string
		var firstSeen, lastSeen time.Time

		err := rows.Scan(
			&e.ID, &e.Body, &e.Provider, &attrs,
			&firstSeen, &lastSeen, &e.ReceiveCount, &e.OriginalQueueName,
		)
		if err != nil {
			return nil, err
		}

		e.FirstSeen = firstSeen
		e.LastSeen = lastSeen

		if attrs != "" {
			if err := json.Unmarshal([]byte(attrs), &e.Attributes); err != nil {
				// Ignore unmarshal error on read
			}
		}

		events = append(events, e)
	}
	return events, rows.Err()
}

// InsertFailureRecord stores a new failure record or updates an existing one for a specific run.
func (db *DB) InsertFailureRecord(runName string, fr model.FailureRecord) error {
	query := `
	INSERT INTO failure_records (run_name, raw_event_id, error_signature, normalized_error, payload_shape, failure_class, classification, severity, retry_verdict)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(run_name, raw_event_id) DO UPDATE SET
		error_signature = excluded.error_signature,
		normalized_error = excluded.normalized_error,
		payload_shape = excluded.payload_shape,
		failure_class = excluded.failure_class,
		classification = excluded.classification,
		severity = excluded.severity,
		retry_verdict = excluded.retry_verdict
	`
	_, err := db.conn.Exec(query,
		runName,
		fr.RawEventID,
		fr.ErrorSignature,
		fr.NormalizedError,
		fr.PayloadShape,
		fr.FailureClass,
		fr.Classification,
		fr.Severity,
		fr.RetryVerdict,
	)
	return err
}

// GetUnclassifiedRecords returns failure records for a specific run that have no failure class.
func (db *DB) GetUnclassifiedRecords(runName string) ([]model.FailureRecord, error) {
	query := `
	SELECT raw_event_id, error_signature, normalized_error, payload_shape, failure_class, classification, severity, retry_verdict
	FROM failure_records
	WHERE run_name = ? AND (failure_class = '' OR failure_class IS NULL)
	`
	rows, err := db.conn.Query(query, runName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []model.FailureRecord
	for rows.Next() {
		var r model.FailureRecord
		err := rows.Scan(
			&r.RawEventID, &r.ErrorSignature, &r.NormalizedError, &r.PayloadShape,
			&r.FailureClass, &r.Classification, &r.Severity, &r.RetryVerdict,
		)
		if err != nil {
			return nil, err
		}
		records = append(records, r)
	}
	return records, rows.Err()
}

// UpdateFailureClass updates the classification for a specific failure record within a run.
func (db *DB) UpdateFailureClass(runName, rawEventID, failureClass, classification, severity, retryVerdict string) error {
	query := `
	UPDATE failure_records
	SET failure_class = ?, classification = ?, severity = ?, retry_verdict = ?
	WHERE run_name = ? AND raw_event_id = ?
	`
	_, err := db.conn.Exec(query, failureClass, classification, severity, retryVerdict, runName, rawEventID)
	return err
}

// GetRuns returns a list of all runs stored in the database.
func (db *DB) GetRuns() ([]model.RunSummary, error) {
	query := `
	SELECT run_name, COUNT(*), MIN(first_seen), MAX(last_seen)
	FROM raw_events
	GROUP BY run_name
	ORDER BY MAX(last_seen) DESC
	`
	rows, err := db.conn.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []model.RunSummary
	for rows.Next() {
		var r model.RunSummary
		var firstAny, lastAny any
		if err := rows.Scan(&r.Name, &r.MessageCount, &firstAny, &lastAny); err != nil {
			return nil, err
		}

		r.FirstSeen = parseSQLiteTime(firstAny)
		r.LastSeen = parseSQLiteTime(lastAny)

		runs = append(runs, r)
	}
	return runs, rows.Err()
}

// parseSQLiteTime converts a value from SQLite (string or time.Time) into a Go time.Time.
func parseSQLiteTime(val any) time.Time {
	if t, ok := val.(time.Time); ok {
		return t
	}
	if s, ok := val.(string); ok {
		formats := []string{
			time.RFC3339Nano,
			time.RFC3339,
			"2006-01-02T15:04:05Z",
			"2006-01-02 15:04:05.999999999-07:00",
			"2006-01-02 15:04:05-07:00",
			"2006-01-02 15:04:05",
			"2006-01-02T15:04:05.999999999Z07:00",
		}
		for _, f := range formats {
			if t, err := time.Parse(f, s); err == nil {
				return t
			}
		}
	}
	return time.Time{}
}

// GetDB returns the underlying sql.DB connection.
func (db *DB) GetDB() *sql.DB {
	return db.conn
}

// DetailRecord is a joined view of a failure record + raw event for the detail command.
type DetailRecord struct {
	MessageID       string
	Body            string
	ReceiveCount    int
	FirstSeen       time.Time
	LastSeen        time.Time
	ErrorSignature  string
	NormalizedError string
	PayloadShape    string
	FailureClass    string
	Classification  string
	Severity        string
	RetryVerdict    string
}

// GetDetailsByClass returns all messages for a specific failure class within a run.
func (db *DB) GetDetailsByClass(runName, failureClass string, limit int) ([]DetailRecord, error) {
	query := `
	SELECT r.id, r.body, r.receive_count, r.first_seen, r.last_seen,
		   f.error_signature, f.normalized_error, f.payload_shape,
		   f.failure_class, f.classification, f.severity, f.retry_verdict
	FROM failure_records f
	JOIN raw_events r ON f.run_name = r.run_name AND f.raw_event_id = r.id
	WHERE f.run_name = ? AND f.failure_class = ?
	ORDER BY r.first_seen DESC
	LIMIT ?
	`
	rows, err := db.conn.Query(query, runName, failureClass, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []DetailRecord
	for rows.Next() {
		var d DetailRecord
		var firstSeen, lastSeen time.Time
		err := rows.Scan(
			&d.MessageID, &d.Body, &d.ReceiveCount, &firstSeen, &lastSeen,
			&d.ErrorSignature, &d.NormalizedError, &d.PayloadShape,
			&d.FailureClass, &d.Classification, &d.Severity, &d.RetryVerdict,
		)
		if err != nil {
			return nil, err
		}
		d.FirstSeen = firstSeen
		d.LastSeen = lastSeen
		records = append(records, d)
	}
	return records, rows.Err()
}

// GetDetailsBySignature returns all messages sharing a specific error signature within a run.
func (db *DB) GetDetailsBySignature(runName, signature string, limit int) ([]DetailRecord, error) {
	query := `
	SELECT r.id, r.body, r.receive_count, r.first_seen, r.last_seen,
		   f.error_signature, f.normalized_error, f.payload_shape,
		   f.failure_class, f.classification, f.severity, f.retry_verdict
	FROM failure_records f
	JOIN raw_events r ON f.run_name = r.run_name AND f.raw_event_id = r.id
	WHERE f.run_name = ? AND f.error_signature LIKE ?
	ORDER BY r.first_seen DESC
	LIMIT ?
	`
	rows, err := db.conn.Query(query, runName, signature+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []DetailRecord
	for rows.Next() {
		var d DetailRecord
		var firstSeen, lastSeen time.Time
		err := rows.Scan(
			&d.MessageID, &d.Body, &d.ReceiveCount, &firstSeen, &lastSeen,
			&d.ErrorSignature, &d.NormalizedError, &d.PayloadShape,
			&d.FailureClass, &d.Classification, &d.Severity, &d.RetryVerdict,
		)
		if err != nil {
			return nil, err
		}
		d.FirstSeen = firstSeen
		d.LastSeen = lastSeen
		records = append(records, d)
	}
	return records, rows.Err()
}

// ListFailureClasses returns all distinct failure classes for a run.
func (db *DB) ListFailureClasses(runName string) ([]string, error) {
	query := `
	SELECT DISTINCT failure_class FROM failure_records
	WHERE run_name = ? AND failure_class != ''
	ORDER BY failure_class
	`
	rows, err := db.conn.Query(query, runName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var classes []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		classes = append(classes, c)
	}
	return classes, rows.Err()
}

// DiffClassSummary holds per-class counts for diffing two runs.
type DiffClassSummary struct {
	FailureClass   string
	Classification string
	Severity       string
	RetryVerdict   string
	Count          int
}

// GetClassSummaries returns per-class message counts for a run.
func (db *DB) GetClassSummaries(runName string) ([]DiffClassSummary, error) {
	query := `
	SELECT failure_class, classification, severity, retry_verdict, COUNT(*) as cnt
	FROM failure_records
	WHERE run_name = ? AND failure_class != ''
	GROUP BY failure_class, classification, severity, retry_verdict
	ORDER BY cnt DESC
	`
	rows, err := db.conn.Query(query, runName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var summaries []DiffClassSummary
	for rows.Next() {
		var s DiffClassSummary
		if err := rows.Scan(&s.FailureClass, &s.Classification, &s.Severity, &s.RetryVerdict, &s.Count); err != nil {
			return nil, err
		}
		summaries = append(summaries, s)
	}
	return summaries, rows.Err()
}

// RunInfo holds info about a stored run for display purposes.
type RunInfo = model.RunSummary

// GetPayloadShapes returns distinct payload shapes and their counts for a failure class.
func (db *DB) GetPayloadShapes(runName, failureClass string) (map[string]int, error) {
	query := `
	SELECT payload_shape, COUNT(*) as cnt
	FROM failure_records
	WHERE run_name = ? AND failure_class = ? AND payload_shape != ''
	GROUP BY payload_shape
	ORDER BY cnt DESC
	`
	rows, err := db.conn.Query(query, runName, failureClass)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	shapes := make(map[string]int)
	for rows.Next() {
		var shape string
		var cnt int
		if err := rows.Scan(&shape, &cnt); err != nil {
			return nil, err
		}
		shapes[shape] = cnt
	}
	return shapes, rows.Err()
}
