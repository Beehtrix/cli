package strategy

import (
	"context"
	"os/exec"
	"strings"

	"github.com/entireio/cli/cmd/entire/cli/checkpoint/id"
	"github.com/entireio/cli/cmd/entire/cli/trailers"
)

// trailerRules is how a repository's git config says to read the checkpoint
// trailers of its saved commits.
type trailerRules struct {
	commentPrefix string
	// wholeMessage scans the whole message, as linking did before only the
	// last paragraph counted. core.commentChar=auto picks the prefix per
	// message, and commit.cleanup modes that keep comment lines can keep lines
	// below the trailer that the last-paragraph reader does not skip.
	wholeMessage bool
}

// readTrailerRules reads core.commentChar, core.commentString and
// commit.cleanup in one git config call. Only the configured values count:
// the hooks cannot see `git commit --cleanup=<mode>`.
func readTrailerRules(ctx context.Context) trailerRules {
	rules := trailerRules{commentPrefix: trailers.DefaultCommentPrefix}
	out, err := exec.CommandContext(ctx, "git", "config", "--get-regexp", `^(core\.comment(char|string)|commit\.cleanup)$`).Output()
	if err != nil {
		return rules // unset, or unreadable
	}
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		key, value, _ := strings.Cut(line, " ")
		switch {
		case key == "commit.cleanup":
			switch value {
			case "whitespace", "verbatim", "scissors":
				rules.wholeMessage = true
			}
		case value == "":
			rules.commentPrefix = trailers.DefaultCommentPrefix
		default:
			// core.commentChar and core.commentString are aliases; the last
			// one set wins.
			rules.commentPrefix = value
		}
	}
	if rules.commentPrefix == "auto" {
		rules.wholeMessage = true
	}
	return rules
}

func (r trailerRules) checkpoints(message string) []id.CheckpointID {
	if r.wholeMessage {
		return trailers.ParseAllCheckpoints(message)
	}
	return trailers.ParseLastParagraphCheckpoints(message, r.commentPrefix)
}

// LinkedCheckpoints returns the checkpoints a saved commit's message links,
// read as the repository's git config says: the Entire-Checkpoint lines of
// its last paragraph, or every ID in the message in configurations where that
// paragraph may not be the one holding the trailer. Code that writes into a
// checkpoint resolves a commit with it; read-only views use CommitCheckpoints.
func LinkedCheckpoints(ctx context.Context, message string) []id.CheckpointID {
	if !strings.Contains(message, trailers.CheckpointTrailerKey) {
		return nil // nothing to link, and no git config to read
	}
	return readTrailerRules(ctx).checkpoints(message)
}

// CommitCheckpoints returns the checkpoints a read-only view shows for a saved
// commit, without reading git config: its last paragraph's IDs, skipping
// git's default comment lines, or, when that names none, the first ID
// anywhere in the message, since a squash commit can carry its squashed
// commits' trailers in the body. Only read-only views may use that fallback.
func CommitCheckpoints(message string) []id.CheckpointID {
	if ids := trailers.ParseLastParagraphCheckpoints(message, trailers.DefaultCommentPrefix); len(ids) > 0 {
		return ids
	}
	if cpID, ok := trailers.ParseCheckpoint(message); ok {
		return []id.CheckpointID{cpID}
	}
	return nil
}

// CommitCheckpoint returns the checkpoint a read-only view shows for a saved
// commit: the last of CommitCheckpoints, the one attach joins and
// `explain --generate` writes, since a squash's inherited trailers come before
// the one prepare-commit-msg stamped.
func CommitCheckpoint(message string) (id.CheckpointID, bool) {
	ids := CommitCheckpoints(message)
	if len(ids) == 0 {
		return id.EmptyCheckpointID, false
	}
	return ids[len(ids)-1], true
}
