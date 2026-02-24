# Non-Goals

The Dead Letter Autopsy Engine has a laser-focused objective and explicitly rejects any feature that expands its scope beyond that objective. 

If you are looking for any of the following, this tool is not for you.

## SaaS Hosting

There will never be a hosted version of dlqautopsy. 

Shipping DLQ payloads to a 3rd party SaaS vendor requires a massive security audit, PII redaction, and compliance overhead. Instead, dlqautopsy runs locally. You download the binary, you point it at your cloud provider using your existing local credentials, and your data never leaves your machine. 

## Active Monitoring Replacement

dlqautopsy does not replace DataDog, NewRelic, or Prometheus. 

Monitoring tools excel at telling you when the dead letter rate spikes. dlqautopsy excels at telling you why the rate spiked and what to do with the backlog. You use monitoring during the incident, and dlqautopsy during the post-mortem.

## Message Redrive UI

dlqautopsy is a forensic analysis tool, not an administrative dashboard.

While v0.3 introduces replay hooks for automated action on known failure classes, dlqautopsy does not present a UI where developers can manually edit JSON payloads and click “Resubmit”. 

## AI-Based Guessing

dlqautopsy is a deterministic engine. 

When you are dealing with a critical outage or 100,000 lost messages, you need certainty. You cannot rely on an LLM to guess the cause of a failure inconsistently across different runs.

dlqautopsy groups issues based on strict algorithmic hashing. Identical errors produce identical signatures. This ensures a stable and trustworthy foundation for analysis.
