# Plugins

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

For the full contract — resolution rules, environment filtering, release-asset conventions, and how to author a plugin — see [External Commands](../architecture/external-commands.md).
