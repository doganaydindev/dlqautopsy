package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

const (
	mainQueueName = "dlqautopsy-test-main-queue"
	dlqName       = "dlqautopsy-test-dlq"
)

// ──────────────────────────────────────────────────────────────────────
// Production-realistic payloads modeled after a typical e-commerce
// platform with: order-service, user-service, payment-service,
// notification-service, inventory-service, and a reporting pipeline.
// ──────────────────────────────────────────────────────────────────────

func poisonMessages() []string {
	uuids := []string{
		"a1b2c3d4-e5f6-7890-abcd-ef1234567890",
		"b2c3d4e5-f6a7-8901-bcde-f12345678901",
		"c3d4e5f6-a7b8-9012-cdef-123456789012",
	}
	return []string{
		// JSON deserialization — someone deployed a schema change and old producers still send the old format
		fmt.Sprintf(`{"service":"order-service","handler":"ProcessOrder","traceId":"%s","error":"json: cannot unmarshal string into Go struct field Order.Amount of type float64","payload":{"orderId":"%s","userId":"usr_8HkQp2","amount":"ninety-nine dollars","currency":"USD","items":[{"sku":"SKU-1234","qty":2}]},"timestamp":"2026-02-24T10:14:32Z"}`, uuids[0], uuids[1]),
		// Validation failure — API gateway let through a request missing a required field
		fmt.Sprintf(`{"service":"user-service","handler":"CreateUser","traceId":"%s","error":"validation failed: required field 'email' is missing in request body","payload":{"userId":"usr_NEW_%d","firstName":"Jane","lastName":"Doe","role":"customer"},"timestamp":"2026-02-24T10:15:01Z"}`, uuids[2], rand.Intn(9999)),
		// Nil pointer dereference — a nullable field was accessed without a nil check
		`{"service":"notification-service","handler":"SendPushNotification","traceId":"d4e5f6a7-b8c9-0123-defa-234567890123","error":"runtime error: invalid memory address or nil pointer dereference\n\tgoroutine 42 [running]:\nmain.(*NotificationHandler).Send(0xc0001a2000, 0x0)\n\t/app/handler/notification.go:87 +0x1a4","payload":{"notificationId":"ntf_99281","userId":"usr_DELETED","channel":"push","template":"order_shipped"},"timestamp":"2026-02-24T10:15:22Z"}`,
		// Index out of range — bulk import with an empty array
		`{"service":"inventory-service","handler":"BulkUpdateStock","traceId":"e5f6a7b8-c9d0-1234-efab-345678901234","error":"runtime error: index out of range [0] with length 0\n\tgoroutine 18 [running]:\nmain.(*StockHandler).BulkUpdate(0xc000198000, {0x0, 0x0, 0x0})\n\t/app/handler/stock.go:134 +0x2b8","payload":{"batchId":"batch_44102","warehouseId":"wh_EU_WEST","items":[],"requestedBy":"admin@example.com"},"timestamp":"2026-02-24T10:16:00Z"}`,
		// Syntax error in a JSON blob forwarded from a partner webhook
		`{"service":"webhook-gateway","handler":"IngestPartnerEvent","traceId":"f6a7b8c9-d0e1-2345-fabc-456789012345","error":"invalid character 'x' looking for beginning of value","payload":"{xml version='1.0'><event type='shipment_update'><id>SHP-88212</id></event>","rawContentType":"text/xml","timestamp":"2026-02-24T10:16:15Z"}`,
	}
}

func downstreamMessages() []string {
	return []string{
		// Postgres connection timeout during a traffic spike
		`{"service":"payment-service","handler":"ChargeCard","traceId":"11111111-2222-3333-4444-555555555555","error":"failed to connect to host=db-primary.internal.example.com user=payment_svc database=payments: connection timeout: context deadline exceeded","payload":{"paymentId":"pay_TXN_90281","orderId":"ord_89012","amount":149.99,"currency":"USD","method":"credit_card","cardLast4":"4242"},"timestamp":"2026-02-24T10:17:00Z"}`,
		// Redis connection refused — probably pod restart during deploy
		`{"service":"session-service","handler":"ValidateSession","traceId":"22222222-3333-4444-5555-666666666666","error":"dial tcp 10.0.14.72:6379: connect: connection refused","payload":{"sessionId":"sess_Kx8mPq","userId":"usr_7712","action":"validate","ipAddress":"203.0.113.42"},"timestamp":"2026-02-24T10:17:05Z"}`,
		// 503 from downstream payment gateway
		`{"service":"payment-service","handler":"CapturePayment","traceId":"33333333-4444-5555-6666-777777777777","error":"HTTP 503 Service Unavailable: POST https://payments.example.net/v1/charges responded with 503: Service temporarily unavailable. Please retry.","payload":{"paymentId":"pay_TXN_90301","orderId":"ord_89024","amount":89.50,"currency":"EUR"},"timestamp":"2026-02-24T10:17:20Z"}`,
		// gRPC deadline exceeded to inventory check
		`{"service":"order-service","handler":"CheckInventory","traceId":"44444444-5555-6666-7777-888888888888","error":"rpc error: code = DeadlineExceeded desc = context deadline exceeded: calling inventory.InventoryService/CheckAvailability","payload":{"orderId":"ord_89031","items":[{"sku":"SKU-7891","qty":1},{"sku":"SKU-2345","qty":3}],"warehouseId":"wh_US_EAST"},"timestamp":"2026-02-24T10:17:30Z"}`,
		// Connection timeout to Elasticsearch
		`{"service":"search-indexer","handler":"IndexProduct","traceId":"55555555-6666-7777-8888-999999999999","error":"connection timeout: failed to connect to es-cluster.internal.example.com:9200 within 5s","payload":{"productId":"prod_112847","action":"index","catalog":"electronics"},"timestamp":"2026-02-24T10:17:45Z"}`,
		// 502 Bad Gateway from internal API
		`{"service":"reporting-pipeline","handler":"FetchDailySales","traceId":"66666666-7777-8888-9999-aaaaaaaaaaaa","error":"HTTP 502 Bad Gateway: GET https://api.internal.example.com/v2/reports/daily-sales responded with: <html><body><h1>502 Bad Gateway</h1></body></html>","payload":{"reportDate":"2026-02-23","region":"us-east-1"},"timestamp":"2026-02-24T10:18:00Z"}`,
	}
}

func authMessages() []string {
	return []string{
		// Expired JWT token
		`{"service":"api-gateway","handler":"AuthMiddleware","traceId":"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee","error":"401 Unauthorized: token expired at 2026-02-24T09:45:00Z, current time 2026-02-24T10:18:30Z","payload":{"endpoint":"/api/v1/orders","method":"POST","userId":"usr_9921","tokenIssuer":"auth.example.com"},"timestamp":"2026-02-24T10:18:30Z"}`,
		// Service-to-service IAM permission denied
		`{"service":"notification-service","handler":"SendEmail","traceId":"bbbbbbbb-cccc-dddd-eeee-ffffffffffff","error":"AccessDeniedException: User: arn:aws:iam::123456789012:role/notification-lambda is not authorized to perform: ses:SendEmail on resource: arn:aws:ses:us-east-1:123456789012:identity/noreply@example.com","payload":{"emailType":"order_confirmation","to":"customer@example.com","orderId":"ord_89055"},"timestamp":"2026-02-24T10:18:45Z"}`,
		// 403 from partner API due to rotated API key
		`{"service":"shipping-service","handler":"CreateLabel","traceId":"cccccccc-dddd-eeee-ffff-111111111111","error":"HTTP 403 Forbidden: POST https://shipping.example.net/v1/shipments responded with: {\"error\":\"invalid_client\",\"error_description\":\"The API key provided has been revoked\"}","payload":{"shipmentId":"shp_EX_77201","carrier":"example-carrier","destination":{"zip":"94105","country":"US"}},"timestamp":"2026-02-24T10:19:00Z"}`,
	}
}

func configMessages() []string {
	return []string{
		// DNS resolution failure — host name in the environment config does not resolve
		`{"service":"email-service","handler":"ConnectSMTP","traceId":"dddddddd-eeee-ffff-1111-222222222222","error":"dial tcp: lookup smtp.mail.example.invalid on 10.0.0.2:53: no such host","payload":{"action":"connect_smtp","server":"smtp.mail.example.invalid","port":587},"timestamp":"2026-02-24T10:19:30Z"}`,
		// TLS certificate error — cert expired yesterday
		`{"service":"payment-service","handler":"Webhook","traceId":"eeeeeeee-ffff-1111-2222-333333333333","error":"x509: certificate has expired or is not yet valid: current time 2026-02-24T10:20:00Z is after 2026-02-23T23:59:59Z","payload":{"webhookUrl":"https://hooks.partner.example.org/v1/events","event":"payment.captured","paymentId":"pay_TXN_90350"},"timestamp":"2026-02-24T10:20:00Z"}`,
	}
}

func networkMessages() []string {
	return []string{
		// Broken pipe during large file upload
		`{"service":"media-service","handler":"UploadAsset","traceId":"ffffffff-1111-2222-3333-444444444444","error":"write tcp 10.0.5.23:43210->10.0.14.88:443: write: broken pipe","payload":{"assetId":"asset_img_99102","size":15728640,"contentType":"image/jpeg","bucket":"example-media-prod"},"timestamp":"2026-02-24T10:20:30Z"}`,
		// Connection reset by peer during streaming response
		`{"service":"analytics-service","handler":"StreamEvents","traceId":"11112222-3333-4444-5555-666677778888","error":"read tcp 10.0.5.23:55120->10.0.20.15:9092: read: connection reset by peer","payload":{"topic":"user-events","partition":3,"offset":18827491,"consumerGroup":"analytics-cg-1"},"timestamp":"2026-02-24T10:20:45Z"}`,
		// Unexpected EOF reading from downstream
		`{"service":"data-pipeline","handler":"IngestCSV","traceId":"22223333-4444-5555-6666-777788889999","error":"unexpected EOF while reading response body from https://sftp-proxy.internal.example.com/exports/daily_orders.csv","payload":{"fileName":"daily_orders.csv","expectedSize":4194304,"bytesRead":2097152},"timestamp":"2026-02-24T10:21:00Z"}`,
	}
}

func resourceMessages() []string {
	return []string{
		// OOM kill — container limit hit
		`{"service":"report-generator","handler":"GeneratePDF","traceId":"33334444-5555-6666-7777-888899990000","error":"runtime: out of memory: cannot allocate 268435456-byte block (1073741824-byte limit)","payload":{"reportId":"rpt_ANNUAL_2025","type":"annual_financial","pages":842,"requestedBy":"cfo@example.com"},"timestamp":"2026-02-24T10:21:30Z"}`,
		// Disk full on the worker node
		`{"service":"log-archiver","handler":"CompressLogs","traceId":"44445555-6666-7777-8888-999900001111","error":"write /var/log/archive/2026-02-23.tar.gz: no space left on device","payload":{"archiveDate":"2026-02-23","sourceDir":"/var/log/app","compressedSize":"12.4GB"},"timestamp":"2026-02-24T10:22:00Z"}`,
	}
}

func transientMessages() []string {
	return []string{
		// Database deadlock during concurrent order processing
		`{"service":"order-service","handler":"FinalizeOrder","traceId":"55556666-7777-8888-9999-aaaa0000bbbb","error":"pq: deadlock detected: process 12847 waits for ShareLock on transaction 98712; blocked by process 12849","payload":{"orderId":"ord_89070","action":"finalize","items":3,"total":299.97},"timestamp":"2026-02-24T10:22:30Z"}`,
		// Rate limited by the payment provider
		`{"service":"payment-service","handler":"RefundPayment","traceId":"66667777-8888-9999-aaaa-bbbb0000cccc","error":"HTTP 429 Too Many Requests: POST https://payments.example.net/v1/refunds: Rate limit exceeded. Retry after 2s.","payload":{"refundId":"ref_R_40211","paymentId":"pay_TXN_88001","amount":45.00,"reason":"customer_request"},"timestamp":"2026-02-24T10:22:45Z"}`,
		// Lock wait timeout in MySQL
		`{"service":"inventory-service","handler":"ReserveStock","traceId":"77778888-9999-aaaa-bbbb-cccc0000dddd","error":"Error 1205 (HY000): Lock wait timeout exceeded; try restarting transaction","payload":{"sku":"SKU-5678","warehouseId":"wh_US_WEST","quantity":10,"reservationId":"rsv_881204"},"timestamp":"2026-02-24T10:23:00Z"}`,
		// Throttled by DynamoDB
		`{"service":"session-service","handler":"PersistSession","traceId":"88889999-aaaa-bbbb-cccc-dddd0000eeee","error":"ProvisionedThroughputExceededException: Rate exceeded for table 'Sessions'. Throttled: rate limit exceeded","payload":{"sessionId":"sess_Mk9vRz","operation":"PutItem","table":"Sessions"},"timestamp":"2026-02-24T10:23:15Z"}`,
	}
}

// generateMessages produces a realistic mix of ~200 messages with the
// distribution you'd expect during a real incident in a microservice
// architecture. Downstream dependency failures dominate because the
// typical real-world scenario is "a database went down for 8 minutes".
func generateMessages() []string {
	poison := poisonMessages()
	downstream := downstreamMessages()
	auth := authMessages()
	cfg := configMessages()
	network := networkMessages()
	resource := resourceMessages()
	transient := transientMessages()

	var msgs []string

	// ~35% downstream dependency (the big incident — a postgres outage burst)
	for i := 0; i < 70; i++ {
		msgs = append(msgs, downstream[i%len(downstream)])
	}
	// ~20% poison messages (ongoing bad schema from old producer)
	for i := 0; i < 40; i++ {
		msgs = append(msgs, poison[i%len(poison)])
	}
	// ~12% transient errors (deadlocks/rate limits during the spike)
	for i := 0; i < 24; i++ {
		msgs = append(msgs, transient[i%len(transient)])
	}
	// ~10% auth failures (a rotated key that hasn't propagated)
	for i := 0; i < 20; i++ {
		msgs = append(msgs, auth[i%len(auth)])
	}
	// ~8% network errors
	for i := 0; i < 16; i++ {
		msgs = append(msgs, network[i%len(network)])
	}
	// ~8% config errors
	for i := 0; i < 16; i++ {
		msgs = append(msgs, cfg[i%len(cfg)])
	}
	// ~7% resource exhaustion
	for i := 0; i < 14; i++ {
		msgs = append(msgs, resource[i%len(resource)])
	}

	// Shuffle for realism
	rand.Shuffle(len(msgs), func(i, j int) {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	})
	return msgs
}

func main() {
	ctx := context.TODO()

	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatalf("unable to load SDK config: %v\n\nPlease ensure your AWS credentials are configured via:\n  - Environment variables (AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY)\n  - ~/.aws/credentials\n  - Or use LocalStack with AWS_ENDPOINT_URL", err)
	}

	client := sqs.NewFromConfig(cfg)

	fmt.Println("╔══════════════════════════════════════════════╗")
	fmt.Println("║       dlqautopsy Test Environment Seeder           ║")
	fmt.Println("╚══════════════════════════════════════════════╝")
	fmt.Println()

	// 1. Create DLQ
	fmt.Println("① Creating Dead Letter Queue...")
	dlqOut, err := client.CreateQueue(ctx, &sqs.CreateQueueInput{
		QueueName: aws.String(dlqName),
	})
	if err != nil {
		log.Fatalf("   ✗ Failed to create DLQ: %v", err)
	}
	dlqURL := *dlqOut.QueueUrl
	fmt.Printf("   ✓ DLQ URL: %s\n\n", dlqURL)

	// Get DLQ ARN
	dlqAttrs, err := client.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl:       aws.String(dlqURL),
		AttributeNames: []types.QueueAttributeName{types.QueueAttributeNameQueueArn},
	})
	if err != nil {
		log.Fatalf("   ✗ Failed to get DLQ attributes: %v", err)
	}
	dlqArn := dlqAttrs.Attributes[string(types.QueueAttributeNameQueueArn)]

	// 2. Create main queue
	fmt.Println("② Creating Main Queue with Redrive Policy (maxReceiveCount=3)...")
	redrivePolicy := fmt.Sprintf(`{"deadLetterTargetArn":"%s","maxReceiveCount":"3"}`, dlqArn)

	mainOut, err := client.CreateQueue(ctx, &sqs.CreateQueueInput{
		QueueName: aws.String(mainQueueName),
		Attributes: map[string]string{
			"RedrivePolicy":     redrivePolicy,
			"VisibilityTimeout": "1",
		},
	})
	if err != nil {
		log.Fatalf("   ✗ Failed to create main queue: %v", err)
	}
	mainURL := *mainOut.QueueUrl
	fmt.Printf("   ✓ Main Queue URL: %s\n\n", mainURL)

	// 3. Generate & send messages
	messages := generateMessages()
	fmt.Printf("③ Sending %d realistic test messages in batches of 10...\n", len(messages))

	for i := 0; i < len(messages); i += 10 {
		end := i + 10
		if end > len(messages) {
			end = len(messages)
		}

		var entries []types.SendMessageBatchRequestEntry
		for j, body := range messages[i:end] {
			entries = append(entries, types.SendMessageBatchRequestEntry{
				Id:          aws.String(fmt.Sprintf("msg_%d", i+j)),
				MessageBody: aws.String(body),
			})
		}

		_, err := client.SendMessageBatch(ctx, &sqs.SendMessageBatchInput{
			QueueUrl: aws.String(mainURL),
			Entries:  entries,
		})
		if err != nil {
			log.Printf("   ⚠ Batch %d failed: %v", i/10, err)
		}
	}
	fmt.Printf("   ✓ Sent %d messages to %s\n\n", len(messages), mainQueueName)

	// 4. Exhaust retries so messages land in DLQ
	fmt.Println("④ Simulating consumer failures to trigger dead-lettering...")
	fmt.Println("   (Receiving messages without deleting → they exhaust maxReceiveCount)")

	for attempt := 1; attempt <= 4; attempt++ {
		fmt.Printf("   Pass %d/4: ", attempt)
		totalPulled := 0
		for pull := 0; pull < 25; pull++ {
			out, _ := client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
				QueueUrl:            aws.String(mainURL),
				MaxNumberOfMessages: 10,
				WaitTimeSeconds:     1,
			})
			totalPulled += len(out.Messages)
			if len(out.Messages) == 0 {
				break
			}
		}
		fmt.Printf("received %d messages\n", totalPulled)
		time.Sleep(2 * time.Second)
	}

	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════════════════════╗")
	fmt.Println("║  Test environment ready!                                ║")
	fmt.Println("║                                                         ║")
	fmt.Println("║  Run the autopsy:                                       ║")
	fmt.Printf("║  $ dlqautopsy inspect --name prod-incident \\\n")
	fmt.Printf("║      --queue-url %s\n", dlqURL)
	fmt.Println("║  $ dlqautopsy classify --name prod-incident             ║")
	fmt.Println("║  $ dlqautopsy report --name prod-incident               ║")
	fmt.Println("║  $ dlqautopsy detail --name prod-incident \\             ║")
	fmt.Println("║      --class \"Poison Message\"                           ║")
	fmt.Println("║                                                         ║")
	fmt.Println("║  Cleanup:                                               ║")
	fmt.Printf("║  $ aws sqs delete-queue --queue-url %s\n", mainURL)
	fmt.Printf("║  $ aws sqs delete-queue --queue-url %s\n", dlqURL)
	fmt.Println("╚══════════════════════════════════════════════════════════╝")
}
