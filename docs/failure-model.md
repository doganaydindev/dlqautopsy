# Failure Model

The engine operates on a strict, pre-defined model of failure. This ensures classifications are deterministic and actionable.

## Failure Classes

Every processed message is assigned exactly one Failure Class. A failure class represents a root cause boundary. If two messages share the same Failure Class, fixing the underlying issue for one guarantees the fix applies to the other.

dlqautopsy recognizes the following deterministic classes:

| Class                     | Severity       | Retry Verdict       | Description                                                                                                               |
| ------------------------- | -------------- | ------------------- | ------------------------------------------------------------------------------------------------------------------------- |
| **Poison Message**        | critical       | do_not_retry        | Payload is structurally invalid. Deserialization, validation, or schema errors. Will never succeed regardless of retries. |
| **Downstream Dependency** | warning        | safe_to_retry       | External service or database is unavailable. Timeouts, connection refused, 502/503/504 errors.                            |
| **Auth Failure**          | critical       | needs_investigation | Authentication or authorization errors (401, 403, token expired). Requires credential or permission fix.                  |
| **Configuration Error**   | critical       | do_not_retry        | DNS resolution failures, TLS/certificate errors. Infrastructure misconfiguration.                                         |
| **Network Error**         | warning        | retry_with_backoff  | Connection dropped mid-flight (broken pipe, connection reset, EOF).                                                       |
| **Resource Exhaustion**   | critical       | needs_investigation | OOM, disk full, file descriptor exhaustion on the consumer.                                                               |
| **Transient Error**       | warning / info | retry_with_backoff  | Deadlocks, rate limiting, throttling. Temporary contention.                                                               |
| **Unknown**               | warning        | needs_investigation | No matching rule. Appended with the first 8 chars of the error signature for grouping.                                    |

## Error Signature Hashing

Because raw stack traces contain memory pointers, timestamps, and randomized IDs, identical logical errors often look different as plaintext.

The **Normalizer** applies a two-phase extraction and sanitization approach:

### Phase 1: Smart Error Extraction

If the message body is JSON, the Normalizer parses it and searches for a known error field in priority order:

`error`, `errorMessage`, `error_message`, `Error`, `ErrorMessage`, `err`, `message`, `msg`, `cause`, `reason`, `detail`, `stackTrace`, `stack_trace`, `stacktrace`, `exception`, `Exception`

The first non-empty match is used as the error text. If the body is not JSON, or no error field is found, the entire body is used as the error text.

### Phase 2: Normalization

The extracted error text is sanitized:

- Removes memory addresses (`0x[0-9a-f]+`)
- Replaces UUIDs with `[UUID]`
- Replaces timestamps with `[TIMESTAMP]`
- Replaces IP addresses with `[IP]`
- Normalizes Go file:line references (e.g., `handler.go:123` → `handler.go:[LINE]`)

The resulting string is SHA-256 hashed to produce the `ErrorSignature`. Messages with identical ErrorSignatures belong to the same Failure Class.

> **Key design choice**: The normalizer extracts the error field specifically (not the full payload), so classification keywords like "timeout", "unmarshal", "connection refused" remain intact in the normalized error. Previous approaches that masked all JSON values destroyed the very text the classifier depends on.

## Payload Shape Analysis

When the message body is JSON, the Normalizer also computes a **payload shape fingerprint** — a sorted, comma-separated list of all key paths in the JSON structure.

Example: `{"error": "timeout", "payload": {"action": "process", "id": 123}}` produces the shape: `error,payload.action,payload.id`

The payload shape enables grouping messages that are structurally identical even when their error signatures differ. The Analyzer includes per-class payload shape breakdowns in the report to help identify whether a failure class affects a single message schema or multiple.

## Burst Analysis

The Analyzer performs burst detection on each failure class based on the temporal window:

| Window Duration     | Assessment                                                                            |
| ------------------- | ------------------------------------------------------------------------------------- |
| < 2 minutes         | **Concentrated burst**: Likely a single event (deploy, config push, dependency crash) |
| 2–15 minutes        | **Short window**: Consistent with a dependency outage or rolling deploy               |
| 15–60 minutes       | **Medium window**: Extended incident                                                  |
| > 1 hour, high rate | **Sustained high rate**: Reports failures/hour metric                                 |

Burst analysis is displayed in both text and JSON reports to help engineers correlate DLQ spikes with external events.

## Severity Levels

Each failure class carries a severity that indicates operational urgency:

- **critical**: Requires immediate human intervention. These failures will not self-heal.
- **warning**: May self-heal if the underlying condition resolves. Worth monitoring.
- **info**: Expected operational friction (e.g., rate limiting). Low urgency.

## Retry Verdicts

Each classified message receives a deterministic retry verdict:

- **safe_to_retry**: The failure is caused by an external, likely transient condition. Retrying after the dependency recovers should succeed.
- **do_not_retry**: The message or configuration is fundamentally broken. Retrying wastes compute and pollutes logs.
- **retry_with_backoff**: The failure is transient but sensitive to load. Exponential backoff is recommended.
- **needs_investigation**: The engine cannot determine safety automatically. A human must inspect the root cause.

## Retry Patterns

The number of times a message has been received by a consumer (e.g., SQS `ApproximateReceiveCount`) informs the retry pattern classification:

- **Instant Death**: 80%+ of messages in the class failed on the first attempt (receive_count <= 1). Indicates a catastrophic configuration failure or poison payload.
- **Exhaustion**: 80%+ of messages retried multiple times before landing in the DLQ. Indicates a persistent downstream outage.
- **Mixed**: Both patterns present in significant proportion. Suggests a failure class that covers multiple distinct issues, or an intermittent fault.
- **Mostly Instant Death / Mostly Exhaustion**: One pattern dominates but the other is present.

## Poison Message Definition

A **Poison Message** is a message that fails due to validation, deserialization, or structural logic errors -- not due to an external dependency or transient condition.

Indicators of a Poison Message:

- Error signatures containing terms: `unmarshal`, `validation`, `required field`, `syntax error`, `nil pointer`, `index out of range`
- Fails repeatedly, synchronously, producing the exact same error on every attempt
- Retry pattern is typically "Instant Death"

Recommendation: Never retry poison messages. They will continuously fail and consume queue polling resources. Code modifications or manual payload overrides are required.
