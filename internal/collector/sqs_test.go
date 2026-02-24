package collector

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/dgnydn/dlqautopsy/internal/storage"
)

type mockSQS struct {
	receiveCount int
	receiveMock  func(ctx context.Context, params *sqs.ReceiveMessageInput) (*sqs.ReceiveMessageOutput, error)
	deleteMock   func(ctx context.Context, params *sqs.DeleteMessageInput) (*sqs.DeleteMessageOutput, error)
}

func (m *mockSQS) ReceiveMessage(ctx context.Context, params *sqs.ReceiveMessageInput, optFns ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error) {
	m.receiveCount++
	if m.receiveMock != nil {
		return m.receiveMock(ctx, params)
	}
	return &sqs.ReceiveMessageOutput{}, nil
}

func (m *mockSQS) DeleteMessage(ctx context.Context, params *sqs.DeleteMessageInput, optFns ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error) {
	if m.deleteMock != nil {
		return m.deleteMock(ctx, params)
	}
	return &sqs.DeleteMessageOutput{}, nil
}

func TestSQSCollector_Run(t *testing.T) {
	db, _ := storage.NewDB(":memory:")
	defer db.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mock := &mockSQS{}
	mock.receiveMock = func(ctx context.Context, params *sqs.ReceiveMessageInput) (*sqs.ReceiveMessageOutput, error) {
		if mock.receiveCount > 1 {
			// Second pull returns empty, breaking the loop smoothly
			return &sqs.ReceiveMessageOutput{}, nil
		}

		// First pull returns messages
		id1 := "msg-1"
		body1 := `{"error":"test1"}`
		rc1 := "1"
		rh1 := "handle-1"

		ts := strconv.FormatInt(time.Now().UnixMilli(), 10)

		msg1 := types.Message{
			MessageId:     &id1,
			Body:          &body1,
			ReceiptHandle: &rh1,
			Attributes: map[string]string{
				string(types.MessageSystemAttributeNameApproximateReceiveCount): rc1,
				string(types.MessageSystemAttributeNameSentTimestamp):           ts,
			},
		}
		return &sqs.ReceiveMessageOutput{
			Messages: []types.Message{msg1},
		}, nil
	}

	c := NewSQSCollector(db, "fake-queue", "test-run", SQSOptions{Purge: true})
	c.Client = mock

	err := c.Run(ctx)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	events, _ := db.GetUnnormalizedEvents("test-run")
	if len(events) != 1 {
		t.Errorf("expected 1 event, got %d", len(events))
	}
}

func TestSQSCollector_Run_Error(t *testing.T) {
	db, _ := storage.NewDB(":memory:")
	defer db.Close()

	mock := &mockSQS{
		receiveMock: func(ctx context.Context, params *sqs.ReceiveMessageInput) (*sqs.ReceiveMessageOutput, error) {
			return nil, fmt.Errorf("network error")
		},
	}

	c := NewSQSCollector(db, "fake-queue", "test-run", SQSOptions{})
	c.Client = mock

	err := c.Run(context.Background())
	if err == nil {
		t.Errorf("expected error")
	}
}

func TestProcessMessage(t *testing.T) {
	db, err := storage.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}
	defer db.Close()

	c := NewSQSCollector(db, "test-queue", "test-run", SQSOptions{})

	msgId := "msg-123"
	body := `{"test": "data"}`
	rc := "3"

	ts := time.Now().Add(-time.Hour).UnixMilli()
	tsStr := strconv.FormatInt(ts, 10)

	msg := types.Message{
		MessageId: &msgId,
		Body:      &body,
		Attributes: map[string]string{
			string(types.MessageSystemAttributeNameApproximateReceiveCount): rc,
			string(types.MessageSystemAttributeNameSentTimestamp):           tsStr,
		},
		MessageAttributes: map[string]types.MessageAttributeValue{
			"custom": {StringValue: new(string)},
		},
	}
	*msg.MessageAttributes["custom"].StringValue = "val"

	err = c.processMessage(msg)
	if err != nil {
		t.Fatalf("processing failed: %v", err)
	}

	unnormalized, err := db.GetUnnormalizedEvents("test-run")
	if err != nil {
		t.Fatalf("failed to query records: %v", err)
	}

	if len(unnormalized) != 1 {
		t.Fatalf("expected 1 record, got %d", len(unnormalized))
	}

	record := unnormalized[0]
	if record.ID != msgId {
		t.Errorf("wrong msg id: %s", record.ID)
	}
	if record.Body != body {
		t.Errorf("wrong body: %s", record.Body)
	}
	if record.ReceiveCount != 3 {
		t.Errorf("wrong receive count: %d", record.ReceiveCount)
	}
	if val, ok := record.Attributes["custom"]; !ok || val != "val" {
		t.Errorf("wrong attributes: %v", record.Attributes)
	}
}

func TestProcessMessage_FallbackTimestamp(t *testing.T) {
	db, err := storage.NewDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create db")
	}
	defer db.Close()

	c := NewSQSCollector(db, "test-queue", "test-run", SQSOptions{})

	msgId := "msg-456"
	body := "{}"
	msg := types.Message{
		MessageId:  &msgId,
		Body:       &body,
		Attributes: map[string]string{
			// No timestamp attribute, should fallback to time.Now()
		},
	}

	err = c.processMessage(msg)
	if err != nil {
		t.Fatalf("processing failed: %v", err)
	}

	unnormalized, _ := db.GetUnnormalizedEvents("test-run")
	record := unnormalized[0]

	if time.Since(record.FirstSeen) > time.Minute {
		t.Errorf("fallback timestamp not close to now: %v", record.FirstSeen)
	}
}
