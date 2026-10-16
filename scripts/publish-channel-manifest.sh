#!/bin/bash
#
# Publishes the channel pointer that install.sh and the CLI's update check read
# before they fall back to the GitHub API:
#
#   <bucket>/channels/stable.json
#   <bucket>/channels/nightly.json
#
# One request, one answer. That removes api.github.com from the version-lookup
# path entirely, and with it the unauthenticated 60-requests-per-hour-per-IP
# limit that a shared office or CI egress address can exhaust on its own.
#
# Run this AFTER the release artifacts are uploaded and verified. The pointer is
# what makes a version visible, so flipping it before the bytes are in place —
# or before the macOS signing check has passed — would advertise a release
# nobody can install.
#
# The manifest deliberately carries no checksums. checksums.txt, which
# GoReleaser generates and mirrors next to the archives, stays the single
# integrity source; two files listing the same hashes is two files that can
# disagree.
#
# Usage:
#   publish-channel-manifest.sh --tag vX.Y.Z --bucket NAME [--region R] [--force]
#
# Exit codes:
#   0  published (or nothing to do)
#   1  bad usage / upload failure
#   2  refused: the channel already points at a newer version (use --force)

set -euo pipefail

TAG=""
BUCKET=""
REGION="us-east-2"
FORCE=0

die() { printf 'Error: %s\n' "$1" >&2; exit 1; }

while [[ $# -gt 0 ]]; do
    case "$1" in
        --tag)    shift; [[ $# -gt 0 ]] || die "--tag requires a value"; TAG="$1" ;;
        --bucket) shift; [[ $# -gt 0 ]] || die "--bucket requires a value"; BUCKET="$1" ;;
        --region) shift; [[ $# -gt 0 ]] || die "--region requires a value"; REGION="$1" ;;
        --force)  FORCE=1 ;;
        -h|--help)
            cat <<USAGE
Usage: publish-channel-manifest.sh --tag vX.Y.Z --bucket NAME [--region R] [--force]

Publishes the channel pointer (channels/stable.json or channels/nightly.json)
that install.sh and the CLI's update check read before falling back to the
GitHub API. The channel is derived from the tag: a prerelease segment means
nightly, anything else means stable.

Run this only after the release artifacts are uploaded and verified — the
pointer is what makes a version visible.

Options:
  --tag      Release tag to publish, with the v prefix (required)
  --bucket   Destination bucket (required)
  --region   AWS region (default: us-east-2)
  --force    Move the pointer backwards to an older version
  -h/--help  Show this help message

Exit codes:
  0  published, or already current
  1  bad usage, or the upload failed
  2  refused: the channel already points at a newer version
USAGE
            exit 0
            ;;
        *) die "Unknown argument: $1" ;;
    esac
    shift
done

[[ -n "$TAG" ]] || die "--tag is required"
[[ -n "$BUCKET" ]] || die "--bucket is required"
command -v aws >/dev/null 2>&1 || die "the aws CLI is required"

VERSION="${TAG#v}"
[[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.+-]+)?$ ]] \
    || die "tag '${TAG}' is not a semver release tag"

# A tag carrying a prerelease segment belongs to nightly, anything else to
# stable. This is the same test release.yml uses to pick release notes, kept
# here so the pointer can never land on the channel the tag doesn't belong to.
if [[ "$VERSION" == *-* ]]; then
    CHANNEL="nightly"
else
    CHANNEL="stable"
fi

KEY="channels/${CHANNEL}.json"
S3_URI="s3://${BUCKET}/${KEY}"

# semver_gt A B -> true when A is strictly newer than B.
semver_gt() {
    python3 - "$1" "$2" <<'PY'
import sys

def parse(v):
    core, _, pre = v.partition("-")
    nums = tuple(int(p) for p in core.split("."))
    # An absent prerelease outranks any prerelease of the same core version.
    return nums, pre

def key(v):
    nums, pre = parse(v)
    if not pre:
        return (nums, 1, ())
    parts = tuple(
        (0, int(p), "") if p.isdigit() else (1, 0, p)
        for p in pre.split(".")
    )
    return (nums, 0, parts)

a, b = sys.argv[1], sys.argv[2]
sys.exit(0 if key(a) > key(b) else 1)
PY
}

# Monotonic guard. release.yml can be dispatched with any version string, so
# without this a re-run of an old tag would quietly point every fresh install
# and every update prompt at a version we already moved past.
CURRENT=""
if CURRENT_JSON="$(aws s3 cp "$S3_URI" - --region "$REGION" 2>/dev/null)"; then
    CURRENT="$(printf '%s' "$CURRENT_JSON" \
        | grep '"version"' \
        | sed -E 's/.*"version": *"v?([^"]+)".*/\1/' \
        | head -n 1)"
fi

if [[ -n "$CURRENT" ]]; then
    if [[ "$CURRENT" == "$VERSION" ]]; then
        printf 'channels/%s.json already points at %s — nothing to do.\n' "$CHANNEL" "$VERSION"
        exit 0
    fi
    if ! semver_gt "$VERSION" "$CURRENT"; then
        if [[ "$FORCE" -ne 1 ]]; then
            printf 'Refusing to move the %s channel backwards: %s -> %s. Re-run with --force to override.\n' \
                "$CHANNEL" "$CURRENT" "$VERSION" >&2
            exit 2
        fi
        printf 'Moving the %s channel backwards (%s -> %s) because --force was given.\n' \
            "$CHANNEL" "$CURRENT" "$VERSION" >&2
    fi
fi

PUBLISHED_AT="$(TZ=UTC0 date +%Y-%m-%dT%H:%M:%SZ)"
TMP="$(mktemp)"
trap 'rm -f "$TMP"' EXIT
cat > "$TMP" <<JSON
{
  "channel": "${CHANNEL}",
  "tag": "${TAG}",
  "version": "${VERSION}",
  "published_at": "${PUBLISHED_AT}"
}
JSON

# A short TTL, not an immutable one: this object is the thing that changes when
# a release ships. Readers check at most once every 24h, so a minute of edge
# caching costs nothing and still absorbs a stampede.
aws s3 cp "$TMP" "$S3_URI" \
    --region "$REGION" \
    --content-type application/json \
    --cache-control "public, max-age=60"

printf 'Published %s -> %s (%s)\n' "$KEY" "$VERSION" "$CHANNEL"
