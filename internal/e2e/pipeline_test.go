// Package e2e runs the whole pipeline (collector -> normalizer -> classifier
// -> analyzer) against fixture messages served by an in-memory fake SQS
// client. No AWS account or network is needed.
package e2e

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/doganaydindev/dlqautopsy/internal/analyzer"
	"github.com/doganaydindev/dlqautopsy/internal/classifier"
	"github.com/doganaydindev/dlqautopsy/internal/collector"
	"github.com/doganaydindev/dlqautopsy/internal/normalizer"
	"github.com/doganaydindev/dlqautopsy/internal/storage"
)

type fixtureMessage struct {
	ID           string    `json:"id"`
	ReceiveCount int       `json:"receive_count"`
	SentAt       time.Time `json:"sent_at"`
	Body         string    `json:"body"`
}

// fakeSQS hands out the fixture messages in batches of up to 10, then
// returns an empty receive, like a drained queue.
type fakeSQS struct {
	msgs    []types.Message
	next    int
	deleted []string
}

func newFakeSQS(t *testing.T, fixtures []fixtureMessage) *fakeSQS {
	t.Helper()
	f := &fakeSQS{}
	for i := range fixtures {
		m := fixtures[i]
		id, body, handle := m.ID, m.Body, "rh-"+m.ID
		f.msgs = append(f.msgs, types.Message{
			MessageId:     &id,
			Body:          &body,
			ReceiptHandle: &handle,
			Attributes: map[string]string{
				string(types.MessageSystemAttributeNameApproximateReceiveCount): strconv.Itoa(m.ReceiveCount),
				string(types.MessageSystemAttributeNameSentTimestamp):           strconv.FormatInt(m.SentAt.UnixMilli(), 10),
			},
		})
	}
	return f
}

func (f *fakeSQS) ReceiveMessage(_ context.Context, in *sqs.ReceiveMessageInput, _ ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error) {
	n := int(in.MaxNumberOfMessages)
	if n <= 0 || n > 10 {
		n = 10
	}
	end := min(f.next+n, len(f.msgs))
	out := &sqs.ReceiveMessageOutput{Messages: f.msgs[f.next:end]}
	f.next = end
	return out, nil
}

func (f *fakeSQS) DeleteMessage(_ context.Context, in *sqs.DeleteMessageInput, _ ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error) {
	f.deleted = append(f.deleted, *in.ReceiptHandle)
	return &sqs.DeleteMessageOutput{}, nil
}

func loadFixtures(t *testing.T) []fixtureMessage {
	t.Helper()
	b, err := os.ReadFile("testdata/dlq_messages.json")
	if err != nil {
		t.Fatal(err)
	}
	var fx []fixtureMessage
	if err := json.Unmarshal(b, &fx); err != nil {
		t.Fatal(err)
	}
	return fx
}

func ingestAndClassify(t *testing.T, db *storage.DB, run string, fixtures []fixtureMessage, purge bool) *fakeSQS {
	t.Helper()
	ctx := context.Background()
	fake := newFakeSQS(t, fixtures)
	col := collector.NewSQSCollector(db, "https://sqs.example.invalid/000000000000/test-dlq", run, collector.SQSOptions{Purge: purge})
	col.Client = fake
	if err := col.Run(ctx); err != nil {
		t.Fatalf("collector: %v", err)
	}
	if err := normalizer.NewNormalizer(db, run).Run(ctx); err != nil {
		t.Fatalf("normalizer: %v", err)
	}
	if err := classifier.NewClassifier(db, run).Run(ctx); err != nil {
		t.Fatalf("classifier: %v", err)
	}
	return fake
}

func TestPipeline_FixtureRun(t *testing.T) {
	db, err := storage.NewDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	fixtures := loadFixtures(t)
	fake := ingestAndClassify(t, db, "fixture-run", fixtures, true)

	if len(fake.deleted) != len(fixtures) {
		t.Errorf("purge deleted %d messages, want %d", len(fake.deleted), len(fixtures))
	}

	sums, err := db.GetClassSummaries("fixture-run")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	for _, s := range sums {
		got[s.FailureClass] += s.Count
	}
	unknownSig := ""
	for class := range got {
		if len(class) > len("Unknown-") && class[:len("Unknown-")] == "Unknown-" {
			unknownSig = class
		}
	}

	want := map[string]int{
		"Downstream Dependency": 4,
		"Poison Message":        2,
		"Transient Error":       1,
		"Auth Failure":          1,
	}
	for class, n := range want {
		if got[class] != n {
			t.Errorf("%s = %d, want %d (all: %v)", class, got[class], n, got)
		}
	}
	if unknownSig == "" || got[unknownSig] != 1 {
		t.Errorf("expected one Unknown-<sig> class, got %v", got)
	}

	// Two connection-refused messages differ only in IP, so they share a signature.
	refused, err := db.GetDetailsByClass("fixture-run", "Downstream Dependency", 20)
	if err != nil {
		t.Fatal(err)
	}
	sigs := map[string]int{}
	for _, r := range refused {
		sigs[r.ErrorSignature]++
	}
	if len(sigs) != 2 {
		t.Errorf("Downstream Dependency signatures = %d, want 2 (timeout + refused)", len(sigs))
	}

	summary, err := analyzer.NewAnalyzer(db, "fixture-run").Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if summary.TotalMessages != len(fixtures) {
		t.Errorf("TotalMessages = %d, want %d", summary.TotalMessages, len(fixtures))
	}
	// Poison(2) do_not_retry; Downstream(4) + Transient(1) retryable;
	// Auth(1) + Unknown(1) need investigation.
	if summary.DoNotRetryMsgs != 2 || summary.SafeToRetryMsgs != 5 {
		t.Errorf("do_not_retry=%d safe=%d, want 2 and 5", summary.DoNotRetryMsgs, summary.SafeToRetryMsgs)
	}
	wantStart := time.Date(2026, 2, 24, 10, 14, 32, 0, time.UTC)
	if !summary.WindowStart.Equal(wantStart) {
		t.Errorf("WindowStart = %v, want %v", summary.WindowStart, wantStart)
	}
	for _, tl := range summary.Timelines {
		if tl.FailureClass == "Poison Message" && tl.RetryPattern != "Instant Death" {
			t.Errorf("Poison Message retry pattern = %q, want Instant Death", tl.RetryPattern)
		}
	}
	if len(summary.Triage) != 3 || summary.Triage[0].Label != "Immediate Action Required" {
		t.Errorf("unexpected triage groups: %+v", summary.Triage)
	}
}

func TestPipeline_ReclassifyIsIdempotent(t *testing.T) {
	db, err := storage.NewDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	fixtures := loadFixtures(t)
	ingestAndClassify(t, db, "r", fixtures, false)
	// Ingesting the same messages again (e.g. inspect without --purge, twice)
	// must not duplicate rows.
	ingestAndClassify(t, db, "r", fixtures, false)

	runs, err := db.GetRuns()
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].MessageCount != len(fixtures) {
		t.Fatalf("runs = %+v, want one run with %d messages", runs, len(fixtures))
	}
}

func TestPipeline_TwoRunsDiffInputs(t *testing.T) {
	db, err := storage.NewDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	fixtures := loadFixtures(t)
	ingestAndClassify(t, db, "before", fixtures, false)
	// Second run: only the downstream timeouts remain.
	ingestAndClassify(t, db, "after", fixtures[:2], false)

	before, _ := db.GetClassSummaries("before")
	after, _ := db.GetClassSummaries("after")
	if len(before) <= len(after) {
		t.Fatalf("expected fewer classes after: before=%d after=%d", len(before), len(after))
	}
	if len(after) != 1 || after[0].FailureClass != "Downstream Dependency" || after[0].Count != 2 {
		t.Errorf("after = %+v", after)
	}
}
