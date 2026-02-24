# Contributing to dlqautopsy

The Dead Letter Autopsy Engine accepts high-quality, focused contributions.

## Code Style Rules

- **Use idiomatic Go**: `gofmt` and `go vet` are mandatory.
- **No overengineering**: If you find yourself writing an interface for a single implementation, rewrite it as a concrete type.
- **Clear package boundaries**: Stick strictly to the unidirectional `collector -> normalizer -> classifier -> analyzer -> reporter` flow. Do not cross these boundaries.
- **Favor correctness over performance**: dlqautopsy operates on bounded local data. We care entirely about deterministic results, not micro-optimizations. 
- **No buzzwords**: Keep the code and comments strictly professional. 

## Adding a New Queue Backend

The Collector layer (e.g., `internal/collector/sqs.go`) provides the data ingress logic. 

To add a new backend (e.g., RabbitMQ):
1. Implement a new Go struct in `internal/collector/rabbitmq.go`.
2. Map the vendor-specific message properties to the normalized `RawEvent` schema defined in `schemas/events.md`.
3. Read the message payload and vendor attributes.
4. Insert the unified representation directly into local storage.

## Adding a New Failure Classifier

The Classifier module assigns Failure Classes based on the Normalizer's `ErrorSignature`.

To add rule-based classification heuristics:
1. Open `internal/classifier/classifier.go`.
2. Add deterministic matching logic against the normalized error string.
3. Validate your logic by ensuring it correctly categorizes known failure patterns (e.g., "Unmarshal Error" = Poison Message).
4. Update tests to explicitly prove your edge case categorizations.
