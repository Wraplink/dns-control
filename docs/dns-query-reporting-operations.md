# DNS query reporting: operations and rollout

DNS query reporting is opt-in. Enable it only after the Backend ingestion endpoint and its database migration are deployed.

## Prerequisites

- Backend supports `POST /api/v1/internal/network/dns-queries/batch`.
- DNS-Control has its Backend URL, control key, and controller/node ID configured.
- The controller is registered with Backend and its credentials are valid.
- The Backend ingestion service is reachable from DNS-Control.

Set `WRAPLINK_DNS_QUERY_REPORT_ENABLED=true` for the DNS-Control process and restart it. Do not enable the reporter before Backend is upgraded.

## Monitoring

The controller emits a periodic aggregate log line:

```text
dns query reporting stats delivered=... failed_batches=... dropped=... pending=...
```

- `delivered`: events accepted by Backend.
- `failed_batches`: failed HTTP delivery attempts, including retries.
- `dropped`: events discarded because the queue is full, Backend permanently rejected a batch, or the process shut down with unsent events.
- `pending`: events queued or waiting for HTTP delivery.

These counters are process-local and reset after restart. Logs contain no DNS query names or client IP addresses.

## Failure handling

- HTTP 408, 429, 5xx and transport errors trigger bounded exponential retry with stable event IDs.
- Other HTTP 4xx responses permanently reject the batch. Investigate the Backend key, registration, and schema immediately.
- The in-memory queue is bounded to protect DNS latency. During an extended Backend outage, new events may be dropped.
- The reporter is **not durable**: a crash or restart can lose queued and in-flight events. Shutdown accounting logs losses during orderly cancellation, but it cannot prevent them.

## Deployment checks

1. Deploy Backend ingestion and migration first.
2. Deploy DNS-Control with reporting disabled; verify ordinary DNS responses.
3. Enable reporting on one controller and issue test DNS queries.
4. Confirm Backend query history shows the controller, queried domain, response code, and effective resolution mode.
5. Confirm `delivered` increases and `pending` returns toward zero; investigate any growth in `dropped`.
6. Roll out to remaining controllers after verifying the first controller.

## Planned durability

A future opt-in disk spool should preserve event IDs across restart, bound disk usage, recover unacknowledged batches, and only remove records after Backend acknowledgment. It must keep DNS resolution independent of disk and Backend latency. This capability is not implemented in the current reporter.
