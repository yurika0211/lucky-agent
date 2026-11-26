# Issues #510 and #511

Both issues say they are decision records, not an implementation order.
They also forbid changing system configuration from the agent. This note
records what the current tree actually does, and what a change here must
not pretend to do.

Checked against `origin/master` at `c5a16ed` on 2026-11-26.

## #510 scheduled results leave the session

Today:

- `agent/cron_tools.go` writes the result back into `metadata["session_id"]`,
  or sends it through the message gateway for that chat.
- `agent/cron_task_runtime.go` runs the task and notifies in the same closure.
- `luckyagent-tools/cron` stores a job plus a short `RunHistory`. There is
  no Task / Execution / Delivery split, no `(task_id, scheduled_at)` key,
  and no device ACK.

What must not be faked here:

- An agent-side "history" would not stop two workers claiming the same slot.
- A notification sent before the result transaction commits violates the
  issue. FCM acceptance is not delivery. A poll is not an ACK.
- Clearing existing jobs is a named production action. This repository has
  not named the operator or the evidence, so it does not clear anything.

The agent can only consume a server contract: claim, final result, and
delivery rows owned by the app. Android keeps `CronWatchWorker` as the
fallback. The first version does not use `fullScreenIntent` or
`CATEGORY_ALARM` once that contract exists. It does not exist yet.

## #511 tool execution isolation

Today:

- `agent/sandbox.go` picks `dev`, `workspace-write`, or `iso` for new turns.
- Restricted modes disable tools that run outside the shell and wrap the
  terminal with bubblewrap. That is a process policy, not an OS boundary
  between a policy service and an execution service.
- There is no `luckyagent-policy` user, no `luckyagent-exec` user, no
  one-shot execution snapshot, and no pidfd lifecycle.

The issue forbids this change set from editing AppArmor, creating system
users, or changing file ownership. The 2026-11-25 probe recorded in the
issue already failed (`unprivileged_userns` denied `setpcap` and
`net_admin`). A fail-closed return string in the agent would not prove
the sandbox was the thing that stopped the command.

Until a trusted execution service exists, a tool that needs isolation and
cannot get it must fail. It must not be rewritten onto the host. This
repository cannot honestly return `SANDBOX_UNAVAILABLE` as an OS fact,
because it cannot observe that service.

## What "done" would require

| Issue | Next owner | This repo after that |
| --- | --- | --- |
| #510 | app API: Task, Execution, Delivery, claim, ACK | Call that API. Do not write the result only into the session. |
| #511 | a separate policy and exec service, plus the host setup the issue lists | Submit a permission request. Surface the service error. Do not grant the launch. |
