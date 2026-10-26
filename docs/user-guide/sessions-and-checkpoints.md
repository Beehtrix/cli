# Sessions and Checkpoints

How the CLI captures agent sessions, links them to your commits as checkpoints, and stores them.

## Getting Started

### 1. Enable Entire in Your Repository

```
entire enable
```

On a repo that has not been enabled yet, `entire enable` runs the initial enable flow: it creates Entire settings, installs git hooks, and prompts you to choose which agent hooks to install. To enable a specific agent non-interactively, use `entire enable --agent <name>` (for example, `entire enable --agent cursor`).

After setup:

- Use `entire enable` to turn Entire back on if the repo is currently disabled.
- Use `entire agent` to add or remove agents.
- Use `entire configure` to update non-agent settings (telemetry, hooks, checkpoint remote, summary provider).

The hooks capture session data as you work. Checkpoints are created when you or the agent make a git commit. Your code commits stay clean: Entire never creates commits on your active branch. Session metadata is stored outside your branch's history, in the checkpoint storage described under [Checkpoint Storage](#checkpoint-storage).

#### Existing git hooks

If your repository already has git hooks (your own scripts, git-lfs, lefthook, pre-commit), `entire enable` keeps them: each one is moved to `<hook>.pre-entire` in the hooks directory, and Entire's hook runs first, then yours.

- If a hook manager reinstalls its hooks later, Entire's hooks come back on the next agent turn or `entire enable`, chained to the hook manager's version. If the hook already had a `<hook>.pre-entire` copy, the hook manager's version takes its place and runs after Entire's; the older copy is kept as `<hook>.pre-entire.<timestamp>` and no longer runs, with a warning naming the file.
- `entire disable --uninstall` removes Entire's hooks and puts yours back (it also removes `.entire/`, session state, and agent hooks). Older `<hook>.pre-entire.<timestamp>` copies are left in place and listed.

### 2. Work with Your AI Agent

Use your AI agents as before. Entire runs in the background, tracking your session:

```
entire status  # Check current session status anytime
```

### 3. Understand Past Work

```
entire checkpoint explain <id>    # the story behind a checkpoint, commit, or session
entire search "<what you want>"   # find past work by intent (requires entire login)
entire recap                      # summarize recent checkpoint activity
```

### 4. Resume a Previous Session

```
entire session resume            # interactive picker
entire session resume <branch>   # latest checkpointed session(s) for a branch
```

With a branch, Entire checks out the branch and restores the session logs from its latest checkpoint (one or more sessions), keeping an existing local log unless you pass `--force`. If the branch has newer commits without checkpoints, it offers to reset to the last checkpointed commit. In a terminal it then asks whether to start the agent; otherwise it prints the command(s) to continue.

### 5. Disable Entire (Optional)

```
entire disable
```

Turns Entire off: its hooks stay installed but do nothing. To remove Entire from the repository — its hooks, `.entire/` settings, and session state — and restore any git hooks you had before, use `entire disable --uninstall`. Your code and commit history remain untouched.

## Key Concepts

### Sessions

A **session** represents a complete interaction with your AI agent, from start to finish. Each session captures all prompts, responses, files modified, and timestamps.

The session ID is the unique identifier the agent itself provides — Entire never mints its own — so an ID you see in `entire session list` is the same one the agent uses. The format is the agent's to choose: most supply a UUID (e.g. `019efea2-b46a-7cbc-be01-4c13460f5019`), while OpenCode uses `ses_`-prefixed IDs.

Sessions are stored separately from your code commits, in the repo's checkpoint storage.

### Checkpoints

A **checkpoint** is the code change paired with the conversation behind it — a "save point" in your work.

Checkpoints are created when you or the agent make a git commit, and the commit carries an `Entire-Checkpoint: <id>` trailer linking the two.

**Checkpoint IDs** are 26-character ULIDs (e.g. `01K9TQ8ZP7X3F5M2WVJ4CNRB6D`), which sort by creation time. Checkpoints on the [legacy branch backend](#legacy-branch-storage) carry a 12-character hex ID (e.g. `a3b2c4d5e6f7`); both formats stay readable in the same repo, and you can pass either to any command that takes a checkpoint ID.

### Checkpoint Storage

Checkpoints live in your repository's own git object store, never in your branch's history. Each checkpoint is its own git ref:

```
refs/entire/checkpoints/<shard>/<id>
```

The ref points at a commit whose tree *is* that checkpoint — `metadata.json`, the per-session transcript files, and any subagent task records. `<shard>` is the last two characters of the ID, which keeps the refs evenly distributed.

Because checkpoints are independent refs, they are written, pushed, and fetched independently. There is no shared branch tip for concurrent sessions to contend on, and a reader can fetch exactly the one checkpoint it needs instead of a whole history. A checkpoint written on another machine is fetched on demand the first time you read it.

A first-time `entire enable` sets this up; there is nothing to configure. It is recorded in `.entire/settings.json` as:

```json
{
  "checkpoints": {
    "primary": { "type": "git-refs" }
  }
}
```

Inspect what a repo has locally with plain git, or through Entire:

```bash
git for-each-ref refs/entire/checkpoints    # the raw refs
entire checkpoint list                      # checkpoints on this branch
entire checkpoint explain <id>              # one checkpoint in full
```

#### Legacy branch storage

Repositories enabled by older versions of Entire, and any repo whose settings have no `checkpoints` block, use the legacy backend: one shared `entire/checkpoints/v1` branch holding every checkpoint. It keeps working. To move such a repo to per-checkpoint refs:

```bash
entire doctor migrate-checkpoints            # convert existing checkpoints first (idempotent; --dry-run to preview)
entire configure --checkpoint-backend refs   # then write new checkpoints as refs
```

Run the migration before switching: once `git-refs` is the primary store, `migrate-checkpoints` has nothing to do, and switching the backend alone does not convert existing checkpoints. Checkpoints left on the branch stay readable after the switch. `--checkpoint-backend branch` (on `enable` or `configure`) selects the legacy backend explicitly, for interoperating with older clients.

### How It Works

```
Your Branch                      Checkpoint storage
     │                                  │
     ▼                                  │
[Base Commit]                           │
     │                                  │
     │  ┌─── Agent works ───┐           │
     │  │  Turn 1           │           │
     │  │  Turn 2           │           │
     │  │  Turn 3           │           │
     │  └───────────────────┘           │
     │                                  │
     ▼                                  ▼
[Your Commit] ─────────────────► [Checkpoint]
  Entire-Checkpoint: <id>          (transcript, prompts,
     │                              files touched, tokens)
     ▼
```

As you work, Entire tracks which files the agent touched (and their content hashes) in local session state, alongside a local copy of the transcript; nothing is written to git until you commit. When you commit, that work is condensed into a permanent checkpoint and linked to your commit by an `Entire-Checkpoint` trailer. Secrets are redacted before the checkpoint is written (see [Security & Privacy](../security-and-privacy.md)).

Entire never creates commits on your branch, so this works the same on `main` and on feature branches.

### Git Worktrees

Entire works with [git worktrees](https://git-scm.com/docs/git-worktree). Each worktree has independent session tracking, so you can run multiple AI sessions in different worktrees without conflicts.

### Concurrent Sessions

Multiple AI sessions can run on the same commit. If you start a second session while another has uncommitted work, Entire warns you and tracks them separately. Both sessions' checkpoints are preserved. A commit includes the sessions that are still running and any other session that edited the files you committed.
