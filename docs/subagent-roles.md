# Subagent role files

`action=delegate` accepts an optional `subagent_type`. The name selects a Markdown file. The same frontmatter `name` in the project directory replaces the user file.

```text
~/.luckyagent/subagents/<name>.md
<workspace>/.luckyagent/subagents/<name>.md
```

`<workspace>` is the parent session working directory, or the process working directory when the session has none. The user directory is the agent config home (`config.Manager.HomeDir()`), which defaults to `~/.luckyagent`.

Frontmatter fields: `name`, `description`, `tools` or `disallowed_tools` (a whitelist wins when both are set), optional `model`, optional `prompt`, optional `max_iterations`. The Markdown body is the prompt when `prompt` is empty.

A whitelist is applied to the child tool schema before the loop starts. `delegate.child.disabled_tools` stays disabled. Recursive tools stay closed unless `allow_recursive_delegate` is set and the global switch allows it. A missing role, a missing `name`, or a whitelist that resolves to no registered tools fails the delegate and does not create a task. Omit `subagent_type` and, when `delegate.builtin_agents` is on (the default), the child uses builtin `explore` (read-only research). When that switch is off, omitting `subagent_type` keeps the current default child tool set.

`explore` and `plan` are built in when `delegate.builtin_agents` is true, the default. Their prompts are `agent/subagents/explore.md` and `agent/subagents/plan.md`. Both allow only `file_read`, `file_list`, `rag_search`, `memory`, `web_search`, and `web_fetch`. Builtin `explore` sets `max_iterations` to 10. A project or user role with the same name replaces the builtin and still cannot turn `delegate.child.disabled_tools` back on. `delegate.builtin_agents: false` makes `explore` and `plan` unknown roles.

`thoroughness` is only valid on `subagent_type=explore`, with values `quick`, `medium`, and `very_thorough`. It changes the search-scope sentence in the child prompt. It does not change the tool list or `max_iterations`. Passing it to `plan` or to a delegate without a type fails before a task is created.
