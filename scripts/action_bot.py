#!/usr/bin/env python3
"""Read-only GitHub Action bot for LuckyAgent maintenance evidence.

The bot does not merge, close, or edit issues. It comments a digest when the
fingerprint changes, and otherwise leaves the issue alone.
"""

import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import urllib.error
import urllib.parse
import urllib.request
from datetime import datetime, timezone


REPO = "yurika0211/lucky-agent"
MARKER = "<!-- luckyagent-action-bot -->"
PSEUDO_VERSION = re.compile(
    r"^(?P<prefix>v\d+\.\d+\.\d+)-0\.\d{14}-(?P<commit>[0-9a-f]{12})$"
)


def github_request(method, path, token, payload=None):
    request = urllib.request.Request(
        "https://api.github.com" + path,
        data=None if payload is None else json.dumps(payload).encode(),
        method=method,
        headers={
            "Accept": "application/vnd.github+json",
            "Authorization": f"Bearer {token}",
            "User-Agent": "luckyagent-action-bot",
            "X-GitHub-Api-Version": "2022-11-28",
        },
    )
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            body = response.read().decode()
            return json.loads(body) if body else None
    except urllib.error.HTTPError as error:
        detail = error.read().decode(errors="replace")
        raise SystemExit(f"GitHub API {method} {path} failed: {error.code} {detail}") from error


def paginated(path, token):
    page = 1
    while True:
        separator = "&" if "?" in path else "?"
        batch = github_request("GET", f"{path}{separator}per_page=100&page={page}", token)
        if not batch:
            return
        yield from batch
        if len(batch) < 100:
            return
        page += 1


def run_git(*args):
    return subprocess.run(
        ["git", *args], check=True, capture_output=True, text=True
    ).stdout.strip()


def module_deps(go_mod):
    with open(go_mod, encoding="utf-8") as handle:
        text = handle.read()
    direct = []
    for raw in text.splitlines():
        line = raw.split("//", 1)[0].strip()
        match = re.search(r"(github\.com/yurika0211/\S+)\s+(\S+)$", line)
        if match and "indirect" not in raw:
            direct.append((match.group(1), match.group(2)))
    return direct


def compare_dependencies(token):
    rows = []
    for module, version in module_deps("go.mod"):
        owner_repo = module.removeprefix("github.com/")
        commit = None
        pseudo = PSEUDO_VERSION.match(version)
        if pseudo:
            commit = pseudo.group("commit")
        elif re.fullmatch(r"v\d+\.\d+\.\d+", version):
            try:
                release = github_request(
                    "GET", f"/repos/{owner_repo}/git/ref/tags/{urllib.parse.quote(version)}", token
                )
                obj = release["object"]
                commit = obj["sha"]
                if obj["type"] == "tag":
                    commit = github_request("GET", f"/repos/{owner_repo}/git/tags/{obj['sha']}", token)["object"]["sha"]
            except SystemExit:
                rows.append((module, version, "tag missing", "unknown"))
                continue
        else:
            rows.append((module, version, "unparsed", "unknown"))
            continue
        branch = github_request("GET", f"/repos/{owner_repo}", token)["default_branch"]
        head = github_request("GET", f"/repos/{owner_repo}/commits/{branch}", token)["sha"]
        state = "current" if head.startswith(commit) else f"behind {head[:12]}"
        rows.append((module, version, commit[:12], state))
    return rows


def open_issues(token):
    return list(paginated(f"/repos/{REPO}/issues?state=open", token))


def issue_rows(issues):
    rows = []
    for issue in issues:
        if "pull_request" in issue:
            continue
        labels = ",".join(label["name"] for label in issue["labels"]) or "-"
        rows.append((issue["number"], labels, issue["title"], issue["html_url"]))
    return rows


def workflow_failures(token):
    runs = github_request(
        "GET", f"/repos/{REPO}/actions/runs?per_page=20&status=completed", token
    )["workflow_runs"]
    failures = []
    for run in runs:
        if run["conclusion"] in {"failure", "timed_out"} and run["head_branch"] in {"master", "main"}:
            failures.append((run["name"], run["head_sha"][:12], run["html_url"], run["created_at"]))
    return failures[:5]


def performance_summary(path):
    if not path or not os.path.exists(path):
        return "这次没有性能结果。"
    bench = json.load(open(path, encoding="utf-8"))
    lines = [
        "| 基准 | 操作 | ns/op | B/op | allocs/op | 指标 |",
        "| --- | ---: | ---: | ---: | ---: | --- |",
    ]
    for item in bench:
        extra = ", ".join(f"{key}={value:g}" for key, value in item.get("metrics", {}).items()) or "-"
        lines.append(
            f"| `{item['name']}` | {item['ops']} | {item['ns_per_op']:.0f} | "
            f"{item['bytes_per_op']} | {item['allocs_per_op']} | {extra} |"
        )
    return "\n".join(lines)


def render(deps, issues, failures, bench_path, sha):
    now = datetime.now(timezone.utc).strftime("%Y-%m-%d %H:%M UTC")
    dep_lines = ["| 模块 | 当前 pin | 提交 | 相对默认分支 |", "| --- | --- | --- | --- |"]
    dep_lines.extend(f"| `{module}` | `{version}` | `{commit}` | {state} |" for module, version, commit, state in deps)
    issue_lines = [f"- #{number} [{labels}] [{title}]({url})" for number, labels, title, url in issues]
    failure_lines = [f"- [{name} {sha_}]({url}) {created}" for name, sha_, url, created in failures] or ["- 最近 master 没有失败的 workflow。"]
    behind = [row for row in deps if row[3].startswith("behind")]
    body = f"""{MARKER}
## LuckyAgent Action 机器人 · {now}

检查提交 `{sha[:12]}`。这是只读摘要，不合并、不关闭 issue。

### 依赖漂移
{chr(10).join(dep_lines)}

默认分支落后的模块：{len(behind)}。伪版本落后不代表必须升级，只说明主仓还没选那次提交。

### Open issues（{len(issues)}）
{chr(10).join(issue_lines)}

### 最近 master workflow 失败
{chr(10).join(failure_lines)}

### 性能
{performance_summary(bench_path)}

检查点轨迹对应 #342：每次保存都重编码整份检查点。这里的 `encoded-bytes/trajectory` 是一次轨迹的累计编码字节，不是单次文件大小。
"""
    return body


def fingerprint(body):
    stripped = re.sub(r"· \d{4}-\d{2}-\d{2} \d{2}:\d{2} UTC", "", body)
    stripped = re.sub(r"检查提交 `[^`]+`", "", stripped)
    return stripped


def upsert_comment(token, issue_number, body, dry_run):
    comments = list(paginated(f"/repos/{REPO}/issues/{issue_number}/comments", token))
    existing = next((item for item in comments if MARKER in item["body"]), None)
    if existing and fingerprint(existing["body"]) == fingerprint(body):
        print(f"issue #{issue_number} digest unchanged")
        return
    if dry_run:
        print(body)
        return
    if existing:
        github_request("PATCH", f"/repos/{REPO}/issues/comments/{existing['id']}", token, {"body": body})
        print(f"updated digest on #{issue_number}")
    else:
        github_request("POST", f"/repos/{REPO}/issues/{issue_number}/comments", token, {"body": body})
        print(f"posted digest on #{issue_number}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--issue", type=int, required=True)
    parser.add_argument("--bench", default="")
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args()
    token = os.environ.get("GITHUB_TOKEN", "")
    if not token and not args.dry_run:
        raise SystemExit("GITHUB_TOKEN is required")
    if args.dry_run and not token:
        print("dry-run without token: dependency and issue checks skipped")
        print(performance_summary(args.bench))
        return 0
    deps = compare_dependencies(token) if Path("go.mod").exists() else []
    issues = issue_rows(open_issues(token))
    failures = workflow_failures(token)
    sha = os.environ.get("GITHUB_SHA") or run_git("rev-parse", "HEAD")
    upsert_comment(token, args.issue, render(deps, issues, failures, args.bench, sha), args.dry_run)
    return 0


if __name__ == "__main__":
    sys.exit(main())
