# Roadmap

dlqautopsy is built iteratively, focusing first on core functionality before expanding the integration surface.

## v0.1 (Current MVP)
- **Target Backend**: AWS SQS (DLQs)
- **Storage**: Local SQLite
- **Core Engine**: Deterministic normalizer, classifier, and analyzer
- **Interface**: CLI with `inspect`, `classify`, and `report` commands
- **Format**: Text tables and JSON output

## v0.2
- **New Target Backend**: RabbitMQ dead-letter exchanges
- **New Target Backend**: Kafka dead-letter topics
- **Enhancement**: Better heuristics for identifying Poison Messages

## v0.3
- **Action Execution**: Replay hooks. Allow dlqautopsy to automatically push safe-to-retry messages back to the primary queue.
- **Reporting**: Diffing two reports to show delta over time.

## Long-term Strategy

- **No UI planned**: dlqautopsy is and always will be a CLI-first integration tool. A web UI or graphical dashboard is explicitly out of scope.
