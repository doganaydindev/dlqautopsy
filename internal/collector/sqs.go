package collector

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/doganaydindev/dlqautopsy/internal/model"
	"github.com/doganaydindev/dlqautopsy/internal/storage"
)

// SQSOptions holds optional configuration for the SQS collector.
type SQSOptions struct {
	Purge  bool   // Delete messages from DLQ after successful ingestion
	Region string // AWS region override (empty = use default)
}

// SQSApi defines the AWS SQS methods used by the collector.
type SQSApi interface {
	ReceiveMessage(ctx context.Context, params *sqs.ReceiveMessageInput, optFns ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error)
	DeleteMessage(ctx context.Context, params *sqs.DeleteMessageInput, optFns ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error)
}

// SQSCollector pulls messages from an SQS DLQ.
type SQSCollector struct {
	db       *storage.DB
	queueURL string
	runName  string
	opts     SQSOptions
	Client   SQSApi // Made public for testing injection
}

// NewSQSCollector creates a new SQSCollector scoped to a run name.
func NewSQSCollector(db *storage.DB, queueURL, runName string, opts SQSOptions) *SQSCollector {
	return &SQSCollector{
		db:       db,
		queueURL: queueURL,
		runName:  runName,
		opts:     opts,
	}
}

// Run starts the ingestion process.
func (c *SQSCollector) Run(ctx context.Context) error {
	fmt.Printf("Polling messages from %s...\n", c.queueURL)

	var client SQSApi
	if c.Client != nil {
		client = c.Client
	} else {
		cfgOpts := []func(*config.LoadOptions) error{}
		if c.opts.Region != "" {
			cfgOpts = append(cfgOpts, config.WithRegion(c.opts.Region))
		}

		cfg, err := config.LoadDefaultConfig(ctx, cfgOpts...)
		if err != nil {
			return fmt.Errorf("failed to load aws config: %w", err)
		}

		client = sqs.NewFromConfig(cfg)
	}
	totalIngested := 0
	var receiptHandles []string

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		out, err := client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
			QueueUrl:              &c.queueURL,
			MaxNumberOfMessages:   10,
			WaitTimeSeconds:       5,
			AttributeNames:        []types.QueueAttributeName{types.QueueAttributeNameAll},
			MessageAttributeNames: []string{".*"},
		})
		if err != nil {
			return fmt.Errorf("failed to receive messages: %w", err)
		}

		if len(out.Messages) == 0 {
			fmt.Println("Queue is empty or wait time exceeded. Stopping ingestion.")
			break
		}

		for _, msg := range out.Messages {
			err := c.processMessage(msg)
			if err != nil {
				fmt.Printf("Error processing message %s: %v\n", *msg.MessageId, err)
			} else {
				totalIngested++
				if c.opts.Purge && msg.ReceiptHandle != nil {
					receiptHandles = append(receiptHandles, *msg.ReceiptHandle)
				}
			}
		}
	}

	// Purge ingested messages if requested
	if c.opts.Purge && len(receiptHandles) > 0 {
		fmt.Printf("Purging %d ingested messages from DLQ...\n", len(receiptHandles))
		deleted := 0
		for _, handle := range receiptHandles {
			h := handle
			_, err := client.DeleteMessage(ctx, &sqs.DeleteMessageInput{
				QueueUrl:      &c.queueURL,
				ReceiptHandle: &h,
			})
			if err != nil {
				fmt.Printf("Warning: failed to delete message: %v\n", err)
			} else {
				deleted++
			}
		}
		fmt.Printf("Purged %d/%d messages.\n", deleted, len(receiptHandles))
	}

	fmt.Printf("Ingestion complete. Total ingested: %d\n", totalIngested)
	return nil
}

func (c *SQSCollector) processMessage(msg types.Message) error {
	rcStr := msg.Attributes[string(types.MessageSystemAttributeNameApproximateReceiveCount)]
	rc, _ := strconv.Atoi(rcStr)

	firstSeenTimestampStr := msg.Attributes[string(types.MessageSystemAttributeNameSentTimestamp)]
	var firstSeen time.Time
	if ts, err := strconv.ParseInt(firstSeenTimestampStr, 10, 64); err == nil {
		firstSeen = time.UnixMilli(ts)
	} else {
		firstSeen = time.Now()
	}

	rawEvent := model.RawEvent{
		ID:                *msg.MessageId,
		Body:              *msg.Body,
		Provider:          "SQS",
		Attributes:        make(map[string]string),
		FirstSeen:         firstSeen,
		LastSeen:          time.Now(),
		ReceiveCount:      rc,
		OriginalQueueName: c.queueURL, // Best effort without parsing arn
	}

	for k, v := range msg.MessageAttributes {
		if v.StringValue != nil {
			rawEvent.Attributes[k] = *v.StringValue
		}
	}

	return c.db.InsertRawEvent(c.runName, rawEvent)
}
