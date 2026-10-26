# Dead Letter Autopsy Engine (dlqautopsy)

Dead-letter queues (DLQs) are a symptom, not a diagnosis. They tell you that a job failed, but they don't explain why, whether it's safe to retry, or what broke downstream.

The Dead Letter Autopsy Engine is a deterministic analyzer for failed async jobs. It ingests messages from your DLQs, analyzes the payloads, error signatures, and timestamps, and provides clear, actionable intelligence on application failures.

## What dlqautopsy Does Differently

- **Deterministic Classification**: No machine learning. Ordered keyword rules assign a failure class, and normalized error signatures group identical errors.
- **Timeline Reconstruction**: Shows when each failure group started and how concentrated it was.
- **Actionable Output**: Gives each group of failures a retry verdict: safe to retry, retry with backoff, do not retry, or needs investigation.
- **Offline First**: All data is ingested into a local SQLite database for rapid, offline analysis. No SaaS, no telemetry, no cloudy dashboards.

## Requirements

- Go 1.26 or newer (see `go.mod`).
- cgo and a C compiler (gcc or clang). The SQLite driver, `github.com/mattn/go-sqlite3`, is a cgo package. `CGO_ENABLED=1` is the default when a C compiler is on your `PATH`; with `CGO_ENABLED=0` the build succeeds, but every command then fails with "go-sqlite3 requires cgo to work".
- AWS credentials and a region that the AWS SDK for Go v2 can find (environment variables, `~/.aws/config`, SSO, etc.), with `sqs:ReceiveMessage` on the DLQ, plus `sqs:DeleteMessage` if you use `--purge`.

## Install

Build from a clone:

```bash
git clone https://github.com/doganaydindev/dlqautopsy.git
cd dlqautopsy
CGO_ENABLED=1 go build -o dlqautopsy ./cmd/dlqautopsy
./dlqautopsy --help
```

Or install the binary into `$(go env GOPATH)/bin`:

```bash
CGO_ENABLED=1 go install github.com/doganaydindev/dlqautopsy/cmd/dlqautopsy@latest
```

Run the tests with `go test ./...`. They need no AWS account; the end-to-end test in `internal/e2e` uses a fake SQS client.

## Usage

```bash
# 1. Ingest messages from a DLQ (saves them locally to autopsy.db under a specific run name)
dlqautopsy inspect --name my-incident --queue-url https://sqs.us-east-1.amazonaws.com/123456789012/my-dlq

# With optional flags:
#   --purge     Delete the ingested messages from the DLQ after ingestion
#   --region    Override the AWS region (e.g. us-west-2)
#   --empty-receives N   Stop after N consecutive empty receives (default 1)
#   --db        SQLite file to use (default: autopsy.db in the current directory; works on every command)
dlqautopsy inspect --name my-incident --queue-url <url> --purge --region us-west-2

# 2. Extract error signatures, normalize payloads, and classify failures deterministically.
#    Prints a classification summary after completion.
dlqautopsy classify --name my-incident

# 3. Generate a report with executive summary, triage groups, and burst analysis
dlqautopsy report --name my-incident
dlqautopsy report --name my-incident --format json
dlqautopsy report --name my-incident --format markdown

# 4. Drill into a specific failure class or error signature to inspect individual messages
dlqautopsy detail --name my-incident
dlqautopsy detail --name my-incident --class "Poison Message"
dlqautopsy detail --name my-incident --class "Downstream Dependency" --limit 5
dlqautopsy detail --name my-incident --signature e3b0c442 --format json

# 5. Compare two runs to detect new, resolved, worsening, or improving failure classes
dlqautopsy diff --name monday-check --compare tuesday-check
dlqautopsy diff --name monday-check --compare tuesday-check --format markdown

# 6. List all stored runs
dlqautopsy runs
```

`inspect` receives up to 10 messages at a time with a 5 second long poll and stops after `--empty-receives` consecutive receives return nothing. The default, 1, stops at the first empty receive. AWS documents that a long-poll receive can, in rare cases, come back empty while the queue still has messages, especially with short wait times, so for a large DLQ or one that is still filling up, `--empty-receives 3` costs at most 10 extra seconds at the end of the run.

`inspect` only reads from the queue (and deletes what it ingested when you pass `--purge`). dlqautopsy never sends messages back to a queue; to move messages from a DLQ back to its source queue, use SQS redrive.

### Local SQS endpoint (no AWS account)

`inspect` uses the standard AWS SDK configuration, so `AWS_ENDPOINT_URL` points it at any SQS-compatible endpoint. This is how the example output below was produced, with [moto](https://github.com/getmoto/moto) running locally:

```bash
pip install 'moto[server]'
moto_server -p 4566 &

export AWS_ENDPOINT_URL=http://127.0.0.1:4566
export AWS_REGION=us-east-1
export AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test   # moto accepts any credentials

go run scripts/seed_sqs.go
dlqautopsy inspect --name incident-0224 --queue-url http://127.0.0.1:4566/123456789012/dlqautopsy-test-dlq --purge
dlqautopsy classify --name incident-0224
dlqautopsy report --name incident-0224
```

## Example Output

All output below comes from the seeded local test (moto, 200 generated messages, see [Testing Environment](#testing-environment)), not from production data. Timestamps are local time on the machine that ran it.

`classify`:

```text
$ dlqautopsy classify --name incident-0224
Normalizing 200 raw events...
Normalization complete. 200 records inserted.
Classifying 200 records...
Classification complete. 200 records updated.

  Classification Complete — 'incident-0224'
  ────────────────────────────────────────────────────────
  [WARN] Downstream Dependency           35 msgs (17.5%)  SAFE TO RETRY
         → Downstream timeout. Safe to retry once dependency recovers.
  [WARN] Downstream Dependency           23 msgs (11.5%)  SAFE TO RETRY
         → Downstream HTTP error. Service temporarily unavailable.
  [CRIT] Auth Failure                    20 msgs (10.0%)  NEEDS INVESTIGATION
         → Authentication or authorization failure. Check credentials and perm...
  [WARN] Network Error                   16 msgs ( 8.0%)  RETRY WITH BACKOFF
         → Network-level connection failure. Connection dropped mid-flight.
  [CRIT] Poison Message                  16 msgs ( 8.0%)  DO NOT RETRY
         → Runtime panic triggered by payload content. Code fix required.
  [WARN] Downstream Dependency           12 msgs ( 6.0%)  SAFE TO RETRY
         → Downstream refused connection. Service may be down.
  [WARN] Transient Error                 12 msgs ( 6.0%)  RETRY WITH BACKOFF
         → Database deadlock or lock contention. Safe to retry with backoff.
  [INFO] Transient Error                 12 msgs ( 6.0%)  RETRY WITH BACKOFF
         → Rate limited by upstream. Retry with exponential backoff.
  [CRIT] Configuration Error              8 msgs ( 4.0%)  DO NOT RETRY
         → DNS resolution failure. Host does not exist or is misconfigured.
  [CRIT] Configuration Error              8 msgs ( 4.0%)  NEEDS INVESTIGATION
         → TLS/certificate error. Check certificate chain and expiry.
  [CRIT] Poison Message                   8 msgs ( 4.0%)  DO NOT RETRY
         → Deserialization failure. Payload is structurally invalid.
  [CRIT] Poison Message                   8 msgs ( 4.0%)  DO NOT RETRY
         → Malformed input. Cannot be parsed by any consumer version.
  [CRIT] Poison Message                   8 msgs ( 4.0%)  DO NOT RETRY
         → Schema validation failure. Payload missing mandatory fields.
  [CRIT] Resource Exhaustion              7 msgs ( 3.5%)  NEEDS INVESTIGATION
         → Disk space exhaustion. Free storage on the consumer host.
  [CRIT] Resource Exhaustion              7 msgs ( 3.5%)  NEEDS INVESTIGATION
         → Memory exhaustion. Consumer ran out of memory processing this message.
  ────────────────────────────────────────────────────────
  Total: 200 messages classified
```

`report` (first lines; the full report continues with triage groups and a per-group breakdown):

```text
$ dlqautopsy report --name incident-0224

  ╔══════════════════════════════════════════════════════╗
  ║          DEAD LETTER AUTOPSY REPORT                 ║
  ╚══════════════════════════════════════════════════════╝
  Run: incident-0224
  Window: 2026-10-26 18:23:48 → 2026-10-26 18:24:03 (14s)

  ── Executive Summary ──────────────────────────────────
  200 messages across 7 failure classes, split into 15 groups by cause (window: 14s). 55% (110 messages) are safe to retry now. 24% (48 messages) should NOT be retried (poison messages or config errors). 21% (42 messages) need manual investigation. Largest class: "Downstream Dependency" with 70 messages (35%).

  ── Dashboard ─────────────────────────────────────────
  Total Messages:    200
  Failure Classes:   7 (15 groups)
  Critical:          4 classes
  Safe to Retry:     110 msgs (55%)
  Do Not Retry:      48 msgs (24%)
```

Rows are per (failure class, cause): one class such as Downstream Dependency can show up as several rows with different causes, which is why the report says "7 failure classes, split into 15 groups by cause".

`diff`, after a second run (`incident-0224-later`) of 50 messages sent directly to the DLQ:

```text
$ dlqautopsy diff --name incident-0224 --compare incident-0224-later

  ╔══════════════════════════════════════════════════════╗
  ║              RUN COMPARISON                         ║
  ╚══════════════════════════════════════════════════════╝
  Base:    incident-0224
  Compare: incident-0224-later

  🆕 NEW FAILURE CLASSES (appeared in incident-0224-later)
  ──────────────────────────────────────────
    + Unknown-1d89be31                 3 msgs [WARNING]

  ✅ RESOLVED (gone from incident-0224-later)
  ──────────────────────────────────────────
    ✓ Downstream Dependency        was 70 msgs
    ✓ Configuration Error          was 16 msgs
    ✓ Network Error                was 16 msgs
    ✓ Resource Exhaustion          was 14 msgs

  📈 WORSENING
  ──────────────────────────────────────────
    ▲ Auth Failure                 20 → 30 (+10, +50%)

  📉 IMPROVING
  ──────────────────────────────────────────
    ▼ Poison Message               40 → 12 (-28, -70%)
    ▼ Transient Error              24 → 5 (-19, -79%)
```

## Testing Environment

To exercise dlqautopsy end to end, a seeding script is provided. It works against a real AWS account or, with `AWS_ENDPOINT_URL` set, against a local SQS endpoint such as moto (see above):

```bash
go run scripts/seed_sqs.go
```

This script will:

1. Create a Dead Letter Queue (`dlqautopsy-test-dlq`).
2. Create a main queue (`dlqautopsy-test-main-queue`) with a redrive policy pointing at the DLQ (`maxReceiveCount=3`, visibility timeout 1 second).
3. Send 200 generated messages with a mix of payloads and error texts (70 downstream dependency, 40 poison, 24 transient, 20 auth, 16 network, 16 configuration, 14 resource exhaustion), shuffled.
4. Receive the messages repeatedly without deleting them, so each one exceeds `maxReceiveCount` and SQS moves it to the DLQ.

On a real account the queues are left in place; delete them afterwards with `aws sqs delete-queue`.

After the script completes, run the autopsy against the DLQ URL it prints:

```bash
./dlqautopsy inspect --name test-run --queue-url <dlq-url>
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
