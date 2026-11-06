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
