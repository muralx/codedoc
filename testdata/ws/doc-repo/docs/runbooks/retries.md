# Retry runbook

What to do when deliveries keep failing. Background is in the
[design](../design.md#6-delivery).

## Backoff

Wait one second, then double the wait after every failure.

## Escalation

Page the owner after the third failure.
