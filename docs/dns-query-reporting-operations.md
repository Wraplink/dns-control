# DNS query reporting: operations and rollout

DNS query reporting is opt-in. Enable it only after the Backend ingestion endpoint and its database migration are deployed.

## Prerequisites

- Backend supports `POST /api/v1/internal/network/dns-queries/batch`.
- DNS-Control has its Backend URL, control key, and controller/node ID configured.
- The controller is registered with Backend and its credentials are valid.
- The Backend ingestion service is reachable from DNS-Control.

Set `WRAPLINK_DNS_QUERY_REPORT_ENABLED=true` for the DNS-Control process and restart it. Do not enable the reporter before Backend is upgraded. For durable persisted batches, configure `WRAPLINK_DNS_QUERY_SPOOL_DIR` to a writable directory backed by a persistent volume. Optional `WRAPLINK_DNS_QUERY_SPOOL_MAX_BYTES` defaults to `67108864` (64 MiB) and must be a positive decimal integer. If no spool directory is configured, reporting remains memory-only.

## Monitoring

The controller emits a periodic aggregate log line:

```text
dns query reporting stats delivered=... failed_batches=... dropped=... pending=...
```

- `delivered`: events accepted by Backend.
- `failed_batches`: failed HTTP delivery attempts, including retries.
- `dropped`: events discarded because the queue is full, Backend permanently rejected a batch, or the process shut down with **unpersisted** events. Persisted batches awaiting replay are not counted as dropped.
- `pending`: events queued or waiting for HTTP delivery.

These counters are process-local and reset after restart. During orderly shutdown, the worker logs `persisted_for_replay` and `discarded_unpersisted` separately. Logs contain no DNS query names or client IP addresses.

## Failure handling

- HTTP 408, 429, 5xx and transport errors trigger bounded exponential retry with stable event IDs.
- Other HTTP 4xx responses permanently reject the batch. Investigate the Backend key, registration, and schema immediately.
- The in-memory queue is bounded to protect DNS latency. During an extended Backend outage, new events may be dropped.
- With a disk spool configured, **successfully persisted** batches survive restart and replay using stable event IDs; Backend deduplicates them. Events waiting in the bounded in-memory channel may still be lost on a crash. Without a spool, reporting is entirely memory-only.\n- A full or unwritable spool prevents new batches from being persisted. The queue remains bounded, so prolonged outages can still cause dropped events. Watch disk space and the `failed_batches` and `dropped` counters.\n- Corrupt spool files halt ordered recovery and require operator investigation; do not delete them without preserving a copy.\n- Backend ingestion has an event-age limit; very old persisted batches can be permanently rejected. Monitor extended outages and recovery promptly.

## Deployment checks

1. Deploy Backend ingestion and migration first.
2. Deploy DNS-Control with reporting disabled; verify ordinary DNS responses.
3. Enable reporting on one controller and issue test DNS queries.
4. Confirm Backend query history shows the controller, queried domain, response code, and effective resolution mode.
5. Confirm `delivered` increases and `pending` returns toward zero; investigate any growth in `dropped`.
6. Roll out to remaining controllers after verifying the first controller.

## Planned durability

A future opt-in disk spool should preserve event IDs across restart, bound disk usage, recover unacknowledged batches, and only remove records after Backend acknowledgment. It must keep DNS resolution independent of disk and Backend latency. This capability is not implemented in the current reporter.

## Persistent spool deployment and recovery

The platform Compose configuration in [Platform PR #38](https://github.com/Wraplink/platform/pull/38) mounts the `dnsqueryspool` named volume at `/var/lib/wraplink/dns-query-spool` and sets:

```dotenv
WRAPLINK_DNS_QUERY_REPORT_ENABLED=false
WRAPLINK_DNS_QUERY_SPOOL_DIR=/var/lib/wraplink/dns-query-spool
WRAPLINK_DNS_QUERY_SPOOL_MAX_BYTES=67108864
```

After deploying Backend ingestion and the DNS-Control reporter, set `WRAPLINK_DNS_QUERY_REPORT_ENABLED=true`. Ensure the container UID can create files in the spool directory. The spool uses owner-restricted files and atomic renames with sync; do not share the same spool directory between independent controllers. Back up or retain the volume before destructive upgrades.

```bash
docker compose -f compose.yml -f compose.prod.yml config --quiet
docker compose -f compose.yml -f compose.prod.yml logs --tail=100 dns-control
docker volume ls --filter name=dnsqueryspool
```

To test recovery in staging: temporarily make the Backend ingestion endpoint unavailable, issue DNS queries, confirm spool files exist, recreate DNS-Control without deleting volumes, restore Backend access, and verify the pending batches are delivered and acknowledged. Do not use `docker compose down -v` because it removes named volumes. Persisted batches may replay after an ambiguous HTTP response, so Backend event-ID deduplication is required.

**Limitations:** The reporter does not fsync the in-memory DNS request queue before returning a DNS response, and its counters reset on restart. A durable spool reduces loss for persisted batches but is not an end-to-end zero-loss guarantee.

### Unreadable spool batches

When a persisted `.json` batch cannot be decoded, DNS-Control renames it to `.json.corrupt` and continues replaying later batches. The original bytes remain on the named volume for operator inspection; they are **not** automatically delivered or deleted. Review the log entry containing `quarantined for operator review`, inspect the quarantined file, and retain or remove it according to your audit-retention policy. Quarantined files are excluded from the replay file list and the active spool-size quota, but still consume physical disk space; monitor the volume's free space separately.
