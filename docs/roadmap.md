# Roadmap

dlqautopsy is built iteratively, focusing first on core functionality before expanding the integration surface.

## Implemented today
- **Target Backend**: AWS SQS (DLQs), including any SQS-compatible endpoint set with `AWS_ENDPOINT_URL`
- **Storage**: Local SQLite, with named runs
- **Core Engine**: Deterministic normalizer, classifier, and analyzer
- **Interface**: CLI with `inspect`, `classify`, `report`, `detail`, `diff`, and `runs`
- **Format**: Text, JSON, and Markdown output
- **Reporting**: Diffing two runs to show new, resolved, worsening and improving failure classes

## Planned (not implemented yet)
- **New Target Backend**: RabbitMQ dead-letter exchanges
- **New Target Backend**: Kafka dead-letter topics
- **Enhancement**: Better heuristics for identifying Poison Messages
- **Action Execution**: Replay hooks. Allow dlqautopsy to push safe-to-retry messages back to the primary queue. Today dlqautopsy never sends messages anywhere.

## Long-term Strategy

- **No UI planned**: dlqautopsy is and always will be a CLI-first integration tool. A web UI or graphical dashboard is explicitly out of scope.
