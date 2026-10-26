package collector

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/doganaydindev/dlqautopsy/internal/storage"
)

// scriptedSQS returns batches of the given sizes, one per receive, and empty
// responses once the script runs out.
type scriptedSQS struct {
	batches  []int
	receives int
	nextID   int
}

func (s *scriptedSQS) ReceiveMessage(_ context.Context, _ *sqs.ReceiveMessageInput, _ ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error) {
	i := s.receives
	s.receives++
	if i >= len(s.batches) {
		return &sqs.ReceiveMessageOutput{}, nil
	}
	var msgs []types.Message
	for j := 0; j < s.batches[i]; j++ {
		s.nextID++
		id := fmt.Sprintf("m-%d", s.nextID)
		body := `{"error":"x"}`
		msgs = append(msgs, types.Message{MessageId: &id, Body: &body, Attributes: map[string]string{"ApproximateReceiveCount": "1"}})
	}
	return &sqs.ReceiveMessageOutput{Messages: msgs}, nil
}

func (s *scriptedSQS) DeleteMessage(context.Context, *sqs.DeleteMessageInput, ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error) {
	return &sqs.DeleteMessageOutput{}, nil
}

func TestEmptyReceives(t *testing.T) {
	// 2 messages, empty, 3 messages, empty, empty, 1 message, then empty forever.
	script := []int{2, 0, 3, 0, 0, 1}
	tests := []struct {
		name          string
		emptyReceives int
		wantIngested  int
		wantReceives  int
	}{
		{"unset means 1: stop at the first empty receive", 0, 2, 2},
		{"1 stops at the first empty receive", 1, 2, 2},
		{"2 tolerates one empty receive, stops at two in a row", 2, 5, 5},
		{"3 reads everything, then needs three empty receives in a row", 3, 6, 9},
		{"a non-empty receive resets the counter", 4, 6, 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, err := storage.NewDB(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			fake := &scriptedSQS{batches: script}
			c := NewSQSCollector(db, "q", "run", SQSOptions{EmptyReceives: tt.emptyReceives})
			c.Client = fake
			if err := c.Run(context.Background()); err != nil {
				t.Fatal(err)
			}
			events, err := db.GetUnnormalizedEvents("run")
			if err != nil {
				t.Fatal(err)
			}
			if len(events) != tt.wantIngested {
				t.Errorf("ingested %d messages, want %d", len(events), tt.wantIngested)
			}
			if fake.receives != tt.wantReceives {
				t.Errorf("made %d receives, want %d", fake.receives, tt.wantReceives)
			}
		})
	}
}
