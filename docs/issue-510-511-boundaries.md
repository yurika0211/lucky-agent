# Issues #510 and #511

This note records the current implementation boundary. It is not a claim that
either issue has passed every product and deployment acceptance item.

Checked against `master` at `2647d43` on 2026-11-27.

## #510 scheduled results

The result contract now has separate `Task`, `Execution`, `Delivery`, and
device records in `luckyagent-tools/cron`. A scheduled slot is claimed once,
the result is completed before delivery rows are used, and the runtime/API
expose task history, device registration, pending results, and ACK. The Agent
selects the runtime result store and mirrors scheduler claims into it. Android
has a polling/notification implementation in a separate, not-yet-merged
worktree; it removes the old full-screen alarm behavior.

The remaining acceptance work is explicit:

- The server still needs a real post-commit FCM Data Message sender and its
  retry/blocked state transitions. Telegram delivery is not yet wired to the
  delivery rows.
- Android still needs the merged client change, execution-specific deep-link
  consumption, and device/cursor end-to-end checks. Tools PR #40 covers tied
  scheduled-slot cursor ordering but is not merged yet.
- Existing tasks have not been cleared. Before rollout, an operator must be
  named and the clearing evidence recorded; no code path silently migrates the
  old tasks.

Until those items are verified, #510 remains open. The session is no longer the
only result record, but a notification is not treated as delivery merely
because a poll or FCM request was accepted.

## #511 tool execution isolation

The tools sandbox now runs a fixed user/PID/network namespace probe before an
isolated command and returns `SANDBOX_UNAVAILABLE` when that probe fails. It
does not fall back to host execution. The change is intentionally limited to
fail-closed command admission.

The issue's trusted `luckyagent-policy` and `luckyagent-exec` services,
one-shot snapshots, generation gate, pidfd lifecycle, system users, AppArmor,
ownership, and deployment self-checks are not implemented here. The recorded
host probe still fails. Therefore this change must not be described as full OS
isolation or as solving compromise of the Agent process, and #511 remains open
until the separate execution service and deployment prerequisites exist.
