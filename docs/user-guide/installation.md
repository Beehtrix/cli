# Installing the Entire CLI

## Install

### macOS and Linux

With Homebrew:

```bash
brew install --cask entireio/tap/entire
```

Use the fully-qualified cask name (`entireio/tap/entire`, not `entire`). Homebrew 6 requires third-party taps to be trusted before it will evaluate them, and a fully-qualified name taps and trusts just that one cask, so no separate `brew tap` / `brew trust` step is needed. Requires Homebrew 6.0.10 or newer.

Or with the install script:

```bash
curl -fsSL https://entire.com/install.sh | bash
```

### Windows

With Windows PowerShell 5.1 or later:

```powershell
irm https://entire.com/install.ps1 | iex
```

For stable releases, the PowerShell installer uses Scoop when it is available, adding the Entire bucket if needed. Without Scoop — and for nightly releases — it verifies the release checksum and installs both `entire.exe` and `git-remote-entire.exe` to `%USERPROFILE%\.local\bin`, adding that directory to your user `PATH` if needed.

Or install the stable release with Scoop directly:

```powershell
scoop bucket add entire https://github.com/entireio/scoop-bucket.git
scoop install entire/entire
```

### Migrating an old `cli` Scoop install (package rename)

The Scoop package was renamed from `cli` to `entire`. If your install is still registered as the old `cli` package, run the migration below. It installs the new package before removing the old one, so the old install is only removed once the new one is in place (`scoop reset` re-links the shared `entire.exe` and `git-remote-entire.exe` shims). Run it where `entire` is **not** running — a live `entire.exe` locks its own shim, so Scoop can't relink or uninstall it mid-run:

```powershell
cmd.exe /D /C "scoop install entire/entire && scoop uninstall entire/cli && scoop reset entire"
```

If the first step fails with "couldn't find manifest", your bucket clone predates the renamed package — run `scoop update` to refresh it, then retry the command above. Nothing is removed until the install succeeds, so a failed attempt leaves your existing install working.


### Go (development/manual setup)

Requires Go 1.27.3 or later (the packaged installs bundle their own runtime).

```bash
go install github.com/entireio/cli/cmd/entire@latest

# The git remote helper that resolves entire:// URLs, needed for `entire repo clone`
# and `git clone entire://…`. The packaged installs above bundle it.
go install github.com/entireio/cli/cmd/git-remote-entire@latest

# Add Go binaries to PATH (add to ~/.zshrc or ~/.bashrc if not already configured)
export PATH="$HOME/go/bin:$PATH"
```

Install both, or just `entire` if you never clone over `entire://`. Git finds the helper by name on `$PATH`, which is what `go install` produces, so nothing else needs configuring. Like git itself, the helper honours `GIT_HTTP_USER_AGENT` for the HTTP `User-Agent` it sends.

A `go install` of a release tag behaves like that release: [experimental commands](commands.md#experimental-commands) stay hidden in `entire help`. Builds that do not resolve to a stable version, such as nightlies and local builds, show them.

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

Release artifacts from v0.12.0 on are published to `install.entire.com` as well as to GitHub, and `install.sh` downloads from that origin first. The `https://entire.com/install.sh` address itself still redirects to GitHub, so during an outage fetch the installer from the mirror, or download an archive directly:

```bash
curl -fsSL https://install.entire.com/installers/install.sh | bash
curl -fsSL https://install.entire.com/v0.12.1/entire_darwin_arm64.tar.gz | tar -xz
```

Homebrew and Scoop still resolve their manifests from GitHub, so use one of the commands above (or `go install github.com/entireio/cli/cmd/entire@v0.12.1` with an explicit version, which is served from the Go module proxy) while GitHub is unavailable. See [Release Origins](../architecture/release-origins.md).
