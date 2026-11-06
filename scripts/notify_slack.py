#!/usr/bin/env python3
"""Send safe GitHub event summaries to Slack."""
import json
import os
import sys
import urllib.request


def clean(value):
    return str(value or "").replace("\n", " ").replace("\r", " ")[:200]


def main():
    url = os.environ.get("SLACK_WEBHOOK_URL")
    if not url:
        print("Slack webhook is not configured; notification skipped.")
        return 0
    with open(os.environ["GITHUB_EVENT_PATH"], encoding="utf-8") as file:
        event = json.load(file)
    kind = os.environ["GITHUB_EVENT_NAME"]
    repo = os.environ["GITHUB_REPOSITORY"]
    action = event.get("action")
    if kind == "issues":
        issue = event.get("issue", {})
        if action not in ("opened", "reopened", "closed"):
            return 0
        message = f"🎫 Issue {action} · {repo}: #{issue.get('number')} {clean(issue.get('title'))}\n{issue.get('html_url', '')}"
    elif kind == "pull_request_target":
        pr = event.get("pull_request", {})
        if action == "closed" and pr.get("merged"):
            action = "merged"
        if action not in ("opened", "reopened", "closed", "merged", "ready_for_review"):
            return 0
        message = f"🔀 PR {action} · {repo}: #{pr.get('number')} {clean(pr.get('title'))}\n{pr.get('html_url', '')}"
    elif kind == "workflow_run":
        run = event.get("workflow_run", {})
        if run.get("conclusion") != "failure":
            return 0
        message = f"🚨 CI/CD failed · {repo}: {clean(run.get('name'))}\n{run.get('html_url', '')}"
    else:
        message = f"🧪 Slack notification test · {repo}"
    request = urllib.request.Request(url, data=json.dumps({"text": message}).encode(), headers={"Content-Type": "application/json"}, method="POST")
    with urllib.request.urlopen(request, timeout=10) as response:
        if response.status != 200:
            raise RuntimeError(f"Slack returned HTTP {response.status}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
