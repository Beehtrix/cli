# Entire CLI

[Entire](https://entire.com) is a Git forge built for agent–human collaboration. This CLI is how you use it from the terminal: create and host repositories, mirror them across regions, run work through Trails, and capture every agent session into your repo so the reasoning behind your code is never lost.

Entire is built on three primitives, and the CLI covers all of them:

- **Distributed Git hosting** — Entire runs a Git-compatible source control server built for agent traffic. Create Entire-native repositories, or mirror your GitHub repositories, and place them on clusters in the regions you choose for data residency and for agents that clone and push in parallel. → [Repositories & Git Hosting](#repositories--git-hosting)
- **Trails** — instead of reviewing a diff in a pull request, a Trail gives you a heads-up display for a change: its intent, the branches across repositories that implement it, the agent sessions behind them, and findings from automated, customizable evals that agents score on every push. → [Trails](#trails)
- **Continuous memory** — sessions and prompts from any coding agent are synced into your Git repository as checkpoints linked to your commits, and stay searchable by you and your agents. → [Capturing Agent Sessions](#capturing-agent-sessions)

The ecosystem is open: the CLI works with Claude Code, Codex, Cursor, Copilot CLI, OpenCode, Antigravity, Factory AI Droid, and Pi (and more through the [external agent protocol](docs/architecture/external-agent-protocol.md)), extends through [plugins](#plugins), and Trails report CI results from partners such as Buildkite and Depot.

The CLI is open source under the MIT license. Full documentation lives at **[docs.entire.com](https://docs.entire.com)**.

## Table of Contents

- [Requirements](#requirements)
- [Quick Start](#quick-start)
- [Release Channels](#release-channels)
- [Repositories & Git Hosting](#repositories--git-hosting)
- [Trails](#trails)
- [Capturing Agent Sessions](#capturing-agent-sessions)
- [Key Concepts](#key-concepts)
- [Search, Activity & the API](#search-activity--the-api)
- [Headless & CI Authentication](#headless--ci-authentication)
- [Commands Reference](#commands-reference)
- [Plugins](#plugins)
- [Configuration](#configuration)
- [Security & Privacy](#security--privacy)
- [Troubleshooting](#troubleshooting)
- [Development](#development)
- [Getting Help](#getting-help)
- [License](#license)

## Requirements

- Git
- macOS, Linux or Windows
- For session capture, a [supported agent](#agent-hook-configuration) installed and authenticated
- Go 1.27.3+ only if you install with `go install` (the packaged installs bundle their own runtime)

Hosting, mirroring, Trails, and search need an Entire account (`entire login`). Capturing agent sessions into your repository works without one.

## Quick Start

### 1. Install

**macOS and Linux** — with Homebrew:

```bash
brew install --cask entireio/tap/entire
```

Use the fully-qualified cask name (`entireio/tap/entire`, not `entire`). Homebrew 6 requires third-party taps to be trusted before it will evaluate them, and a fully-qualified name taps and trusts just that one cask, so no separate `brew tap` / `brew trust` step is needed. Requires Homebrew 6.0.10 or newer.

Or with the install script:

```bash
curl -fsSL https://entire.com/install.sh | bash
```

**Windows** — with Windows PowerShell 5.1 or later:

```powershell
irm https://entire.com/install.ps1 | iex
```

For stable releases, the PowerShell installer uses Scoop when it is available, adding the Entire bucket if needed. Without Scoop — and for nightly releases — it verifies the release checksum and installs both `entire.exe` and `git-remote-entire.exe` to `%USERPROFILE%\.local\bin`, adding that directory to your user `PATH` if needed.

Or install the stable release with Scoop directly:

```powershell
scoop bucket add entire https://github.com/entireio/scoop-bucket.git
scoop install entire/entire
```

<details>
<summary>Migrating an old <code>cli</code> Scoop install (package rename)</summary>

The Scoop package was renamed from `cli` to `entire`. If your install is still registered as the old `cli` package, run the migration below. It installs the new package before removing the old one, so the old install is only removed once the new one is in place (`scoop reset` re-links the shared `entire.exe` and `git-remote-entire.exe` shims). Run it where `entire` is **not** running — a live `entire.exe` locks its own shim, so Scoop can't relink or uninstall it mid-run:

```powershell
cmd.exe /D /C "scoop install entire/entire && scoop uninstall entire/cli && scoop reset entire"
```

If the first step fails with "couldn't find manifest", your bucket clone predates the renamed package — run `scoop update` to refresh it, then retry the command above. Nothing is removed until the install succeeds, so a failed attempt leaves your existing install working.

</details>

<details>
<summary>Go (development/manual setup)</summary>

```bash
go install github.com/entireio/cli/cmd/entire@latest

# The git remote helper that resolves entire:// URLs, needed for `entire repo clone`
# and `git clone entire://…`. The packaged installs above bundle it.
go install github.com/entireio/cli/cmd/git-remote-entire@latest

# Add Go binaries to PATH (add to ~/.zshrc or ~/.bashrc if not already configured)
export PATH="$HOME/go/bin:$PATH"
```

Install both, or just `entire` if you never clone over `entire://`. Git finds the helper by name on `$PATH`, which is what `go install` produces, so nothing else needs configuring. Like git itself, the helper honours `GIT_HTTP_USER_AGENT` for the HTTP `User-Agent` it sends.

One difference from a stable release: a `go install` build leaves [experimental commands](#experimental-commands) visible in `entire help`, the same as a nightly or a local build.

</details>

### 2. Sign in

```bash
entire login
```

### 3. Put a repository on Entire

Mirror an existing GitHub repository onto one or more regions, then clone it from the nearest one:

```bash
entire repo mirror add                       # wizard: pick repos and regions
entire repo clone /gh/<owner>/<repo> --nearest
```

Or create an Entire-native repository:

```bash
entire project create                        # wizard: owner, name, region
entire repo create <name> --project <project>
entire repo clone /et/<project>/<name>
```

### 4. Capture your agent sessions

```bash
cd your-project
entire enable
```

`entire enable` asks which agents to connect, installs their hooks and Entire's git hooks, and writes `.entire/settings.json`. Then use your agent and commit as usual — each commit that includes agent work gets a checkpoint:

```bash
entire status                    # current sessions and where checkpoints are uploaded
entire checkpoint explain <id>   # the prompt, transcript, and files behind a checkpoint
entire search "why did we add rate limiting"
```

After the initial setup, use `entire agent` to add or remove agents, `entire configure` to update non-agent settings, and `entire enable` / `entire disable` to toggle session capture on or off.

## Release Channels

Entire currently ships two release channels:

- `stable`: recommended for most users. Stable releases change less often and are the default for Homebrew, Scoop, `install.sh`, and `install.ps1`.
- `nightly`: prerelease builds for users who want the latest changes earlier. Nightlies are published more frequently and may include newer, less-proven changes than stable.

| Installer   | Stable                                               | Nightly                                                                   |
| ----------- | ---------------------------------------------------- | ------------------------------------------------------------------------- |
| Homebrew    | `brew install --cask entireio/tap/entire`            | `brew install --cask entireio/tap/entire@nightly`                         |
| install.sh  | `curl -fsSL https://entire.com/install.sh \| bash`   | `curl -fsSL https://entire.com/install.sh \| bash -s -- --channel nightly` |
| install.ps1 | `irm https://entire.com/install.ps1 \| iex`          | `iex "& {$(irm https://entire.com/install.ps1)} -Channel nightly"`        |
| Scoop       | `scoop install entire/entire`                        | not available — use `install.ps1`                                         |

### If GitHub is down

Release artifacts are published to `install.entire.com` as well as to GitHub, and `install.sh` reads that origin first — so the normal install command keeps working through a GitHub outage. If you need to bypass version discovery entirely, pin a version or fetch the archive directly:

```bash
curl -fsSL https://entire.com/install.sh | bash -s -- --version 0.10.0
curl -fsSL https://install.entire.com/v0.10.0/entire_darwin_arm64.tar.gz | tar -xz
```

Homebrew and Scoop still resolve their manifests from GitHub, so use one of the commands above (or `go install github.com/entireio/cli/cmd/entire@v0.10.0`, which is served from the Go module proxy) while GitHub is unavailable. See [Release Origins](docs/architecture/release-origins.md).

## Repositories & Git Hosting

Entire hosts Git repositories on clusters in several regions. Agents clone, fetch, and push against the cluster closest to where they run, and you choose which regions hold a copy of your code. Repositories live in **projects**, which belong to an **organization** or a personal account.

Every repository command names its forge:

- `/et/<project>/<repo>` — an **Entire-native** repository, hosted on Entire
- `/gh/<owner>/<repo>` — a **GitHub mirror**, kept in sync with its GitHub upstream
- `entire://<cluster>/…` — a full clone URL that names the cluster directly

### Regions and clusters

```bash
entire cluster list                          # clusters you can place projects and repos on
entire org create <name> --region <slug>     # jurisdiction such as us or eu
entire project create <name> --owner <org> --region <slug>
```

Organizations and projects carry a **region** (jurisdiction) for data residency. Individual repositories are placed on **clusters** — hosts such as `aws-us-east-2.entire.io` or `aws-eu-central-1.entire.io` — and a repository can have copies on several of them.

### Create an Entire-native repository

```bash
entire repo create web --project acme                       # private by default
entire repo create web --project acme --visibility public   # readable by any signed-in Entire user
entire repo create                                          # wizard: project, name, visibility, options
```

`entire repo create` waits for provisioning to finish (bounded by `--wait-timeout`, or skip with `--no-wait`) and supports `--object-format sha256`. To replicate a native repository into more regions, add mirrors of it: `entire repo mirror add /et/acme/web --cluster aws-eu-central-1.entire.io`.

### Mirror a GitHub repository

```bash
entire repo mirror list                      # your mirrors, and GitHub repos you could onboard
entire repo mirror add                       # wizard: pick repos, pick regions
entire repo mirror add /gh/acme/web --cluster aws-us-east-2.entire.io,aws-eu-central-1.entire.io
entire repo mirror resync /gh/acme/web       # catch a mirror up with GitHub now
entire repo mirror remove /gh/acme/web --cluster aws-eu-central-1.entire.io
```

A mirror keeps GitHub as the source of truth: clones and fetches come from Entire, and pushes through a mirror are forwarded to GitHub, where your GitHub permissions, branch protection, and required checks still apply. `entire repo mirror detach /gh/acme/web --into /et/acme/web` converts a mirror into an Entire-native repository when you are ready to move off GitHub (`--dry-run` lists the preconditions first).

### Clone and connect existing checkouts

```bash
entire repo clone /et/acme/web                    # pick a cluster when there are several
entire repo clone /gh/acme/web --nearest          # time each cluster and use the fastest
entire repo clone /gh/acme/web --cluster aws-eu-central-1.entire.io

entire repo remote add entire                     # add Entire alongside your existing origin
entire repo remote add origin --override          # or route origin itself through Entire
```

Clones use `entire://` URLs, resolved by the `git-remote-entire` helper that ships with the packaged installs. After that, it is plain Git: `git fetch`, `git pull`, and `git push` work as usual.

**Entire-native branches.** On a mirror, branches named `entire/unmirrored/<name>` are never forwarded to GitHub. They stay on the Entire cluster that received the push, so they are not subject to GitHub's push rate limits and keep working during a GitHub outage. Push the same commits to a branch without the prefix when you want them on GitHub. Entire does not currently back up unmirrored branches, and they are not visible from mirrors in other regions.

### Manage repositories and access

```bash
entire repo view /et/acme/web                       # identity, visibility, every cluster holding a copy
entire repo list --project acme
entire repo edit /et/acme/web --visibility private
entire repo protection add /et/acme/web main        # refuse force pushes and deletion
entire repo protection add /et/acme/web main --server-side-merge-only
entire repo grant add /et/acme/web github:alice --role writer
entire project grant add acme github:alice --role reader
entire org invite send acme-org --email dev@example.com --role admin
entire repo delete /et/acme/web
```

Branch protection applies to Entire-native repositories; a GitHub mirror uses its upstream's protection. With `--server-side-merge-only`, a branch moves only through a merge Entire performs, such as a Trail merge.

Commands that need a choice offer a picker in a terminal and accept flags in scripts, and most take `--json`. See [Repositories](https://docs.entire.com/guides/repositories/overview) for the full guide.

## Trails

A **Trail** captures the intent behind a piece of work — a title, description, type, priority, and assignees — and links the repository branches that implement it, even across several repositories in a project. Each branch on a Trail collects its agent sessions, review findings, CI results from connected providers, and approvals, and merges from the Trail when it is ready.

```bash
entire trail create --title "Add rate limiting" --type feature   # publishes the current branch
entire trail list --project et/acme --status open
entire trail show                       # the trail for the current branch
entire trail watch                      # stream reviews, findings, and runner events live

entire trail finding                    # findings dashboard for this branch
entire trail finding apply <id>         # apply a finding's suggested fix
entire trail finding resolve <id> -m "fixed in abc123"

entire trail comment add -m "Should this cover the admin API too?"
entire trail approve
entire trail merge                      # merge this branch's change into its base
entire trail resume 42                  # restore the trail branch's sessions and continue
```

Trails select the project from the current repository (`gh/<owner>` or `et/<project>`); pass `--project` or `--repo` to work on another one without a checkout. Other subcommands are `checkout`, `link`, `unlink`, `update`, and `approvals`.

**Runners** are the per-repository evaluators — risk, confidence, drift, security, review, and others — that score each push on a Trail. They live in `.entire/runners/`, and `entire runner setup` creates the default set and can tailor its prompts to your repository using its docs, history, and past findings.

## Capturing Agent Sessions

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
- `entire disable --uninstall` removes Entire's hooks and puts yours back. Older `<hook>.pre-entire.<timestamp>` copies are left in place and listed.

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

With a branch, Entire checks out the branch, restores the latest checkpointed session metadata (one or more sessions), and prints command(s) to continue.

### 5. Disable Entire (Optional)

```
entire disable
```

Turns Entire off: its hooks stay installed but do nothing. To remove Entire's hooks and restore any hooks you had before, use `entire disable --uninstall`. Your code and commit history remain untouched.

## Key Concepts

### Sessions

A **session** represents a complete interaction with your AI agent, from start to finish. Each session captures all prompts, responses, files modified, and timestamps.

The session ID is the unique identifier the agent itself provides — Entire never mints its own — so an ID you see in `entire session list` is the same one the agent uses. The format is the agent's to choose: most supply a UUID (e.g. `019efea2-b46a-7cbc-be01-4c13460f5019`), while OpenCode uses `ses_`-prefixed IDs.

Sessions are stored separately from your code commits, in the repo's checkpoint storage.

### Checkpoints

A **checkpoint** is the code change paired with the conversation behind it — a "save point" in your work.

Checkpoints are created when you or the agent make a git commit, and the commit carries an `Entire-Checkpoint: <id>` trailer linking the two.

**Checkpoint IDs** are 26-character ULIDs (e.g. `01K9TQ8ZP7X3F5M2WVJ4CNRB6D`), which sort by creation time. Checkpoints written by older versions of Entire carry a legacy 12-character hex ID (e.g. `a3b2c4d5e6f7`); both formats stay readable in the same repo, and you can pass either to any command that takes a checkpoint ID.

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

As you work, Entire tracks which files the agent touched (and their content hashes) in local session state, alongside a local copy of the transcript; nothing is written to git until you commit. When you commit, that work is condensed into a permanent checkpoint and linked to your commit by an `Entire-Checkpoint` trailer. Secrets are redacted before the checkpoint is written (see [Security & Privacy](#security--privacy)).

Entire uses a single manual-commit strategy that keeps your git history clean:

- **No commits on your branch** — Entire never creates commits on the active branch
- **Safe on any branch** — works on main, master, and feature branches alike
- **Metadata stored separately** — session data lives in checkpoint storage, never in your branch's history

### Git Worktrees

Entire works with [git worktrees](https://git-scm.com/docs/git-worktree). Each worktree has independent session tracking, so you can run multiple AI sessions in different worktrees without conflicts.

### Concurrent Sessions

Multiple AI sessions can run on the same commit. If you start a second session while another has uncommitted work, Entire warns you and tracks them separately. Both sessions' checkpoints are preserved, and a commit condenses every session with pending work in that worktree.

## Search, Activity & the API

- **Search** — `entire search` runs hybrid (semantic + keyword) search over checkpoints, commits, and sessions, scoped to the current repo or `--all-repos`. `--json --compact` returns a trimmed shape suited to agents, and `entire checkpoint explain <id>` fetches the full detail of a hit.
- **Activity and dispatches** — `entire activity` shows your activity overview, repository breakdown, and recent sessions from entire.com. `entire dispatch` writes a summary of recent agent work; `--local` generates it with your locally installed agent CLI instead of the Entire server.
- **Raw API access** — `entire api <path>` makes an authenticated request to an Entire API and prints the JSON response, like `gh api`. It attaches your token and picks the host: `--to core` (the default) reaches the control plane (orgs, projects, repos, mirrors, clusters), `--to cell` reaches your home data cell (activity, repo aggregates), and `--jurisdiction <slug>` reaches another jurisdiction's cell. `{owner}`, `{repo}`, and `{repo_id}` in the path are filled from the current repository. These endpoints are internal and can change without notice; where a purpose-built command exists, prefer it.

```bash
entire api /api/v1/clusters
entire api --to cell "/api/v1/me/recap?repo={repo_id}"
entire api /api/v1/me
```

See [CLI and entire.com](https://docs.entire.com/platforms/cli-and-entire-com) for how the CLI and the web app fit together.

## Headless & CI Authentication

By default `entire login` opens a browser to sign in and stores tokens in the OS keyring (macOS Keychain, Linux Secret Service, Windows Credential Manager). Machines without a usable browser or keyring — headless servers, containers, minimal VMs, CI runners — have two supported paths:

### Interactive login on a headless machine

Sign-in itself already handles this: with no interactive terminal, over SSH, or on a Linux or BSD machine with no graphical display, `entire login` switches to the device-code flow on its own and prints an approval URL you can open on any machine. `entire login --device` forces that flow explicitly. Only token *storage* needs an override — use the file-backed store:

```bash
ENTIRE_TOKEN_STORE=file entire login
```

Tokens are written with `0600` permissions to `tokens.json` in your Entire config directory (`~/.config/entire` by default). Override the location with `ENTIRE_TOKEN_STORE_PATH`. Set `ENTIRE_TOKEN_STORE=file` persistently (e.g. in your shell profile) so later commands read from the same store.

### Non-interactive automation (CI, workload identity)

Skip login and storage entirely by injecting a token per invocation:

```bash
ENTIRE_TOKEN=<login-or-sa-session-JWT> entire ...
```

`ENTIRE_TOKEN` bypasses stored credentials; the CLI derives the control-plane endpoint from the token itself. Nothing is written to disk. This is the right path for CI pipelines and service accounts.

> **Seeing `save login` / `failed to unlock correct collection` errors from `entire login`?** That's the OS keyring being unavailable — use one of the two paths above.

## Commands Reference

Descriptions below are the commands' own summaries. `entire help` always reflects the installed binary; `entire agent-help` is the machine-readable version agents read. The [CLI reference](https://docs.entire.com/cli-reference) documents every command.

### Setup

| Command            | Description                                                                                |
| ------------------ | ------------------------------------------------------------------------------------------ |
| `entire enable`    | Enable Entire in current repository                                                        |
| `entire disable`   | Disable Entire in current repository                                                       |
| `entire status`    | Show Entire status (`--json` for structured output)                                        |
| `entire agent`     | Manage agent integrations (`add`, `remove`, `list`)                                        |
| `entire configure` | Update Entire settings in the current repository                                           |
| `entire doctor`    | Diagnose and fix session issues (`trace`, `logs`, `bundle`, `migrate-checkpoints`)         |
| `entire clean`     | Clean up Entire session data (`--all` for repo-wide cleanup)                               |
| `entire plugin`    | Manage Entire plugins (`install`, `list`, `upgrade`, `search`, `browse`, `info`, `doctor`, `index`, `remove`; see [Plugins](#plugins)) |

### Sessions & Checkpoints

| Command                       | Description                                                                              |
| ----------------------------- | ---------------------------------------------------------------------------------------- |
| `entire session`              | Manage agent sessions (`list`, `info`, `current`, `stop`, `attach`, `adopt`, `resume`, `tokens`) |
| `entire session resume`       | Resume a stopped session — interactive picker, or by branch                               |
| `entire checkpoint`           | Inspect and search checkpoints (`list`, `explain`, `search`, `tokens`, `delete`)          |
| `entire checkpoint explain`   | Explain a checkpoint, commit, or session                                                 |
| `entire search`               | Search checkpoints, commits, and sessions using semantic and keyword matching            |
| `entire activity`             | Show your activity overview                                                              |
| `entire recap`                | Summarize recent checkpoint activity                                                     |
| `entire dispatch`             | Generate a dispatch summarizing recent agent work                                         |

### Account

| Command          | Description                                                                          |
| ---------------- | ------------------------------------------------------------------------------------ |
| `entire login`   | Log in to Entire (browser by default; `--device` for the device-code flow)            |
| `entire logout`  | Log out of Entire                                                                    |
| `entire auth`    | Manage authentication (`status`, `contexts`, `switch`, `token`, `login`, `logout`)   |

Every command accepts `--context <name>` to act as a different saved login for that one invocation; `entire auth contexts` lists them.

### Repositories & Hosting

| Command               | Description                                                                       |
| --------------------- | --------------------------------------------------------------------------------- |
| `entire cluster`      | Show the Entire clusters you can place projects and repos on (`list`)              |
| `entire org`          | Manage Entire organizations (`create`, `list`, `get`, `delete`, `grant`, `invite`) |
| `entire project`      | Manage Entire projects (`create`, `list`, `get`, `delete`, `grant`)                |
| `entire repo`         | Manage Entire repositories (`create`, `list`, `view`, `edit`, `delete`, `clone`, `mirror`, `remote`, `visibility`, `protection`, `grant`) |
| `entire repo mirror`  | Manage where a repository is mirrored across Entire clusters (`add`, `list`, `remove`, `resync`, `detach`) |
| `entire api`          | Make an authenticated request to an Entire API and print the response              |

### Trails

| Command                | Description                                                                       |
| ---------------------- | --------------------------------------------------------------------------------- |
| `entire trail`         | Manage trails across repositories and branches (`create`, `list`, `show`, `update`, `link`, `unlink`, `checkout`, `resume`, `watch`, `comment`, `approve`, `approvals`, `merge`) |
| `entire trail finding` | Manage a trail's agent findings (`list`, `show`, `add`, `apply`, `resolve`, `dismiss`, `reopen`, `update`) |
| `entire runner`        | Set up and tune trail runners for this repository (`setup`; experimental)          |

### Other

| Command             | Description                                                                    |
| ------------------- | ------------------------------------------------------------------------------ |
| `entire agent-help` | Machine-readable usage for coding agents (always matches the installed CLI)     |
| `entire labs`       | Explore experimental Entire workflows                                          |
| `entire version`    | Show build information                                                         |

### Experimental Commands

These are visible in developer and nightly builds and hidden in stable releases, but always runnable in every build. Run `entire labs` to discover them.

| Command              | Description                                              |
| -------------------- | -------------------------------------------------------- |
| `entire review`      | Run a multi-agent review against a branch                |
| `entire tokens`      | Analyze token usage across sessions and checkpoints       |
| `entire blame`       | Show which lines came from Entire checkpoints             |
| `entire why`         | Show why a line exists                                   |
| `entire experts`     | Rank agent provenance for code scopes                    |
| `entire import`      | Import pre-existing agent history into Entire            |
| `entire runner`      | Set up and tune trail runners for this repository        |

`entire blame <file>` shows which current file lines came from an Entire checkpoint (`--long` for the full agent, model, author, and session table), and `entire why <file>:<line>` jumps from a specific line back to the prompt, session, and checkpoint that created it.

### `entire enable` Flags

| Flag                                        | Description                                                                                                       |
| ------------------------------------------- | ----------------------------------------------------------------------------------------------------------------- |
| `--agent <name>`                            | Agent to set up hooks for: `antigravity`, `claude-code`, `codex`, `copilot-cli`, `cursor`, `factoryai-droid`, `opencode`, `pi` (external agents on `$PATH` also work). Enables non-interactive mode |
| `--yes`, `-y`                               | Accept all defaults without prompting                                                                             |
| `--force`, `-f`                             | Force reinstall hooks (removes existing Entire hooks first)                                                       |
| `--checkpoint-backend <refs\|branch>`       | Checkpoint storage backend: `refs` (default) or `branch` (legacy); see [Checkpoint Storage](#checkpoint-storage)  |
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

`entire enable` is primarily for turning Entire on. On an unconfigured repo it will also bootstrap setup. Use `entire agent` for adding or removing agents, and `entire configure` for non-agent settings.

### `entire configure`

Use `entire configure` to update non-agent settings on a repo that's already set up. Agent installation lives under `entire agent`.

Typical uses:

- Toggle telemetry
- Reinstall the Entire git hook (`--force`, `--absolute-git-hook-path`)
- Update strategy options such as `--checkpoint-remote`, `--skip-push-sessions`, or `--checkpoint-backend`
- Pick the provider, model, and timeout for `entire checkpoint explain --generate` (`--summarize-provider`, `--summarize-model`, `--summarize-timeout-seconds`)

**Examples:**

```bash
# Show help and the hint pointing to 'entire agent'
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

## Plugins

Plugins extend the CLI with new verbs: any executable named `entire-<name>` on `$PATH` runs as `entire <name>`, kubectl-style — stdio passes through, exit codes propagate, no SDK or protocol required.

```sh
entire plugin search                                    # search the plugin index
entire plugin browse                                    # browse the index interactively and install
entire plugin install run                               # install by name (index lookup)
entire plugin install https://github.com/you/entire-x   # install from any git host
entire plugin install ./dist/entire-x                   # link a local build
entire plugin list                                      # list what's installed
entire run                                              # run an installed plugin
entire plugin upgrade --all                             # update remote-installed plugins
entire plugin doctor                                    # check for broken installs and missing dependencies
entire plugin remove run
```

Remote installs are forge-agnostic: the newest stable semver tag is resolved over the git protocol (prereleases need an explicit `--pin`), and the platform's release asset is downloaded over HTTPS and verified against the release's `checksums.txt`. A release that publishes no checksums is refused unless you pass `--allow-unverified` — installing means making those bytes executable. `entire plugin doctor` re-checks installed binaries against the digests recorded at install time. Plugins can declare dependencies on other plugins in an `entire-plugin.yml`; missing ones are installed after a single confirmation.

Discovery uses a git-synced catalog, [entireio/plugin-index](https://github.com/entireio/plugin-index) by default. Organizations can point the CLI at an internal catalog with the `ENTIRE_PLUGIN_INDEX_URL` environment variable or `--index`. It is deliberately not settable from a repository's committed settings: an index-listed plugin installs without a prompt, so a checked-out repo must not be able to choose the catalog.

For the full contract — resolution rules, environment filtering, release-asset conventions, and how to author a plugin — see [External Commands](docs/architecture/external-commands.md).

## Configuration

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
| `checkpoints.primary.type`                | `git-refs`, `git-branch`                     | Checkpoint storage backend — `git-refs` is written by `entire enable`; absent means legacy `git-branch`. See [Checkpoint Storage](#checkpoint-storage) |
| `telemetry`                               | `true`, `false`                              | Send anonymous usage statistics to Posthog                                        |
| `absolute_git_hook_path`                  | `true`, `false`                              | Embed the full binary path in git hooks, for GUI git clients that don't source shell profiles |
| `commit_linking`                          | `always`, `prompt`                           | Link commits to sessions automatically, or ask each time (default `prompt`)        |
| `sign_checkpoint_commits`                 | `true`, `false`                              | Sign checkpoint commits (default: on). See [checkpoint signing](docs/architecture/checkpoint-signing.md) |
| `strategy_options.push_sessions`          | `true`, `false`                              | Auto-push checkpoint data on git push (default `true`)                            |
| `strategy_options.checkpoint_remote`      | `{"provider": "github", "repo": "org/repo"}` | Push checkpoint data to a separate repo (see below)                               |
| `strategy_options.checkpoint_push_remote` | remote name, e.g. `"upstream"`               | Pin which single remote carries checkpoint data (see below)                       |
| `strategy_options.filtered_fetches`       | `true`, `false`                              | Use `--filter=blob:none` on checkpoint fetches                                    |
| `strategy_options.checkpoint_ref_list_timeout_seconds` | seconds (default `5`, max `120`) | How long `entire checkpoint list` waits for each remote to list checkpoints that aren't local yet. Raise it if checkpoints pushed from another machine are missing because the remote answers slowly |
| `strategy_options.summarize.enabled`      | `true`, `false`                              | Auto-generate AI summaries at commit time                                         |
| `summary_generation.provider`             | e.g. `claude-code`, `codex`, `pi`            | Which agent generates summaries (defaults to Claude)                              |
| `summary_generation.model`                | provider-specific model hint                 | Model hint for summary generation (requires `provider`)                            |
| `summary_timeout_seconds`                 | seconds                                      | Hard deadline for `entire checkpoint explain --generate`. Unset or `0` means **no deadline** |
| `redaction.*`                             | nested object                                | PII redaction, custom secret patterns, and scanner engines — documented in [docs/security-and-privacy.md](docs/security-and-privacy.md) |

> **Deprecated:** the OpenAI Privacy Filter (`redaction.openai_privacy_filter`) is deprecated and will be removed in a future release. Existing configurations keep working until then, but don't enable it in new setups.

### Agent Hook Configuration

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

Agents not listed here can integrate through the [external agent protocol](docs/architecture/external-agent-protocol.md): an `entire-agent-<name>` binary on `$PATH` appears as an agent option.

### Checkpoint Remote

By default, checkpoint data rides along with your own pushes — but only to **one** remote, the elected checkpoint sync remote. Entire picks it in this order:

1. `strategy_options.checkpoint_push_remote`, if set. This is fail-closed: if it names a remote that isn't configured, checkpoints don't sync.
2. A remote captured from your own habits: the first push whose target matches the branch's declared push destination elects that remote, announces it on stderr, and carries the checkpoints. The first capture sticks.
3. `origin`
4. The sole remote, if the repo has exactly one
5. The first remote in `.git/config` order

A push to any *other* remote carries no checkpoint data — unless every push URL of that remote names the same GitHub repository as the elected one, such as `git@github.com:acme/widgets.git` and its Entire mirror `entire://<cluster>/gh/acme/widgets`. Both reach the same repository, so either push carries the checkpoints. An SSH host alias from `~/.ssh/config` (`git@github-work:acme/widgets.git`) counts as GitHub when git runs plain `ssh`; with a custom `GIT_SSH_COMMAND` or `core.sshCommand` the alias cannot be resolved, so it does not match. `entire status` shows the current destination, where it came from, and how many checkpoints are unpushed; on the git-refs backend, `entire checkpoint list --outgoing` lists them. This matters if you push code to several remotes: checkpoints go to exactly one of them.

When a repository has several remotes that could receive checkpoints, interactive first-time `entire enable` asks which one to use, after agent selection and before any hooks or settings are written. Re-running `entire enable` in an enabled repository does not ask again. Choosing a remote saves `strategy_options.checkpoint_push_remote` in `.entire/settings.local.json`, so teammates do not inherit a remote name specific to your clone; keeping the current destination writes nothing.

To select a remote without the picker:

```bash
entire enable --yes --checkpoint-push-remote fork
```

The remote must already exist, and this is also how to change the destination later or repair a saved selection that names a missing remote. An explicit flag pins the named remote, even if it is currently selected automatically. `--yes` alone does not change the checkpoint destination. Every run ends by naming where checkpoints will be uploaded, followed by a short privacy notice. When you chose a destination, it also says how; a saved destination that is unusable is reported instead of the notice, unless checkpoints still upload to a dedicated `checkpoint_remote`. Selecting a destination does not re-enable disabled checkpoint pushing, upload existing checkpoints immediately, or move or delete checkpoint history from other remotes.

If instead you want checkpoint data in a separate repo (e.g., a private repo for a public project), configure `checkpoint_remote` with a structured provider and repo. A dedicated `checkpoint_remote` is addressed directly and is exempt from the single-remote election above:

```json
{
  "strategy_options": {
    "checkpoint_remote": {
      "provider": "github",
      "repo": "myorg/checkpoints-private"
    }
  }
}
```

Or via the CLI:

```bash
entire enable --checkpoint-remote github:myorg/checkpoints-private
```

Entire derives the git URL automatically using the same protocol (SSH or HTTPS) as your push remote. It will:

- Fetch existing checkpoint data locally if the remote has it and you don't (one-time)
- Push checkpoint data to the checkpoint repo instead of your default push remote
- Ignore the setting if it looks inherited rather than yours, and fall back to `origin` (or, failing that, the remote you are pushing to). `checkpoint_remote` is normally committed in `.entire/settings.json`, so cloning or forking a project inherits it — without this, a contributor's session data would be pushed into the upstream project's checkpoint repo. A setting is treated as yours when it lives in the gitignored `.entire/settings.local.json`, or when your `origin` **and every push URL** of the remote you are pushing to are owned by the same account or org as the checkpoint repo. Requiring the push destination too covers contributors who cloned the upstream repo and added their own fork, where `origin` belongs to the upstream project rather than to them; requiring every push URL covers mirror-style remotes that fan out to several repositories
- If your checkpoint repo is owned by a different account or org than `origin`, configure it in `.entire/settings.local.json` so it is always honored
- If the remote is unreachable, warn and continue without blocking your main push

#### `ENTIRE_CHECKPOINT_TOKEN`

`ENTIRE_CHECKPOINT_TOKEN` allows you to provide a dedicated token for checkpoint repository operations, without modifying the credentials used for your primary repository.

When this environment variable is set, Entire behaves as follows:

- Injects the token into HTTPS Git operations used for checkpoint fetch and push
- If `checkpoint_remote` is configured:
  - Prefers an HTTPS URL for the checkpoint remote when a token is present, even if the repository’s `origin` uses SSH
- If `checkpoint_remote` is not configured:
  - Falls back to using the default `origin` remote
- If `checkpoint_remote` configuration cannot be loaded:
  - Falls back to `origin`
  - If `origin` is a valid SSH or HTTPS Git remote, Entire converts it to an HTTPS URL to enable token-based authentication

### Auto-Summarization

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

**Which agent writes them.** By default Claude Code (`claude` on your `PATH`, model `sonnet`). Set a different one with `summary_generation.provider` — `antigravity`, `claude-code`, `codex`, `copilot-cli`, `cursor`, `opencode`, or `pi`, plus an optional `summary_generation.model` hint:

```bash
entire configure --summarize-provider codex
```

`factoryai-droid` cannot generate summaries. Whichever provider you pick must be installed and authenticated.

**Requirements:**

- The configured provider's CLI installed and authenticated
- Summary generation is non-blocking: failures are logged but don't prevent commits

### Settings Priority

Local settings override project settings field-by-field. `entire status --detailed` shows the state of each settings file.

Two exceptions to field-by-field merging:

- The `checkpoints` block is **replaced wholesale** by the local one, not deep-merged — it selects a backend, so a half-merged block would be meaningless.
- A `settings.local.json` that is **tracked in git** is ignored entirely, and Entire tells you to `git rm --cached` it. `.gitignore` doesn't apply to an already-tracked path, so a committed local file would otherwise override project settings for everyone who clones.

### Agent-Specific Steps & Limitations

- Codex hooks are enabled by default (codex-cli 0.124.0+), so enabling Entire for Codex only installs `.codex/hooks.json` — no `config.toml` is needed and Entire never creates one. If an older Entire version left a `.codex/config.toml` behind and your repo lives inside `~/.codex/agents`, delete that file to stop Codex's "malformed agent role definition" startup warning.
- Entire supports Cursor IDE and Cursor Agent CLI tool. Commands (`doctor`, `status` etc.) work the same as all other agents.
- Entire supports Copilot CLI, but not Copilot in VS Code, in other IDEs, or on github.com.
- Entire supports Pi coding agent (Preview). Pi uses a TypeScript extension instead of a JSON hook config. Subagent capture is not currently available.

Per-agent setup guides are at [docs.entire.com/agents](https://docs.entire.com/agents/overview).

## Security & Privacy

**Your session transcripts are stored in your git repository**, in the [checkpoint storage](#checkpoint-storage) described above. If your repository is public, this data is visible to anyone.

Entire automatically redacts detected secrets (API keys, tokens, credentials) from transcripts and metadata before writing a checkpoint, but redaction is best effort, not bulletproof. Review checkpoints with `entire checkpoint explain` before you push.

To keep transcripts out of a public repository, upload checkpoints to a separate private repo with [`checkpoint_remote`](#checkpoint-remote) (`entire configure --local --checkpoint-remote github:<owner>/<repo>`), or stop uploading them with `entire configure --local --skip-push-sessions`. `--local` makes the setting take effect for your clone: a project `checkpoint_remote` owned by a different account than `origin` is ignored, and a local `push_sessions` overrides the project one. `entire enable` names the checkpoint destination every time it runs. `entire checkpoint delete <id>` deletes a checkpoint locally and from every checkpoint remote that holds it, but it is not a full purge: on the legacy `entire/checkpoints/v1` branch it removes the checkpoint from the branch tip only, so the branch's history still contains it, and other checkpoints from the same session each carry that session's full transcript and are not deleted.

Older Entire versions also kept local `entire/<commit>-<worktree>` shadow branches whose code-file snapshots were raw blobs of your working tree. Current versions no longer create them, but do not delete leftover ones automatically: `entire doctor` reports them and `entire doctor --force` deletes them, leaving pending agent work alone (plain `entire clean` would also clear the session state that records it). `entire disable --uninstall` deletes the `entire/<commit>-<worktree>` form too, and lists any oldest-form `entire/<commit>` branches without deleting them, since a branch of yours named after a short SHA looks the same; `entire clean --all` covers that form after you confirm. See [docs/security-and-privacy.md](docs/security-and-privacy.md) for the full picture, including the configurable scanner layers and opt-in PII redaction.

## Troubleshooting

### Common Issues

| Issue                              | Solution                                                          |
| ---------------------------------- | ----------------------------------------------------------------- |
| "Not a git repository"             | Navigate to a Git repository first                                |
| "Entire is disabled"               | Run `entire enable`                                               |
| Agent hooks outdated after a CLI upgrade | Run `entire agent add <name>`; add `--force` if that does not fix them |
| A session stuck ACTIVE, or leftover session state | Run `entire doctor`, then `entire clean --force` if it persists |
| Anything else                      | Run `entire doctor`; `entire doctor bundle` produces a redacted diagnostic bundle for a bug report |

### Debug Mode

```
# Via environment variable
ENTIRE_LOG_LEVEL=debug entire status

# Or via settings.local.json
{
  "log_level": "debug"
}
```

### Cleaning Up State

```
# Clean session data for current commit
entire clean --force

# Clean all orphaned data across the repository
entire clean --all --force
```

### Accessibility

For screen reader users, enable accessible mode:

```
export ACCESSIBLE=1
entire enable
```

This uses simpler text prompts instead of interactive TUI elements.

## Development

This project uses [mise](https://mise.jdx.dev/) for task automation and dependency management. Contributor guidelines are in [CONTRIBUTING.md](CONTRIBUTING.md), and [docs/first-time-contributors.md](docs/first-time-contributors.md) is a good starting point.

### Prerequisites

- [mise](https://mise.jdx.dev/) - Install with `curl https://mise.run | sh`

### Getting Started

```
# Clone the repository
git clone https://github.com/entireio/cli.git
cd cli

# Trust the mise configuration (required on first setup)
mise trust

# Install dependencies (including Go)
mise install

# Build the CLI
mise run build
```

### Dev Container

The repo includes a `.devcontainer/` configuration that installs the system packages used by local development and CI (`git`, `tmux`, `gnome-keyring`, etc) and then bootstraps the repo's `mise` toolchain.

Open the folder in a Dev Container, or start it from the `devcontainer` CLI as follows:

```bash
devcontainer up --workspace-folder .
devcontainer exec --workspace-folder . bash -lc '.devcontainer/run-with-keyring.sh'
```

The container's `postCreateCommand` runs `.devcontainer/post-create.sh`, which does `mise trust --yes && mise install`, so Go, `golangci-lint`, `gotestsum`, `shellcheck`, and the canary E2E helper binaries are ready after creation. Use `.devcontainer/run-with-keyring.sh <command>` for commands that touch the Linux keyring, including `mise run test:ci`.

If `ENTIRE_DEVCONTAINER_KEYRING_PASSWORD` is set in the environment, `.devcontainer/run-with-keyring.sh` uses that value to unlock the keyring non-interactively. If it is unset, the script generates a random password for the session automatically.

### Common Tasks

```
# Run tests
mise run test

# Run integration tests
mise run test:integration

# Run all tests: unit + integration under -race, then the E2E canary
mise run test:ci

# Lint the code
mise run lint

# Format the code
mise run fmt

# Format, lint, and run all CI tests (before every commit)
mise run check
```

### Local Device Auth Testing

If you're working on the CLI login flow against a locally running Entire API, use the smoke script. It defaults to `http://localhost:8787` for both the data API and the login server, and passes the hidden `--insecure-http-auth` flag that a plain-HTTP login server requires:

```bash
./scripts/local-device-auth-smoke.sh
```

Override either endpoint with `ENTIRE_API_BASE_URL` and `ENTIRE_LOGIN_SERVER`:

```bash
ENTIRE_LOGIN_SERVER=http://localhost:8180 ./scripts/local-device-auth-smoke.sh
```

The script starts a login, opens the approval URL, waits for the CLI to finish, and then verifies a matching context was written to `contexts.json`.

To drive the flow by hand, or to run the focused integration coverage:

```bash
# Login against a local server (--device forces the device-code flow)
go run ./cmd/entire login --server http://localhost:8787 --insecure-http-auth --device

# Focused integration coverage for login
go test -tags=integration ./cmd/entire/cli/integration_test -run TestLogin
```

## Getting Help

```
entire --help              # General help
entire <command> --help    # Command-specific help
```

- **Documentation:** https://docs.entire.com
- **GitHub Issues:** Report bugs or request features at https://github.com/entireio/cli/issues
- **Contributing:** See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines

## License

MIT License - see [LICENSE](LICENSE) for details.
