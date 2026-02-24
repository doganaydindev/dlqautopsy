# Vision

Dead-letter queues are operationally broken.

Today, engineers deal with DLQs by looking at a long list of JSON strings in a cloud console, clicking the first few, and guessing if the rest of the 10,000 messages failed for the same reason. Monitoring tools and APMs tell you the failure rate spiked, but they don't help you process the backlog of broken state that remains. 

Monitoring is insufficient. It is real-time observability. What you need after a major incident is forensics. You need to perform an autopsy on the dead letters.

## Why This Tool Exists

dlqautopsy exists to turn a pile of dead messages into actionable operational intelligence. 

Instead of treating a DLQ as a dumping ground, dlqautopsy treats it as a dataset. By extracting payloads, error messages, stack traces, and timestamps, and placing them into a local relational model, dlqautopsy allows engineers to query, group, and resolve async failures systematically. 

The goal is to answer:
1. Are these 10,000 messages one problem, or five distinct problems?
2. Which problems are caused by downstream dependencies (and can be retried)?
3. Which messages are structurally malformed and will never succeed (poison pills)?
4. Exactly when did each failure class start and stop failing?

By making the process local, heavily deterministic, and CLI-based, dlqautopsy fits into existing engineering workflows without requiring another SaaS subscription or fragile cloud integration.
