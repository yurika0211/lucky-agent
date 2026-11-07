# Subagent role files

`action=delegate` accepts an optional `subagent_type`. The name selects a Markdown file. The same frontmatter `name` in the project directory replaces the user file.

```text
~/.luckyagent/subagents/<name>.md
<workspace>/.luckyagent/subagents/<name>.md
```

`<workspace>` is the parent session working directory, or the process working directory when the session has none. The user directory is the agent config home (`config.Manager.HomeDir()`), which defaults to `~/.luckyagent`.

Frontmatter fields: `name`, `description`, `tools` or `disallowed_tools` (a whitelist wins when both are set), optional `model`, optional `prompt`. The Markdown body is the prompt when `prompt` is empty.

A whitelist is applied to the child tool schema before the loop starts. `delegate.child.disabled_tools` stays disabled. Recursive tools stay closed unless `allow_recursive_delegate` is set and the global switch allows it. A missing role, a missing `name`, or a whitelist that resolves to no registered tools fails the delegate and does not create a task. Omit `subagent_type` to keep the current child tool set.
