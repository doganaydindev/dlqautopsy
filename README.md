# Dead Letter Autopsy Engine (dlqautopsy)

Dead-letter queues (DLQs) are a symptom, not a diagnosis. They tell you that a job failed, but they don't explain why, whether it's safe to retry, or what broke downstream.

The Dead Letter Autopsy Engine is a deterministic analyzer for failed async jobs. It ingests messages from your DLQs, analyzes the payloads, error signatures, and timestamps, and provides clear, actionable intelligence on application failures.

## What dlqautopsy Does Differently

- **Deterministic Classification**: No machine learning. It maps failures to exact error signatures to group identical issues.
- **Timeline Reconstruction**: Analyzes when messages failed to expose cascading outages or bad deployments.
- **Actionable Output**: Tells you whether a message is safe to retry, a poison pill, or blocked by a downstream dependency.
- **Offline First**: All data is ingested into a local SQLite database for rapid, offline analysis. No SaaS, no telemetry, no cloudy dashboards.

## Example Usage

```bash
# 1. Ingest messages from a DLQ (saves them locally to autopsy.db under a specific run name)
dlqautopsy inspect --name my-incident --queue-url https://sqs.us-east-1.amazonaws.com/123456789/my-dlq

# With optional flags:
#   --purge     Delete messages from the DLQ after successful ingestion
#   --region    Override the AWS region (e.g. us-west-2)
dlqautopsy inspect --name my-incident --queue-url <url> --purge --region us-west-2

# 2. Extract error signatures, normalize payloads, and classify failures deterministically
#    Prints a classification summary after completion.
dlqautopsy classify --name my-incident

# 3. Generate actionable report with executive summary, triage groups, and burst analysis
dlqautopsy report --name my-incident
dlqautopsy report --name my-incident --format json
dlqautopsy report --name my-incident --format markdown

# 4. Drill into a specific failure class to inspect individual messages
dlqautopsy detail --name my-incident --class "Poison Message"
dlqautopsy detail --name my-incident --class "Downstream Dependency" --limit 5
dlqautopsy detail --name my-incident --signature e3b0c442 --format json

# 5. Compare two runs to detect new, resolved, or worsening failure classes
dlqautopsy diff --name monday-check --compare tuesday-check
dlqautopsy diff --name monday-check --compare tuesday-check --format markdown

# 6. List all historical inspection runs
dlqautopsy runs
```

### Example JSON Output

```json
{
  "run_name": "my-incident",
  "total_messages": 1432,
  "total_classes": 2,
  "critical_count": 1,
  "safe_to_retry_count": 1,
  "safe_to_retry_messages": 1250,
  "do_not_retry_count": 1,
  "do_not_retry_messages": 182,
  "window_start": "2025-01-15T08:14:00Z",
  "window_end": "2025-01-15T11:00:21Z",
  "window_duration": "2h 46m 21s",
  "executive_summary": "1432 messages across 2 failure classes (window: 2h 46m 21s). 87% (1250 messages) are safe to retry now. 13% (182 messages) should NOT be retried (poison messages or config errors). Largest class: \"Downstream Dependency\" with 1250 messages (87%).",
  "triage": [
    {
      "label": "Immediate Action Required",
      "classes": ["...(Poison Message details)..."],
      "total_messages": 182
    },
    {
      "label": "Safe to Retry",
      "classes": ["...(Downstream Dependency details)..."],
      "total_messages": 1250
    }
  ],
  "classes": [
    {
      "failure_class": "Downstream Dependency",
      "severity": "warning",
      "retry_verdict": "safe_to_retry",
      "count": 1250,
      "percentage": 87.3,
      "unique_signatures": 1,
      "first_occurrence": "2025-01-15T10:02:14Z",
      "last_occurrence": "2025-01-15T10:14:05Z",
      "duration": "11m 51s",
      "retry_pattern": "Exhaustion",
      "avg_receive_count": 3.2,
      "max_receive_count": 5,
      "peak_hourly_spike": 1250,
      "recommended_action": "Downstream timeout. Safe to retry once dependency recovers.",
      "sample_payload": "{...}",
      "sample_error": "failed to connect to postgres: connection timeout during startup",
      "payload_shapes": ["error,payload.action,payload.user_id"],
      "burst_analysis": "Short window: 1250 failures over 11m 51s. Consistent with a dependency outage or rolling deploy."
    },
    {
      "failure_class": "Poison Message",
      "severity": "critical",
      "retry_verdict": "do_not_retry",
      "count": 182,
      "percentage": 12.7,
      "unique_signatures": 3,
      "first_occurrence": "2025-01-15T08:14:00Z",
      "last_occurrence": "2025-01-15T11:00:21Z",
      "duration": "2h 46m 21s",
      "retry_pattern": "Instant Death",
      "avg_receive_count": 1.0,
      "max_receive_count": 1,
      "peak_hourly_spike": 82,
      "recommended_action": "Deserialization failure. Payload is structurally invalid.",
      "sample_payload": "{...}",
      "sample_error": "json: cannot unmarshal string into Go struct field User.Age of type int",
      "payload_shapes": ["age,name,type"]
    }
  ]
}
```

## Testing Environment

To test dlqautopsy against real infrastructure, a seeding script is provided:

```bash
# Requires AWS credentials configured in your environment
go run scripts/seed_sqs.go
```

This script will:

1. Create a Dead Letter Queue (`dlqautopsy-test-dlq`)
2. Create a main queue (`dlqautopsy-test-main-queue`) with a redrive policy attached to the DLQ.
3. Send 8 mock messages containing different simulated payload and error patterns.
4. Auto-receive the messages repeatedly until they exhaust their 3-retry limit and are shifted to the DLQ.

After the script completes, grab the DLQ URL and run the autopsy:

```bash
./dlqautopsy inspect --name test-run --queue-url <url>
./dlqautopsy classify --name test-run
./dlqautopsy report --name test-run
```

## Non-goals

If you are looking for any of the following, this tool is not for you:

- A Web UI or SaaS dashboard
- A message redrive tool (use your cloud provider's API for that)
- Active monitoring or alerting
- AI-based failure guessing

Read more in [docs/non-goals.md](docs/non-goals.md).
