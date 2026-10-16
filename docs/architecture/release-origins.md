# Release Origins

A released CLI must stay installable and updatable while GitHub is down.

Until this change every path to the binary ran through github.com — the
installer script, the version lookup, and the bytes. This document describes the
second origin, what still depends on GitHub, and how to turn the origin on.

## Why a second origin, read first

The CLI reads our own origin **before** GitHub, and falls back to GitHub
automatically. Reading it second would have been lower-risk on day one and worse
every day after: a fallback that only runs during an incident is a fallback
nobody knows works. Reading it first means every install and every update check
exercises the path, so a broken origin shows up on an ordinary Tuesday instead of
during an outage.

The fallback direction matters too. If our origin is unreachable, installs and
updates degrade to exactly the previous behaviour rather than breaking.

## The two origins

Both lay artifacts out identically, which is what lets one URL template cover
both:

| | Our origin | GitHub |
| --- | --- | --- |
| Archives | `https://install.entire.com/v0.10.0/entire_darwin_arm64.tar.gz` | `https://github.com/entireio/cli/releases/download/v0.10.0/entire_darwin_arm64.tar.gz` |
| Checksums | `https://install.entire.com/v0.10.0/checksums.txt` | `…/v0.10.0/checksums.txt` |
| Current version | `https://install.entire.com/channels/stable.json` | `https://api.github.com/repos/entireio/cli/releases/latest` |

### The channel pointer

`channels/stable.json` and `channels/nightly.json` answer one question — what is
the current version on this channel:

```json
{
  "channel": "stable",
  "tag": "v0.10.0",
  "version": "0.10.0",
  "published_at": "2026-08-12T19:07:28Z"
}
```

It deliberately carries **no checksums**. `checksums.txt`, which GoReleaser
generates and mirrors next to the archives, stays the single integrity source;
two files listing the same hashes are two files that can disagree.

Reading a pointer instead of the releases API also retires a live problem:
unauthenticated `api.github.com` allows 60 requests per hour per IP, which a
shared office or CI egress address can exhaust on its own.

Cache headers differ by what the object is. Anything under `v<version>/` never
changes once published and is served `immutable` for a year. The pointers change
on every release and get `max-age=60`.

## Who reads what

- **`scripts/install.sh`** — walks the origins for both version discovery and
  the download. `--version X.Y.Z` pins a version and skips discovery entirely;
  `ENTIRE_INSTALL_BASE_URL` replaces the origin list with one entry, because
  someone pinning a mirror wants that mirror, not a silent fall back that hides
  whether it worked.
- **`cmd/entire/cli/versioncheck`** — `versionOrigins` is the ordered chain,
  `fetchLatestForChannel` walks it. `httpTimeout` (2s) bounds the *whole* chain,
  not each origin, so adding an origin costs no extra worst-case latency on a
  path that runs synchronously after every command.
- **Homebrew cask and the Scoop manifest** — still point at GitHub. See
  "Remaining work".

Two rules the chain borrows from `strategy.CheckpointReadRemotesWithElection`
and `checkpoint/remote/checkpoint_ref.go`, the closest precedents in this repo:

1. **Fail open.** Every origin failing is reported as one error, never as "you
   are up to date" or "there is nothing to install".
2. **A definite bad answer aborts; an unreachable one advances.** A missing or
   unreachable origin moves to the next candidate. A checksum *mismatch* does
   not — `install.sh` exits. Bytes that disagree with their own origin's
   checksums mean that origin is corrupt or hostile, and quietly retrying
   elsewhere would hide it.

`install.sh` also never mixes origins within one install: the archive and the
`checksums.txt` that vouches for it always come from the same place. A healthy
origin's checksums vouching for another origin's bytes would be a weaker
guarantee than either origin offers alone.

## Turning it on

The publishing side is **inert until two repository variables are set**, so this
config ships before the bucket exists and a release behaves exactly as it did
before:

| Variable | Example | Effect |
| --- | --- | --- |
| `ENTIRE_RELEASE_BUCKET` | `install.entire.com` | Enables the GoReleaser `blobs` pipe and the channel-pointer step |
| `ENTIRE_RELEASE_UPLOAD_ROLE` | `arn:aws:iam::934711778961:role/github-actions-release-upload` | Role assumed via OIDC |
| `ENTIRE_RELEASE_REGION` | `us-east-2` | Optional, defaults to `us-east-2` |

Order of operations inside `release.yml` is load-bearing. The channel pointer is
flipped **last**, after the artifacts are uploaded *and* after the macOS
Developer ID check has passed. The pointer is what makes a version visible, so
advertising a release whose binaries Gatekeeper kills would be worse than not
advertising it at all.

## Infrastructure required (not in this repo)

To be built in `entirehq/infra`, modelled on the existing
`infra/aws/entire-nonprod/public-release-bucket/` unit. That unit is the pattern
to copy, **not** the bucket to reuse: it lives in the nonprod account with
versioning off, no lifecycle rules and `enforce_tls = false`, and its upload role
trusts only `repo:entireio/cli:ref:refs/heads/main` as an exact match — a
tag-triggered release presents `refs/tags/vX.Y.Z` and would be refused.

1. **Bucket** — new unit `infra/aws/entire-prod/public-release-bucket/`, module
   `catalog/modules/aws-s3`, region `us-east-2`, `allow_public_policy = true`.
   Differences from the nonprod twin, both deliberate for a customer-facing
   binary store: `enable_versioning = true` so an accidental overwrite is
   recoverable, and `enforce_tls = true`.
2. **Bucket policy** — anonymous `s3:GetObject` on `/v*` and `/channels/*` only,
   keeping the nonprod unit's deny-by-default posture rather than opening `/*`.
   Plus the same `DenyInsecureTransport` statement.
3. **Upload role** — `github-actions-release-upload`, federated to
   `token.actions.githubusercontent.com`, `sts:AssumeRoleWithWebIdentity`,
   `aud = sts.amazonaws.com`. The subject condition must cover **both**
   `repo:entireio/cli:ref:refs/tags/v*` (`StringLike` — tag-triggered releases)
   and `repo:entireio/cli:ref:refs/heads/main` (`workflow_dispatch` runs present
   the branch ref, not the tag). Inline policy: `s3:PutObject` on this bucket
   only, plus `s3:GetObject` so the channel-pointer step can read the current
   pointer for its monotonic check.
4. **CDN + TLS** — the wrinkle worth knowing before starting. Cloudflare cannot
   proxy straight to an S3 REST endpoint over HTTPS with a custom `Host`: the
   origin certificate covers `*.s3.<region>.amazonaws.com`, so validation fails
   unless the zone drops to Flexible SSL, which contradicts point 1. Put
   **CloudFront** in front of the bucket with an ACM certificate for
   `install.entire.com` (issued in `us-east-1`), and point Cloudflare DNS at the
   distribution. This is a new pattern for the infra repo — there is no
   CloudFront distribution in it today. The alternative that avoids CloudFront
   entirely is an R2 bucket with a native custom domain, at the cost of static
   R2 access keys in Actions secrets instead of keyless OIDC.
5. **DNS** — `install.entire.com` in the `entire.com` zone
   (`infra/cloudflare/entire.com/dns_records.tf`), *not* the `entire.io` zone the
   rest of this spec touches. Both zones are on the same Cloudflare account
   (nameservers `chelsea`/`norm.ns.cloudflare.com`), so this is a new record in an
   existing zone — but confirm that zone has a Terraform unit, since `entire.io`
   is the one with units today.
6. **Security Hub** — extend the `public-release-bucket-anonymous-read`
   suppression (controls `S3.2`, `S3.8`) in
   `infra/aws/entire-security/securityhub-org-config/` to the new bucket ARN, and
   note the bucket in the prod `account-baseline` unit as the nonprod one does.
7. **Serve the installers from Cloudflare, on the `entire.com` zone.**
   Independent of the bucket, and the single cheapest fix available:
   `infra/cloudflare/entire.io/redirect_rules.tf`
   currently 307s `entire.io/install.sh` to
   `raw.githubusercontent.com/entireio/cli/refs/heads/main/scripts/install.sh`.
   The install command on the marketing site, in the README, and in the CLI's own
   update prompt therefore fails at hop zero when GitHub is down. It is also a
   version-skew bug: users always run `main`'s installer, never the released one.

   Serve `/install.sh` and `/install.ps1` from the `entire.com` zone instead, and
   apply the same change to the `partial.to` twin.

   **The `entire.io` rules are not deleted.** They become permanent 307s to the
   `entire.com` equivalents. The published one-liner self-heals — an old binary
   prints the `.io` URL, that URL serves the installer, and the binary it installs
   prints `.com` from then on — so the installed population converges after one
   upgrade each. What never heals is everything with no upgrade path: third-party
   Dockerfiles and CI configs that baked in the `.io` one-liner, the CHANGELOG
   entry that published it, and docs we do not control. Those keep arriving
   indefinitely, which is why the redirect is permanent rather than a deprecation
   window.

   Prefer a redirect over serving identical bytes at both hosts: the redirect is
   what makes the residual `.io` traffic countable, which is the only way to tell
   whether that tail is actually shrinking.

## Installing while GitHub is down

`install.sh` handles this automatically once the origin is live. The manual
paths, for when it does not:

```bash
# Straight from our origin, no version lookup
curl -fsSL https://install.entire.com/v0.10.0/entire_darwin_arm64.tar.gz | tar -xz

# Pin a version through the installer
curl -fsSL https://entire.com/install.sh | bash -s -- --version 0.10.0

# From the Go module proxy, which caches independently of GitHub
go install github.com/entireio/cli/cmd/entire@v0.10.0
```

The `go install` route works today, with no infrastructure at all:
`proxy.golang.org` serves module content from its own cache and
`versioninfo.Resolve` recovers the real version from build info. Pin an explicit
version — `@latest` re-resolves against GitHub. The caveats are real but small:
no PostHog key is stamped in, so telemetry is off, and `experimental.Visible`
keeps its default, so experimental commands are visible.

## Remaining work

- **Repoint the cask and Scoop download URLs** at `install.entire.com`
  (`homebrew_casks[].url.template` with `verified:`, and `scoops[].url_template`).
  Deliberately not done here: a published manifest must never name a host that
  isn't serving yet. Once the origin is live this is the highest-value follow-up
  — it moves roughly the other half of all download bytes off GitHub with **no**
  loss of integrity, because the sha256 stays pinned in the manifest, which still
  comes from the tap. Metadata refresh (`brew update`, bucket clone) still needs
  GitHub, so those channels remain outage-affected for *discovering* a release.
- **Decide on signing.** Nothing is signed today beyond macOS notarization — no
  cosign, no SBOM, no attestations. A second origin doubles the number of hosts
  that can serve a bad binary. Two options: sign `checksums.txt` with a key
  pinned in the installer and the CLI, or fetch `checksums.txt` from both origins
  and require agreement, degrading to one origin when the other is down.
- **Warm the module proxy** at release time (a `GET` of
  `proxy.golang.org/github.com/entireio/cli/@v/vX.Y.Z.info`) so the `go install`
  escape hatch is populated *before* an outage rather than during one.
- **Measure the install mix.** `installManagerForCurrentBinary()` already detects
  brew/mise/scoop/unknown but never reports it, so how much of the user base each
  path covers is currently inferred from `checksums.txt` download counts. See
  `docs/install-provenance-plan.md`.

## Tests

`scripts/test-release-origins.sh` (also `mise run test:release-origins`, and a
job in `ci.yml`) runs both scripts against local fake origins: version discovery,
download and verification from our origin, fallback to GitHub for both, a
checksum mismatch aborting rather than falling back, `--version` pinning, the
all-origins-down error, and the channel pointer's routing, monotonicity and
idempotence. It is hermetic and refuses to run an installer still aimed at a
public origin.
