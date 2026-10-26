# Configuration

Entire's per-repository settings live in `.entire/settings.json` (shared with your team) and `.entire/settings.local.json` (personal, gitignored). Change them with `entire enable` flags, `entire configure`, or by editing the files.

## `entire enable` Flags

| Flag                                        | Description                                                                                                       |
| ------------------------------------------- | ----------------------------------------------------------------------------------------------------------------- |
| `--agent <name>`                            | Agent to set up hooks for: `antigravity`, `claude-code`, `codex`, `copilot-cli`, `cursor`, `factoryai-droid`, `opencode`, `pi` (external agents on `$PATH` also work). Enables non-interactive mode |
| `--yes`, `-y`                               | Accept all defaults without prompting                                                                             |
| `--force`, `-f`                             | Force reinstall hooks (removes existing Entire hooks first)                                                       |
| `--checkpoint-backend <refs\|branch>`       | Checkpoint storage backend: `refs` (default) or `branch` (legacy); see [Checkpoint Storage](sessions-and-checkpoints.md#checkpoint-storage)  |
| `--checkpoint-remote <provider:owner/repo>` | Push checkpoint data to a separate repo; providers `github`, `gitlab` (e.g., `github:org/checkpoints-repo`)      |
| `--checkpoint-push-remote <name>`           | Select an existing Git remote for checkpoints; always saves to this clone's `.entire/settings.local.json`, even with `--project` |
| `--skip-push-sessions`                      | Disable automatic pushing of checkpoint data on git push                                                           |
| `--local`                                   | Write settings to `.entire/settings.local.json` instead of `.entire/settings.json`                                |
| `--project`                                 | Write settings to `.entire/settings.json` even if it already exists                                               |
| `--absolute-git-hook-path`                  | Embed the full binary path in git hooks (for GUI git clients that don't source shell profiles)                    |
| `--import-history`                          | During first-time setup, import the selected agents' existing session history (last 30 days) without prompting     |
| `--search-skill`                            | Install the optional Entire search skill for the selected agent(s)                                                |
| `--agent-help-skill`                        | Install the Entire agent-help skill (points agents at `entire agent-help`) for the selected agent(s)              |
| `--telemetry=false`                         | Disable anonymous usage analytics                                                                                 |

Run in a directory that is not a git repository, `entire enable` offers to initialize one and make an initial commit. It is local-only — no remote is created or pushed to, so publish the repository yourself when you are ready (`gh repo create`, `entire repo create`, or your forge's web UI). That path is driven by `--init-repo` / `--no-init-repo`, `--skip-initial-commit`, and `--initial-commit-message`. See `entire enable --help` for the full list.

**Examples:**

```
# First-time setup with a specific agent
entire enable --agent claude-code

# Re-enable a disabled repo
entire enable

# Save settings locally (not committed to git)
entire enable --local
```

## `entire configure`

Use `entire configure` to update non-agent settings on a repo that's already set up. Agent installation lives under `entire agent`.

Typical uses:

- Toggle telemetry
- Reinstall the Entire git hook (`--force`, `--absolute-git-hook-path`)
- Update strategy options such as `--checkpoint-remote`, `--skip-push-sessions`, or `--checkpoint-backend`
- Pick the provider, model, and timeout for `entire checkpoint explain --generate` (`--summarize-provider`, `--summarize-model`, `--summarize-timeout-seconds`)

**Examples:**

```bash
# With no flags, print help
entire configure

# Opt out of telemetry
entire configure --telemetry=false

# Reinstall the Entire git hook with an absolute binary path
entire configure --absolute-git-hook-path

# Update strategy options on an existing repo
entire configure --checkpoint-remote github:myorg/checkpoints-private

# Choose who generates summaries
entire configure --summarize-provider claude-code
```

Adding or removing an agent lives under `entire agent`:

```bash
entire agent add claude-code          # also refreshes outdated hooks after a CLI upgrade
entire agent add claude-code --force  # rebuilds Entire's hooks if that does not fix them
entire agent remove claude-code
```

## Settings Files

Entire uses two configuration files in the `.entire/` directory:

### settings.json (Project Settings)

Shared across the team, typically committed to git. This is what `entire enable` writes for a new repo:

```json
{
  "enabled": true,
  "checkpoints": {
    "primary": { "type": "git-refs" }
  }
}
```

### settings.local.json (Local Settings)

Personal overrides, gitignored by default:

```json
{
  "enabled": false,
  "log_level": "debug"
}
```

### Configuration Options

| Option                                    | Values                                       | Description                                                                       |
| ----------------------------------------- | -------------------------------------------- | --------------------------------------------------------------------------------- |
| `enabled`                                 | `true`, `false`                              | Enable/disable Entire                                                             |
| `log_level`                               | `debug`, `info`, `warn`, `error`             | Logging verbosity                                                                 |
| `checkpoints.primary.type`                | `git-refs`, `git-branch`                     | Checkpoint storage backend — `git-refs` is written by `entire enable`; absent means legacy `git-branch`. See [Checkpoint Storage](sessions-and-checkpoints.md#checkpoint-storage) |
| `telemetry`                               | `true`, `false`                              | Send anonymous usage statistics to PostHog                                        |
| `absolute_git_hook_path`                  | `true`, `false`                              | Embed the full binary path in git hooks, for GUI git clients that don't source shell profiles |
| `commit_linking`                          | `always`, `prompt`                           | Link commits to sessions automatically, or ask each time (default `prompt`)        |
| `sign_checkpoint_commits`                 | `true`, `false`                              | Sign checkpoint commits (default: on). See [checkpoint signing](../architecture/checkpoint-signing.md) |
| `strategy_options.push_sessions`          | `true`, `false`                              | Auto-push checkpoint data on git push (default `true`)                            |
| `strategy_options.checkpoint_remote`      | `{"provider": "github", "repo": "org/repo"}` | Push checkpoint data to a separate repo (see [Where Checkpoints Are Uploaded](checkpoint-sync.md))                               |
| `strategy_options.checkpoint_push_remote` | remote name, e.g. `"upstream"`               | Pin which single remote carries checkpoint data (see [Where Checkpoints Are Uploaded](checkpoint-sync.md))                       |
| `strategy_options.filtered_fetches`       | `true`, `false`                              | Use `--filter=blob:none` on checkpoint fetches                                    |
| `strategy_options.checkpoint_ref_list_timeout_seconds` | seconds (default `5`, max `120`) | How long `entire checkpoint list` waits for each remote to list checkpoints that aren't local yet. Raise it if checkpoints pushed from another machine are missing because the remote answers slowly |
| `strategy_options.summarize.enabled`      | `true`, `false`                              | Auto-generate AI summaries at commit time                                         |
| `summary_generation.provider`             | e.g. `claude-code`, `codex`, `pi`            | Which agent generates summaries (see [Auto-Summarization](#auto-summarization))                              |
| `summary_generation.model`                | provider-specific model hint                 | Model hint for summary generation (requires `provider`)                            |
| `summary_timeout_seconds`                 | seconds                                      | Hard deadline for `entire checkpoint explain --generate`. Unset or `0` means **no deadline** |
| `redaction.*`                             | nested object                                | PII redaction, custom secret patterns, and scanner engines — documented in [docs/security-and-privacy.md](../security-and-privacy.md) |

> **Deprecated:** the OpenAI Privacy Filter (`redaction.openai_privacy_filter`) is deprecated and will be removed in a future release. Existing configurations keep working until then, but don't enable it in new setups.

## Agent Hook Configuration

Each agent stores its hook configuration in its own directory. When you run `entire enable`, hooks are installed in the appropriate location for each selected agent:

| Agent            | Hook Location                    | Format               |
| ---------------- | -------------------------------- | -------------------- |
| Antigravity      | `.agents/hooks.json`             | JSON hooks config    |
| Claude Code      | `.claude/settings.json`          | JSON hooks config    |
| Codex            | `.codex/hooks.json`              | JSON hooks config    |
| Copilot CLI      | `.github/hooks/entire.json`      | JSON hooks config    |
| Cursor           | `.cursor/hooks.json`             | JSON hooks config    |
| Factory AI Droid | `.factory/settings.json`         | JSON hooks config    |
| OpenCode         | `.opencode/plugins/entire.ts`    | TypeScript plugin    |
| Pi               | `.pi/extensions/entire/index.ts` | TypeScript extension |

You can enable multiple agents at the same time — each agent's hooks are independent. Entire detects which agents are active by checking for installed hooks, not by a setting in `settings.json`.

Agents not listed here can integrate through the [external agent protocol](../architecture/external-agent-protocol.md): an `entire-agent-<name>` binary on `$PATH` appears as an agent option.

## Auto-Summarization

When enabled, Entire automatically generates AI summaries for checkpoints at commit time. Summaries capture intent, outcome, learnings, friction points, and open items from the session.

```json
{
  "strategy_options": {
    "summarize": {
      "enabled": true
    }
  }
}
```

Summaries are also generated on demand, with or without this setting, by `entire checkpoint explain --generate`.

**Which agent writes them.** Commit-time summaries default to Claude Code (`claude` on your `PATH`, model `sonnet`). Without a provider set, `entire checkpoint explain --generate` instead picks an installed summary-capable agent — asking when there are several — and saves the choice. Set one explicitly with `summary_generation.provider` — `antigravity`, `claude-code`, `codex`, `copilot-cli`, `cursor`, `opencode`, or `pi`, plus an optional `summary_generation.model` hint:

```bash
entire configure --summarize-provider codex
```

`factoryai-droid` cannot generate summaries. Whichever provider you pick must be installed and authenticated.

**Requirements:**

- The configured provider's CLI installed and authenticated
- Summary generation is non-blocking: failures are logged but don't prevent commits

## Settings Priority

Local settings override project settings field-by-field. `entire status --detailed` shows the state of each settings file.

The main exceptions to field-by-field merging:

- The `checkpoints` block is **replaced wholesale** by the local one, not deep-merged — it selects a backend, so a half-merged block would be meaningless.
- `strategy_options` merges only its top-level keys, so a nested object such as `summarize` or `checkpoint_remote` in the local file replaces the project one; `review` is also replaced wholesale.
- A `settings.local.json` that is **tracked in git** is ignored (its `checkpoints` block still applies), and Entire tells you to `git rm --cached` it. `.gitignore` doesn't apply to an already-tracked path, so a committed local file would otherwise override project settings for everyone who clones.

## Agent-Specific Steps & Limitations

- Codex hooks are enabled by default (codex-cli 0.124.0+), so enabling Entire for Codex only installs `.codex/hooks.json` — no `config.toml` is needed and Entire never creates one. If an older Entire version left a `.codex/config.toml` behind and your repo lives inside `~/.codex/agents`, delete that file to stop Codex's "malformed agent role definition" startup warning.
- Entire supports Cursor IDE and Cursor Agent CLI tool. Commands (`doctor`, `status` etc.) work the same as all other agents.
- Entire supports Copilot CLI, but not Copilot in VS Code, in other IDEs, or on github.com.
- Entire supports the Pi coding agent. Pi uses a TypeScript extension instead of a JSON hook config. Subagent capture is not currently available.

Per-agent setup guides are at [docs.entire.com/agents](https://docs.entire.com/agents/overview).
