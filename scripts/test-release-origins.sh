#!/bin/bash
#
# Exercises the two-origin release path against local fake origins. No network,
# no writes outside a temp dir.
#
# Why this exists: install.sh reads our own release origin first and falls back
# to GitHub. The fallback only runs when something is broken, which is exactly
# when nobody wants to discover it never worked — so it is tested here instead
# of in production. Covers:
#
#   * install.sh resolving a version from channels/<channel>.json
#   * install.sh downloading and verifying from our origin
#   * install.sh falling back to the GitHub origin for both steps
#   * a checksum mismatch aborting instead of silently trying the next origin
#   * --version pinning, and the error text when every origin is unreachable
#   * publish-channel-manifest.sh routing, monotonicity and idempotence
#
# Requires: python3, curl, tar, sha256sum or shasum.
# Usage: scripts/test-release-origins.sh

set -uo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
INSTALL_SH="${REPO_ROOT}/scripts/install.sh"
PUBLISH_SH="${REPO_ROOT}/scripts/publish-channel-manifest.sh"
WORK="$(mktemp -d)"
# A PATH without the developer's real `entire`, so install.sh takes its
# first-time-install branch instead of the PATH-conflict branch.
SAFE_PATH="/usr/bin:/bin:/usr/sbin:/sbin"

pass=0
fail=0
ok()   { printf '  \033[0;32mPASS\033[0m %s\n' "$1"; pass=$((pass + 1)); }
nope() { printf '  \033[0;31mFAIL\033[0m %s\n' "$1"; fail=$((fail + 1)); }
section() { printf '\n\033[1m%s\033[0m\n' "$1"; }
indent() { while IFS= read -r line; do printf '      %s\n' "$line"; done; }

# expect <description> <command...> — passes when the command succeeds.
expect() {
    local desc="$1"
    shift
    if "$@"; then ok "$desc"; else nope "$desc"; fi
}

# refute <description> <command...> — passes when the command fails.
refute() {
    local desc="$1"
    shift
    if "$@"; then nope "$desc"; else ok "$desc"; fi
}

contains() { grep -qF -- "$2" <<< "$1"; }
eq()       { [ "$1" = "$2" ]; }

cleanup() {
    for pidfile in "$WORK"/*/.pid; do
        [ -f "$pidfile" ] && kill "$(cat "$pidfile")" 2>/dev/null
    done
    rm -rf "$WORK"
}
trap cleanup EXIT

sha256_of() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1"
    else
        shasum -a 256 "$1"
    fi
}

archive_name() {
    local os arch
    os="$(uname -s | tr '[:upper:]' '[:lower:]')"
    arch="$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')"
    printf 'entire_%s_%s.tar.gz' "$os" "$arch"
}

# build_origin <dir> <version> — a release tree shaped like both real origins:
# <root>/v<version>/{archive,checksums.txt} plus <root>/channels/*.json
build_origin() {
    local dir="$1" version="$2" arch
    arch="$(archive_name)"
    mkdir -p "$dir/v$version" "$dir/channels" "$dir/.stage"
    printf '#!/bin/sh\necho "entire %s"\n' "$version" > "$dir/.stage/entire"
    printf '#!/bin/sh\necho helper\n' > "$dir/.stage/git-remote-entire"
    chmod +x "$dir/.stage/entire" "$dir/.stage/git-remote-entire"
    tar -czf "$dir/v$version/$arch" -C "$dir/.stage" entire git-remote-entire
    ( cd "$dir/v$version" && sha256_of "$arch" > checksums.txt )
    printf '{"channel":"stable","tag":"v%s","version":"%s"}\n' "$version" "$version" \
        > "$dir/channels/stable.json"
}

# blackhole <dir> -> prints a port that accepts connections and never responds.
# A dead origin fails fast on connect; this is the nastier case — reachable, so
# curl waits. install.sh must still fail over.
blackhole() {
    local dir="$1" port
    mkdir -p "$dir"
    port="$(free_port)"
    python3 -c '
import socket, sys, time
srv = socket.socket()
srv.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
srv.bind(("127.0.0.1", int(sys.argv[1])))
srv.listen(16)
held = []
while True:
    conn, _ = srv.accept()
    held.append(conn)  # accepted, deliberately never answered
    time.sleep(0.01)
' "$port" >/dev/null 2>&1 &
    echo $! > "$dir/.pid"
    echo "$port"
}

free_port() {
    python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1]);s.close()'
}

serve() { # <dir> -> prints the port it is listening on
    local dir="$1" port
    port="$(free_port)"
    ( cd "$dir" && exec python3 -m http.server "$port" --bind 127.0.0.1 >/dev/null 2>&1 ) &
    echo $! > "$dir/.pid"
    for _ in $(seq 1 50); do
        curl -fsS -o /dev/null "http://127.0.0.1:$port/" 2>/dev/null && break
        sleep 0.1
    done
    echo "$port"
}

# repoint <out> <our-origin> <github-origin> <github-api>
# A copy of install.sh with its origin constants aimed at local servers, so the
# two-tier chain runs without touching the network.
repoint() {
    sed -e "s|^DEFAULT_RELEASE_ORIGIN=.*|DEFAULT_RELEASE_ORIGIN=\"$2\"|" \
        -e "s|^GITHUB_RELEASE_ORIGIN=.*|GITHUB_RELEASE_ORIGIN=\"$3\"|" \
        -e "s|^GITHUB_API=.*|GITHUB_API=\"$4\"|" \
        "$INSTALL_SH" > "$1"
}

# run_install <home> <script> [args...]
#
# Runs install.sh with an empty environment and an isolated HOME, so it can
# never write to the developer's real ~/.local/bin. Set BASE_URL to point it at
# a local origin.
#
# env -i wipes the environment, so BASE_URL has to be re-injected explicitly —
# exporting it around the call is not enough. Getting that wrong made two cases
# silently download the real CLI from the real GitHub, so the guard below
# refuses to run a script that is still aimed at a public origin.
run_install() {
    local home="$1" script="$2"
    shift 2
    mkdir -p "$home"

    local -a env_args=(HOME="$home" PATH="$SAFE_PATH" SHELL=/bin/bash)
    if [ -n "${BASE_URL:-}" ]; then
        env_args+=("ENTIRE_INSTALL_BASE_URL=$BASE_URL")
    elif ! grep -q '^DEFAULT_RELEASE_ORIGIN="http://127.0.0.1:' "$script"; then
        printf 'Error: %s would reach a public origin. Set BASE_URL or use repoint().\n' \
            "$(basename "$script")" >&2
        return 1
    fi

    env -i "${env_args[@]}" bash "$script" "$@" 2>&1
}

for tool in python3 curl tar; do
    command -v "$tool" >/dev/null 2>&1 || { printf 'Error: %s is required\n' "$tool" >&2; exit 1; }
done

# ============================================================ install.sh
section "install.sh — our origin first"

build_origin "$WORK/ours" 9.9.9
OURS_PORT="$(serve "$WORK/ours")"
OURS="http://127.0.0.1:$OURS_PORT"
ARCH="$(archive_name)"

out="$(BASE_URL="$OURS" run_install "$WORK/home-ours" "$INSTALL_SH")"
expect "resolves the version from channels/stable.json" contains "$out" "Installing version: 9.9.9"
expect "verifies the archive against our origin's checksums.txt" \
    contains "$out" "Checksum verified ($OURS)"
expect "installs entire" test -x "$WORK/home-ours/.local/bin/entire"
expect "installs git-remote-entire" test -x "$WORK/home-ours/.local/bin/git-remote-entire"
refute "does not touch GitHub when our origin answers" contains "$out" "github"

section "install.sh — falls back to GitHub"

build_origin "$WORK/gh" 8.8.8
mkdir -p "$WORK/gh/api/releases"
printf '{"tag_name":"v8.8.8","prerelease":false}\n' > "$WORK/gh/api/releases/latest"
GH_PORT="$(serve "$WORK/gh")"
GH="http://127.0.0.1:$GH_PORT"
DEAD="http://127.0.0.1:$(free_port)"

repoint "$WORK/install-fallback.sh" "$DEAD" "$GH" "$GH/api"
out="$(run_install "$WORK/home-fallback" "$WORK/install-fallback.sh")"
expect "falls back for version discovery" contains "$out" "Installing version: 8.8.8"
expect "falls back for the download" contains "$out" "Checksum verified (github.com/"
expect "the fallback install completes" test -x "$WORK/home-fallback/.local/bin/entire"

section "install.sh — a checksum mismatch aborts"

build_origin "$WORK/bad" 7.7.7
printf '%s  %s\n' "$(printf '0%.0s' $(seq 1 64))" "$ARCH" > "$WORK/bad/v7.7.7/checksums.txt"
BAD_PORT="$(serve "$WORK/bad")"
repoint "$WORK/install-badsum.sh" "http://127.0.0.1:$BAD_PORT" "$GH" "$GH/api"
out="$(run_install "$WORK/home-badsum" "$WORK/install-badsum.sh")"
rc=$?
refute "exits non-zero" eq "$rc" 0
expect "says the checksum failed" contains "$out" "Checksum verification failed"
refute "installs nothing" test -e "$WORK/home-badsum/.local/bin/entire"
refute "does not fall back after a mismatch" contains "$out" "Checksum verified"

section "install.sh — pinning and unreachable origins"

out="$(BASE_URL="$OURS" run_install "$WORK/home-pin" "$INSTALL_SH" --version v9.9.9)"
refute "--version skips version discovery" contains "$out" "Fetching latest"
expect "--version installs" test -x "$WORK/home-pin/.local/bin/entire"

repoint "$WORK/install-alldown.sh" "$DEAD" "$DEAD" "$DEAD/api"
out="$(run_install "$WORK/home-alldown" "$WORK/install-alldown.sh")"
rc=$?
refute "every origin down exits non-zero" eq "$rc" 0
expect "the error names the pinning escape hatch" contains "$out" "--version X.Y.Z"

section "install.sh — a hanging origin still fails over"

# Same code path, shorter budget, so this costs ~2s instead of ~15s.
BH_PORT="$(blackhole "$WORK/blackhole")"
repoint "$WORK/install-hang.sh" "http://127.0.0.1:$BH_PORT" "$GH" "$GH/api"
sed -i.bak \
    -e 's|^CURL_METADATA_OPTS=.*|CURL_METADATA_OPTS=(--connect-timeout 1 --max-time 2)|' \
    -e 's|^CURL_DOWNLOAD_OPTS=.*|CURL_DOWNLOAD_OPTS=(--connect-timeout 1 --max-time 2)|' \
    "$WORK/install-hang.sh"
started="$(date +%s)"
out="$(run_install "$WORK/home-hang" "$WORK/install-hang.sh")"
elapsed=$(( $(date +%s) - started ))
expect "falls over to GitHub instead of hanging" contains "$out" "Installing version: 8.8.8"
expect "the install completes despite the hung origin" test -x "$WORK/home-hang/.local/bin/entire"
expect "gives up on the hung origin within the budget" test "$elapsed" -lt 20

# ============================================================ channel pointer
section "publish-channel-manifest.sh"

STUB_BIN="$WORK/stub-bin"
mkdir -p "$STUB_BIN"
cat > "$STUB_BIN/aws" <<'AWS'
#!/bin/bash
# Stub for `aws s3 cp`, mapping s3://bucket/key to $AWS_STUB_ROOT/bucket/key.
set -uo pipefail
echo "$*" >> "$AWS_STUB_ROOT/calls.log"
[ "${1:-}" = "s3" ] && [ "${2:-}" = "cp" ] || exit 64
src="$3"; dst="$4"
s3path() { printf '%s/%s' "$AWS_STUB_ROOT" "${1#s3://}"; }
if [ "${src#s3://}" != "$src" ]; then
    f="$(s3path "$src")"; [ -f "$f" ] || exit 1; cat "$f"; exit 0
fi
f="$(s3path "$dst")"; mkdir -p "$(dirname "$f")"; cp "$src" "$f"
AWS
chmod +x "$STUB_BIN/aws"

publish() { PATH="$STUB_BIN:$SAFE_PATH" bash "$PUBLISH_SH" "$@" 2>&1; }
fresh() { export AWS_STUB_ROOT="$WORK/s3-$1"; rm -rf "$AWS_STUB_ROOT"; mkdir -p "$AWS_STUB_ROOT/b"; }
seed() {
    mkdir -p "$AWS_STUB_ROOT/b/channels"
    printf '{"channel":"%s","tag":"v%s","version":"%s"}\n' "$1" "$2" "$2" \
        > "$AWS_STUB_ROOT/b/channels/$1.json"
}
pointer() {
    grep '"version"' "$AWS_STUB_ROOT/b/channels/$1.json" 2>/dev/null \
        | sed -E 's/.*"version": *"([^"]+)".*/\1/'
}

fresh cold
publish --tag v1.2.3 --bucket b >/dev/null
expect "a stable tag writes channels/stable.json" eq "$(pointer stable)" "1.2.3"
expect "the pointer gets a short TTL, not an immutable one" \
    grep -qF "max-age=60" "$AWS_STUB_ROOT/calls.log"

fresh nightly
publish --tag v1.2.4-nightly.202608180612.abc1234 --bucket b >/dev/null
expect "a nightly tag writes channels/nightly.json" \
    eq "$(pointer nightly)" "1.2.4-nightly.202608180612.abc1234"
refute "a nightly never touches the stable pointer" test -f "$AWS_STUB_ROOT/b/channels/stable.json"

fresh forward
seed stable 1.2.3
publish --tag v1.3.0 --bucket b >/dev/null
expect "moves the pointer forward" eq "$(pointer stable)" "1.3.0"

fresh backward
seed stable 1.3.0
out="$(publish --tag v1.2.3 --bucket b)"; rc=$?
expect "refuses to move the pointer backwards" eq "$rc" 2
expect "the pointer is unchanged after a refusal" eq "$(pointer stable)" "1.3.0"
expect "the refusal names the override" contains "$out" "--force"

fresh forced
seed stable 1.3.0
publish --tag v1.2.3 --bucket b --force >/dev/null
expect "--force moves it backwards" eq "$(pointer stable)" "1.2.3"

fresh repeat
seed stable 1.2.3
out="$(publish --tag v1.2.3 --bucket b)"; rc=$?
expect "re-publishing the same version exits 0" eq "$rc" 0
expect "re-publishing the same version is a no-op" contains "$out" "nothing to do"

fresh older-nightly
seed nightly 1.2.4-nightly.202608180612.bbb1111
out="$(publish --tag v1.2.4-nightly.202608170612.aaa0000 --bucket b)"; rc=$?
expect "refuses an older nightly timestamp" eq "$rc" 2

fresh prerelease-vs-core
seed nightly 1.2.3
out="$(publish --tag v1.2.3-nightly.202608180612.abc1234 --bucket b)"; rc=$?
expect "a prerelease ranks below the released core version" eq "$rc" 2

fresh badinput
publish --tag not-a-version --bucket b >/dev/null 2>&1; rc=$?
expect "rejects a tag that is not semver" eq "$rc" 1
publish --bucket b >/dev/null 2>&1; rc=$?
expect "requires --tag" eq "$rc" 1

printf '\n  %d passed, %d failed\n' "$pass" "$fail"
[ "$fail" -eq 0 ]
