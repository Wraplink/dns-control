# Slack project reporting

This repository reports GitHub issue changes, pull requests, and failed workflow runs to Slack.

## Setup
1. Create a Slack Incoming Webhook for a channel in `wraplink.slack.com`.
2. Set `WRAPLINK_SLACK_WEBHOOK_URL` in GitHub repository Actions secrets.
3. Merge the PR, then run **Actions → Slack project reporting → Run workflow**.
4. Check the Slack channel and GitHub Actions logs.

The workflow does not execute code from untrusted pull requests and does not include issue bodies, stack traces, secrets, or user data.

## Scope and limitations
This workflow reports **GitHub activity only**. It does not capture runtime errors, application logs, DNS failures, nftables failures, or support tickets submitted through the website.

For application-level reporting, emit structured events to a backend-owned alert dispatcher, persist notifications in an outbox, redact sensitive information, deduplicate by fingerprint, retry delivery with bounded backoff, and preserve user-submitted tickets even if Slack is unavailable. Keep the Slack webhook on the server only. Report ticket IDs, categories, severity and admin links; do not send ticket body or personal details by default.

## Runtime operational alerts
Configure the service container with:
- `WRAPLINK_ALERT_API_URL`: internal backend base URL, e.g. `http://backend:8080` (use the actual configured port).
- `WRAPLINK_ALERT_INGEST_KEY`: same strong secret configured in backend.
- `WRAPLINK_ENVIRONMENT`: deployment name, e.g. `production`.

The service sends a subset of critical operational failures to backend `POST /api/v1/internal/alerts` using authenticated HTTP. Backend deduplicates and dispatches through its persistent Slack outbox. No Slack webhook is stored on agents. Network failures are best-effort and do not stop the service; this is not a persistent agent-side queue or comprehensive runtime monitoring.
