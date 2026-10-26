# Where Checkpoints Are Uploaded

Checkpoints are uploaded to one place, the **checkpoint destination**. It is either:

- **One of your existing remotes** — chosen automatically, or pinned with `--checkpoint-push-remote <remote-name>`. Checkpoints ride along with your own `git push` to that remote.
- **A separate checkpoint repository** — set with `--checkpoint-remote github:<owner>/<repo>` (or `gitlab:`), for example a private repository for a public project.

The two flags differ by one word but mean different things: `--checkpoint-push-remote` picks which of your remotes receives checkpoints, while `--checkpoint-remote` sends them to a different repository altogether.

`entire status` shows the current destination, where it came from, and how many checkpoints are unpushed; on the git-refs backend, `entire checkpoint list --outgoing` lists them. Every `entire enable` run ends by naming the destination.

## Which remote receives checkpoints

If you push code to several remotes, checkpoints go to exactly one of them. Entire picks it in this order:

1. `strategy_options.checkpoint_push_remote`, if set. This is fail-closed: if it names a remote that isn't configured, checkpoints don't sync.
2. The remote of your first push whose target matches the branch's configured push destination. Entire announces the choice on stderr and keeps it.
3. `origin`
4. The sole remote, if the repository has exactly one
5. The first remote in `.git/config` order

A push to any *other* remote carries no checkpoint data — unless every push URL of that remote names the same GitHub repository as the chosen one, such as `git@github.com:acme/widgets.git` and its Entire mirror `entire://<cluster>/gh/acme/widgets`. Both reach the same repository, so either push carries the checkpoints.

### Choosing the remote

When a repository has several remotes that could receive checkpoints, interactive first-time `entire enable` asks which one to use, after agent selection and before any hooks or settings are written. Re-running `entire enable` in an enabled repository does not ask again. Choosing a remote saves `strategy_options.checkpoint_push_remote` in `.entire/settings.local.json`, so teammates do not inherit a remote name specific to your clone; keeping the current destination writes nothing.

To select a remote without the picker:

```bash
entire enable --yes --checkpoint-push-remote fork
```

The remote must already exist, and this is also how to change the destination later or repair a saved selection that names a missing remote. An explicit flag pins the named remote, even if it is currently selected automatically. `--yes` alone does not change the destination. Selecting a destination does not re-enable disabled checkpoint pushing, upload existing checkpoints immediately, or move or delete checkpoint history from other remotes.

### Edge cases

- An SSH host alias from `~/.ssh/config` (`git@github-work:acme/widgets.git`) counts as GitHub when git runs plain `ssh`; with a custom `GIT_SSH_COMMAND` or `core.sshCommand` the alias cannot be resolved, so it does not match.
- When you chose a destination, `entire enable` also says how. A saved destination that is unusable is reported instead of the privacy notice, unless checkpoints still upload to a dedicated `checkpoint_remote`.

## A separate checkpoint repository

To keep checkpoint data in its own repository, configure `checkpoint_remote` with a provider and repository. It is addressed directly and is exempt from the remote selection above:

```bash
entire enable --checkpoint-remote github:myorg/checkpoints-private
```

or in settings:

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

Entire derives the git URL using the same protocol (SSH or HTTPS) as your push remote. It will:

- Push checkpoint data to the checkpoint repository instead of your push remote
- On the legacy branch backend, fetch existing checkpoint data locally if the remote has it and you don't (one-time); the default `git-refs` backend fetches checkpoints on demand instead
- If the remote is unreachable, warn and continue without blocking your main push

### Inherited settings are ignored

`checkpoint_remote` is normally committed in `.entire/settings.json`, so cloning or forking a project inherits it. To keep a contributor's session data out of the upstream project's checkpoint repository, Entire ignores the setting when it looks inherited rather than yours, and pushes checkpoints to the remote you are pushing to instead (still subject to the remote selection above).

A setting is treated as yours when it lives in the gitignored `.entire/settings.local.json`, or when your `origin` **and every push URL** of the remote you are pushing to are owned by the same account or org as the checkpoint repository. Requiring the push destination too covers contributors who cloned the upstream repository and added their own fork, where `origin` belongs to the upstream project rather than to them; requiring every push URL covers mirror-style remotes that fan out to several repositories. If your checkpoint repository is owned by a different account or org than `origin`, configure it in `.entire/settings.local.json` so it is always honored.

## `ENTIRE_CHECKPOINT_TOKEN`

`ENTIRE_CHECKPOINT_TOKEN` provides a dedicated token for checkpoint repository operations, without changing the credentials used for your primary repository. When it is set, Entire:

- Injects the token into HTTPS Git operations used for checkpoint fetch and push
- Rewrites SSH remotes (including `origin`) to HTTPS for checkpoint operations so the token can authenticate, and prefers an HTTPS URL for a configured `checkpoint_remote`
- Without a `checkpoint_remote`, uses the remote chosen as described above
