# Architecture

dlqautopsy is designed around a strictly unidirectional pipeline. It extracts data from the remote queue, persists it locally, and runs all subsequent analysis over the local dataset.

## Component Boundaries

1. **Collector**: Reaches out to the target DLQ (e.g., AWS SQS), pulls down messages, their attributes, and metadata (timestamps, receive counts), and writes them to local storage. Supports `--purge` to delete messages from the DLQ after successful ingestion, `--region` to override the AWS region, and `--empty-receives N` to stop only after N consecutive receives return no messages (default 1; each receive long-polls for up to 5 seconds).
2. **Storage**: The unified persistence layer.
3. **Normalizer**: Parses raw message bodies as JSON to intelligently extract error-specific fields (e.g., `error`, `errorMessage`, `cause`) rather than treating the entire payload as the error. Computes a payload shape fingerprint from the JSON key structure for shape-based grouping. Strips volatile data (memory addresses, UUIDs, timestamps, IPs) from the extracted error text and hashes the remainder to create a stable error signature.
4. **Classifier**: Matches the normalized error text against an ordered list of keyword rules (first match wins) and assigns a failure class. It does not use payload shapes. Messages that match no rule get an `Unknown-<signature prefix>` class, so identical unknown errors still group together. Assigns severity levels (critical/warning/info) and retry verdicts (safe_to_retry/do_not_retry/retry_with_backoff/needs_investigation).
5. **Analyzer**: Builds the temporal view. Maps the failure classes to the execution timeline to determine cascade effects, first/last seen metrics, retry exhaustion patterns, failure window duration, the busiest hour (by SentTimestamp) and the time spread. Produces an executive summary with triage groups (Immediate Action / Safe to Retry / Needs Investigation) and per-class percentage breakdowns.
6. **Reporter**: Translates the internal analytical model into structured outputs (JSON, Markdown, or human-readable CLI tables). Produces a top-level `ReportSummary` with executive summary, aggregate stats, triage groupings, and per-class breakdowns. Also supports `detail` output for individual message inspection and `diff` output for comparing two runs.

```
   ┌─────────┐      ┌────────────┐      ┌─────────────┐
   │         │      │            │      │             │
   │ SQS DLQ ├─────►│ Collector  ├─────►│   Storage   │
   │         │      │            │      │  (SQLite)   │
   └─────────┘      └────────────┘      └──────┬──────┘
                                               │
   ┌─────────┐      ┌────────────┐             │
   │         │      │            │◄────────────┘
   │ Output  │◄─────┤  Reporter  │
   │         │      │            │
   └─────────┘      └──────▲─────┘
                           │
                    ┌──────┴─────┐
                    │            │
                    │  Analyzer  │
                    │            │
                    └──────▲─────┘
                           │
                    ┌──────┴─────┐
                    │            │
                    │ Classifier │
                    │            │
                    └──────▲─────┘
                           │
                    ┌──────┴─────┐
                    │            │
                    │ Normalizer │
                    │            │
                    └────────────┘
```

## Why SQLite?

A core design principle of dlqautopsy is that analyzing DLQs should not require standing up infrastructure. SQLite provides a robust relational engine that allows the Analyzer and Classifier modules to use SQL for grouping and filtering, drastically reducing the complexity of the Go code. It is fast, lightweight, and requires zero configuration.

### Run Isolation (Multi-Tenancy)

All data ingested into the SQLite database is strictly partitioned by a user-provided `--name` flag (e.g., `incident-jan-4`, `daily-check`). This allows the engineer to maintain a historical database of multiple independent debug sessions inside `autopsy.db`, preventing data mixing and enabling longitudinal forensics via the `dlqautopsy runs` command.

## Why CLI-First?

Engineers live in the terminal. When triaging an incident, forcing an engineer to log into a web dashboard, configure SSO, and click through a slow frontend is hostile UX. A CLI-first tool can be piped into `jq`, automated in bash scripts, and run immediately from any developer machine with standard AWS credentials.
