# Repositories

How to create, mirror, clone, and manage repositories on Entire from the CLI. The README's [Repositories & Git Hosting](../../README.md#repositories--git-hosting) section defines regions, clusters, projects, and mirrors.

Repository references name their forge: `/et/<project>/<repo>` for an Entire-native repository, `/gh/<owner>/<repo>` for a GitHub mirror, or a full `entire://<cluster>/…` clone URL.

## Regions and clusters

```bash
entire cluster list                          # clusters you can place projects and repos on
entire org create <name> --region <slug>     # jurisdiction such as us or eu
entire project create <name> --owner <org> --region <slug>
```

Organizations and projects carry a **region** (jurisdiction) for data residency. Individual repositories are placed on **clusters** — hosts such as `aws-us-east-2.entire.io` or `aws-eu-central-1.entire.io` — and a repository can have copies on several of them.

## Create an Entire-native repository

```bash
entire repo create web --project acme                       # private by default
entire repo create web --project acme --visibility public   # readable by any signed-in Entire user
entire repo create                                          # wizard: project, name, visibility, options
```

`entire repo create` waits for provisioning to finish (bounded by `--wait-timeout`, or skip with `--no-wait`) and supports `--object-format sha256`. To place copies of a native repository on more clusters: `entire repo mirror add /et/acme/web --cluster aws-eu-central-1.entire.io`.

## Mirror a GitHub repository

Mirroring needs the [Entire GitHub App](https://docs.entire.com/guides/repositories/github-app-access) installed for the repository, and mirror access follows GitHub collaborator permissions.

```bash
entire repo mirror list                      # your mirrors, and GitHub repos you could onboard
entire repo mirror add                       # wizard: pick repos, pick regions
entire repo mirror add /gh/acme/web --cluster aws-us-east-2.entire.io,aws-eu-central-1.entire.io
entire repo mirror resync /gh/acme/web       # catch a mirror up with GitHub now
entire repo mirror remove /gh/acme/web --cluster aws-eu-central-1.entire.io
```

A mirror keeps GitHub as the source of truth: clones and fetches come from Entire, and pushes through a mirror are forwarded to GitHub, where your GitHub permissions, branch protection, and required checks still apply. `entire repo mirror detach /gh/acme/web --into /et/acme/web` converts a mirror into an Entire-native repository when you are ready to move off GitHub (`--dry-run` lists the preconditions first).

## Clone and connect existing checkouts

```bash
entire repo clone /et/acme/web                    # pick a cluster when there are several
entire repo clone /gh/acme/web --nearest          # time each cluster and use the fastest
entire repo clone /gh/acme/web --cluster aws-eu-central-1.entire.io

entire repo remote add entire                     # add Entire alongside your existing origin
entire repo remote add origin --override          # or route origin itself through Entire
```

Clones use `entire://` URLs, resolved by the `git-remote-entire` helper that ships with the packaged installs. After that, it is plain Git: `git fetch`, `git pull`, and `git push` work as usual.


## Unmirrored branches

On a GitHub mirror, branches named `entire/unmirrored/<name>` are never forwarded to GitHub. They stay on the Entire cluster that received the push, so they are not subject to GitHub's push rate limits and keep working during a GitHub outage. Push the same commits to a branch without the prefix when you want them on GitHub. Entire does not currently back up unmirrored branches, and they are not visible from mirrors in other regions.

## Manage repositories and access

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
