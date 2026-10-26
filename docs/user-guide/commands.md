# Commands Reference

Descriptions below are the commands' own summaries. `entire help` always reflects the installed binary; `entire agent-help` is the machine-readable version agents read. The [CLI reference](https://docs.entire.com/cli-reference) documents every command.

## Setup

| Command            | Description                                                                                |
| ------------------ | ------------------------------------------------------------------------------------------ |
| `entire enable`    | Enable Entire in current repository                                                        |
| `entire disable`   | Disable Entire in current repository                                                       |
| `entire status`    | Show Entire status (`--json` for structured output)                                        |
| `entire agent`     | Manage agent integrations (`add`, `remove`, `list`)                                        |
| `entire configure` | Update Entire settings in the current repository                                           |
| `entire doctor`    | Diagnose and fix session issues (`trace`, `logs`, `bundle`, `migrate-checkpoints`)         |
| `entire clean`     | Clean up Entire session data (`--all` for repo-wide cleanup)                               |
| `entire plugin`    | Manage Entire plugins (`install`, `list`, `upgrade`, `search`, `browse`, `info`, `doctor`, `index`, `remove`; see [Plugins](plugins.md)) |

## Sessions & Checkpoints

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

## Account

| Command          | Description                                                                          |
| ---------------- | ------------------------------------------------------------------------------------ |
| `entire login`   | Log in to Entire (browser by default; `--device` for the device-code flow)            |
| `entire logout`  | Log out of Entire                                                                    |
| `entire auth`    | Manage authentication (`status`, `contexts`, `switch`, `token`, `login`, `logout`)   |

Every command accepts `--context <name>` to act as a different saved login for that one invocation; `entire auth contexts` lists them.

## Repositories & Hosting

| Command               | Description                                                                       |
| --------------------- | --------------------------------------------------------------------------------- |
| `entire cluster`      | Show the Entire clusters you can place projects and repos on (`list`)              |
| `entire org`          | Manage Entire organizations (`create`, `list`, `get`, `delete`, `grant`, `invite`) |
| `entire project`      | Manage Entire projects (`create`, `list`, `get`, `delete`, `grant`)                |
| `entire repo`         | Manage Entire repositories (`create`, `list`, `view`, `edit`, `delete`, `clone`, `mirror`, `remote`, `visibility`, `protection`, `grant`) |
| `entire repo mirror`  | Manage where a repository is mirrored across Entire clusters (`add`, `list`, `remove`, `resync`, `detach`) |
| `entire api`          | Make an authenticated request to an Entire API and print the response              |

## Trails

| Command                | Description                                                                       |
| ---------------------- | --------------------------------------------------------------------------------- |
| `entire trail`         | Manage Trails across repositories and branches (`create`, `list`, `show`, `update`, `link`, `unlink`, `checkout`, `resume`, `watch`, `comment`, `approve`, `approvals`, `merge`) |
| `entire trail finding` | Manage a Trail's agent findings (`list`, `show`, `add`, `apply`, `resolve`, `dismiss`, `reopen`, `update`) |
| `entire runner`        | Set up and tune Trail runners for this repository (`setup`; experimental)          |

## Other

| Command             | Description                                                                    |
| ------------------- | ------------------------------------------------------------------------------ |
| `entire agent-help` | Machine-readable usage for coding agents (always matches the installed CLI)     |
| `entire labs`       | Explore experimental Entire workflows                                          |
| `entire version`    | Show build information                                                         |

## Experimental Commands

These are visible in developer and nightly builds and hidden in stable releases, but always runnable in every build. Run `entire labs` to discover most of them; `entire runner` appears in `entire help` on those builds.

| Command              | Description                                              |
| -------------------- | -------------------------------------------------------- |
| `entire review`      | Run a multi-agent review against a branch                |
| `entire tokens`      | Analyze token usage across sessions and checkpoints       |
| `entire blame`       | Show which lines came from Entire checkpoints             |
| `entire why`         | Show why a line exists                                   |
| `entire experts`     | Rank agent provenance for code scopes                    |
| `entire import`      | Import pre-existing agent history into Entire            |
| `entire runner`      | Set up and tune Trail runners for this repository        |

`entire blame <file>` shows which current file lines came from an Entire checkpoint (`--long` for the full agent, model, author, and session table), and `entire why <file>:<line>` jumps from a specific line back to the prompt, session, and checkpoint that created it.
