package strategy

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/entireio/cli/cmd/entire/cli/checkpoint/id"
	"github.com/entireio/cli/cmd/entire/cli/testutil"
)

const (
	bodyMentionMessage = "Fix\n\nEntire-Checkpoint: a1b2c3d4e5f6\n\nSigned-off-by: Test User <test@example.com>\n"
	semicolonMessage   = "Fix\n\nEntire-Checkpoint: a1b2c3d4e5f6\n\n; note\n"
	commentStrMessage  = "Fix\n\nEntire-Checkpoint: a1b2c3d4e5f6\n\n// note\n"
)

// LinkedCheckpoints reads the repository's git config: the last paragraph,
// skipping trailing lines with the configured comment prefix, or the whole
// message when the config can keep lines below the trailer that the
// last-paragraph reader would take for the trailer block.
func TestLinkedCheckpoints_FollowsGitConfig(t *testing.T) {
	mentioned := []id.CheckpointID{id.MustCheckpointID("a1b2c3d4e5f6")}
	tests := []struct {
		name    string
		config  [][2]string
		message string
		want    []id.CheckpointID
	}{
		{name: "default reads the last paragraph", message: bodyMentionMessage},
		{name: "strip reads the last paragraph", config: [][2]string{{"commit.cleanup", "strip"}}, message: bodyMentionMessage},
		{name: "default cleanup reads the last paragraph", config: [][2]string{{"commit.cleanup", "default"}}, message: bodyMentionMessage},
		{name: "whitespace scans the whole message", config: [][2]string{{"commit.cleanup", "whitespace"}}, message: bodyMentionMessage, want: mentioned},
		{name: "verbatim scans the whole message", config: [][2]string{{"commit.cleanup", "verbatim"}}, message: bodyMentionMessage, want: mentioned},
		{name: "scissors scans the whole message", config: [][2]string{{"commit.cleanup", "scissors"}}, message: bodyMentionMessage, want: mentioned},
		{name: "auto scans the whole message", config: [][2]string{{"core.commentChar", "auto"}}, message: bodyMentionMessage, want: mentioned},
		{name: "default prefix keeps ; lines", message: semicolonMessage},
		{name: "configured comment char", config: [][2]string{{"core.commentChar", ";"}}, message: semicolonMessage, want: mentioned},
		{name: "configured comment string", config: [][2]string{{"core.commentString", "//"}}, message: commentStrMessage, want: mentioned},
		{
			name:    "the last of the aliases set wins",
			config:  [][2]string{{"core.commentChar", ";"}, {"core.commentString", "//"}},
			message: semicolonMessage,
		},
		{
			name:    "the last of the aliases set wins, reversed",
			config:  [][2]string{{"core.commentString", "//"}, {"core.commentChar", ";"}},
			message: semicolonMessage,
			want:    mentioned,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testutil.IsolateGitConfigEnv(t)
			dir := resolvedTempDir(t)
			testutil.InitRepo(t, dir)
			for _, kv := range tt.config {
				testutil.RunGit(t, dir, "config", kv[0], kv[1])
			}
			t.Chdir(dir)

			assert.Equal(t, tt.want, LinkedCheckpoints(context.Background(), tt.message))
		})
	}
}

// Read-only views read without git config: the last paragraph's last ID, the
// one attach joins and --generate writes, or else the first ID anywhere.
func TestCommitCheckpoint(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		message string
		want    string
	}{
		{name: "last of the last paragraph", message: "Squash\n\nEntire-Checkpoint: aaaaaaaaaaaa\nEntire-Checkpoint: bbbbbbbbbbbb\n", want: "bbbbbbbbbbbb"},
		{name: "last paragraph over a body mention", message: "Fix\n\nEntire-Checkpoint: aaaaaaaaaaaa\n\nEntire-Checkpoint: bbbbbbbbbbbb\n", want: "bbbbbbbbbbbb"},
		{name: "first anywhere without a last-paragraph trailer", message: "Squash\n\n    Entire-Checkpoint: aaaaaaaaaaaa\n\n    Entire-Checkpoint: bbbbbbbbbbbb\n\nBody.\n", want: "aaaaaaaaaaaa"},
		{name: "skips # comment lines", message: "Fix\n\nEntire-Checkpoint: aaaaaaaaaaaa\n\n# Please enter the commit message.\n", want: "aaaaaaaaaaaa"},
		{name: "none", message: "Fix\n\nBody.\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := CommitCheckpoint(tt.message)
			assert.Equal(t, tt.want != "", ok)
			assert.Equal(t, tt.want, got.String())
		})
	}
}
