# OpenCode — Subagent Tracking One-Pager

Scope: native tracking of the `task` tool's child sessions. Session and turn
lifecycle, transcript export and plugin installation are the existing
integration (`entire_plugin.ts`, `lifecycle.go`, `cli_commands.go`). The
generic task-record model is in
[Sessions and Checkpoints](../../../../../docs/architecture/sessions-and-checkpoints.md).

## Verdict: COMPATIBLE

Verified against OpenCode 1.18.30. The plugin sees every signal Entire needs,
and the child session ID is on the launch signal, the completion signal and
the child's own export. Live verification was done during development with a
probe script that is not kept in the repo.

## Subagent Model

- The primary agent calls the **`task` tool** (`{description, prompt,
  subagent_type, task_id?, background?}`); each call runs a **child session**
  with its own ID, messages, tokens and `opencode export` payload.
- Join keys: `ToolUseID` = the task part's `callID`; `SubagentID` = the child
  session ID (`ses_…`, also the export key); the parent is the child's
  `info.parentID`. `callID` is model-generated and unique only within the
  parent.
- `task_id` resumes an existing child, so one child can back several calls.
- Child sessions are suppressed plugin-side (`childSessions`, learned from
  `parentID` and task metadata): no `session-start`, `turn-start` or
  `turn-end` is ever sent for a child, including one the user drives directly.

## Event Mapping

| OpenCode signal | Entire event | Payload |
|-----------------|--------------|---------|
| `tool.execute.before`, `tool == "task"` | (plugin only) | Records the call's start, sent as `started_at`; falls back to the part's `state.time.start` when the hook was missed. |
| `message.part.updated`, task part `running` with `metadata.sessionId` | `SubagentStart` (`subagent-start`) | `ToolUseID = callID`, `SessionID` = top-level session, `SubagentID` = child, type/description from `args`, `DeferredCompletion`. |
| `tool.execute.after`, `tool == "task"` | `SubagentEnd` (`subagent-stop`) | Same IDs, `Final`, `CompletionWithoutLaunch`, no declared transcript. |
| announced task part `error` (abort, cancel, throw) | `SubagentEnd` (`subagent-stop`) | Same payload; OpenCode skips `tool.execute.after` in these cases. |
| background child's own `session.status` idle | `SubagentEnd` (`subagent-stop`) | The held payload for that child (see below). |

## One Start, One Stop

Every announced call fires exactly one `subagent-stop` under the same
`ToolUseID`. The plugin keeps each start payload until its task stops and
every stop path takes it, so a second path finds nothing to send. A stop
that runs before the start was announced fires under the hook key and the
late announcement is skipped. A call that fails before its child is bound
was never announced and fires nothing.

- **Background** (`background: true`, or promoted mid-run): `tool.execute.after`
  returns at launch with `metadata.background`, so the stop is held per child
  and fired on the child's idle. A `task_id` call that joins a running child
  queues behind it, and each idle after a busy period fires the oldest held
  stop. When the job ends (the synthetic `<task … state="completed|error">`
  result, or a `MessageAbortedError` for the child or its launcher), every
  held stop for that child fires; for a busy child, at its next idle.
- **Nested** (`subagent_depth > 1`): a child's own task call is announced and
  stopped on the **top-level** session (`rootOf`), as its own record.
- **Command subtasks** (`subtask: true`): the hooks carry the part `id`
  (`prt_…`), not the `callID`; the plugin maps one to the other when it
  announces, so start and stop share a `ToolUseID`.
- **Abort**: any end of the parent's stream cancels the child before OpenCode
  marks the part `error`, so the stop's export already holds the child's work.

## Transcript Export and Slicing

The Final capture, after its skip checks, runs `opencode export <child>`
(`FetchSubagentTranscript`, without git's repo-override variables) and
declares the result; files and exact tokens come from that export. The
declared file is `.entire/tmp/<child>.<callID>.json`, cut to this call:
from the first non-synthetic, non-compaction user message at or after
`started_at`, up to the next such prompt (and, for a later re-export, no
later than the record's completion). Without a `started_at` the full export is declared
(`<child>.json`); a call that never prompted its child declares an empty
slice. A failed export completes the record transcript-unavailable, and
condensation (or the SessionEnd sweep) exports again with the same bounds.
The background result that OpenCode injects into the parent is synthetic
text, so prompt extraction and the summary skip it.

## Gaps & Limitations

- macOS unresolved-symlink paths (`/var/…` vs `/private/var/…`) drop from a task's `files`; the parent's turn end still covers them.
- `opencode run` aborts a running background child at exit, and its teardown instance can leave the parent ACTIVE until the exited-owner sweep; the TUI is unaffected.
- Two calls in one message that resume the same child: the queued call ends at the first run's idle, so its own work lands in no record.
- A call whose start the plugin never saw (restart mid-task) declares the child's full export.
- `callID` is opaque and not globally unique; the child session ID is the cross-process key.
- A running background child's edits do not count toward an IDLE parent's co-authorship of a commit until the child stops; condensation still stores its export so far.
