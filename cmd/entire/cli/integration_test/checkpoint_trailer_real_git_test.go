//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/entireio/cli/cmd/entire/cli/checkpoint/id"
	"github.com/entireio/cli/cmd/entire/cli/trailers"
)

// The trailer tests above hand prepare-commit-msg the message git would write.
// These run real `git commit` with Entire's hooks and a scripted editor, so the
// message, the comment character git picks and its cleanup are git's own.
// Every commit must stay linked to the checkpoint post-commit condensed into,
// read as post-commit reads it, and no checkpoint the message only names may
// be created.
func TestTrailerAuthority_RealGitCommits(t *testing.T) {
	t.Parallel()
	editors := t.TempDir()
	writeScripts(t, editors, map[string]string{
		// Types at the start of the first line, where an editor opens.
		"type": "#!/bin/sh\n{ printf 'Typed '; cat \"$1\"; } > \"$1.typed\" && mv \"$1.typed\" \"$1\"\n",
	})
	keep := "GIT_EDITOR=true"
	typeSubject := "GIT_EDITOR=" + shellQuote(filepath.ToSlash(filepath.Join(editors, "type")))
	files := t.TempDir()
	message := filepath.Join(files, "message")
	require.NoError(t, os.WriteFile(message, []byte("Fix login\n\nBody.\n"), 0o600))
	foreignTrailer := filepath.Join(files, "foreign-trailer")
	require.NoError(t, os.WriteFile(foreignTrailer, []byte("Fix login\n\nEntire-Checkpoint: "+advForeignCheckpoint+"\n"), 0o600))

	// commitLinked commits path with messageArgs (-m "My change" when none)
	// as an agent's work, and returns the checkpoint the commit links.
	commitLinked := func(env *TestEnv, git func([]string, ...string) (string, error), path string, messageArgs ...string) string {
		env.T.Helper()
		authorityAgentTurn(env, path, "work on "+path+"\n")
		_, err := git(nil, "add", path)
		require.NoError(env.T, err)
		if len(messageArgs) == 0 {
			messageArgs = []string{"-m", "My change"}
		}
		out, err := git(nil, append([]string{"commit", "--no-gpg-sign"}, messageArgs...)...)
		require.NoError(env.T, err, out)
		linked := trailers.ParseLastParagraphCheckpoints(env.GetCommitMessage(env.GetHeadHash()), trailers.DefaultCommentPrefix)
		require.Len(env.T, linked, 1, "precondition: the commit is linked")
		return linked[0].String()
	}
	runGit := func(env *TestEnv, git func([]string, ...string) (string, error), args ...string) {
		env.T.Helper()
		out, err := git(nil, args...)
		require.NoError(env.T, err, out)
	}
	// squashOf commits side.txt on a side branch with messageArgs, then
	// squash-merges it back, and returns the side commit's checkpoint.
	squashOf := func(env *TestEnv, git func([]string, ...string) (string, error), messageArgs ...string) string {
		env.T.Helper()
		runGit(env, git, "checkout", "-q", "-b", "side")
		sideID := commitLinked(env, git, "side.txt", messageArgs...)
		runGit(env, git, "checkout", "-q", "-")
		runGit(env, git, "merge", "--squash", "side")
		return sideID
	}
	// redoOf commits first.txt with messageArgs and resets it away softly, so
	// the commit under test redoes it, and returns its checkpoint.
	redoOf := func(env *TestEnv, git func([]string, ...string) (string, error), messageArgs ...string) string {
		env.T.Helper()
		firstID := commitLinked(env, git, "first.txt", messageArgs...)
		runGit(env, git, "reset", "-q", "--soft", "HEAD~1")
		return firstID
	}
	bodyMention := []string{"-m", "Side work", "-m", "Entire-Checkpoint: " + advForeignCheckpoint, "-m", "More body."}
	amendLinked := func(env *TestEnv, git func([]string, ...string) (string, error)) ([]string, []string) {
		commitLinked(env, git, "first.txt")
		return nil, nil
	}

	tests := []struct {
		name   string
		config map[string]string
		// setup prepares what the commit under test amends, squashes or
		// redoes, and returns the checkpoints the commit must also link and
		// the ones it must not.
		setup  func(env *TestEnv, git func([]string, ...string) (string, error)) (inherits, unlinked []string)
		commit []string
		// editor is the GIT_EDITOR for the commit, when git opens one.
		editor string
		// prefix is the comment prefix post-commit reads the commit with;
		// wholeMessage is its fallback to scanning the whole message.
		prefix       string
		wholeMessage bool
	}{
		{name: "-m", commit: []string{"-m", "Fix login"}},
		{name: "-F", commit: []string{"-F", message}},
		{name: "editor", editor: typeSubject},
		{name: "--amend", setup: amendLinked, commit: []string{"--amend"}, editor: keep},
		{name: "--amend --no-edit", setup: amendLinked, commit: []string{"--amend", "--no-edit"}},
		{name: "merge --squash then editor", setup: func(env *TestEnv, git func([]string, ...string) (string, error)) ([]string, []string) {
			return []string{squashOf(env, git)}, nil
		}, editor: keep},
		{name: "merge --squash then -m", setup: func(env *TestEnv, git func([]string, ...string) (string, error)) ([]string, []string) {
			return []string{squashOf(env, git)}, nil
		}, commit: []string{"-m", "Squash side"}},
		{name: "merge --squash of a commit whose body names a checkpoint", setup: func(env *TestEnv, git func([]string, ...string) (string, error)) ([]string, []string) {
			return []string{squashOf(env, git, bodyMention...)}, []string{advForeignCheckpoint}
		}, editor: keep},
		{name: "merge --squash of a commit whose body names a commit", setup: func(env *TestEnv, git func([]string, ...string) (string, error)) ([]string, []string) {
			namedID := commitLinked(env, git, "named.txt")
			sideID := squashOf(env, git, "-m", "Side work", "-m", "commit "+env.GetHeadHash())
			return []string{sideID}, []string{namedID}
		}, editor: keep},
		{name: "redo after reset --soft then editor", setup: func(env *TestEnv, git func([]string, ...string) (string, error)) ([]string, []string) {
			return []string{redoOf(env, git)}, nil
		}, editor: typeSubject},
		{name: "redo of a commit whose body names a checkpoint", setup: func(env *TestEnv, git func([]string, ...string) (string, error)) ([]string, []string) {
			return []string{redoOf(env, git, bodyMention...)}, []string{advForeignCheckpoint}
		}, editor: typeSubject},
		{name: "editor with ; comments", config: map[string]string{"core.commentChar": ";"}, editor: typeSubject, prefix: ";"},
		{name: "editor with whitespace cleanup", config: map[string]string{"commit.cleanup": "whitespace"}, editor: typeSubject, wholeMessage: true},
		{name: "editor with verbatim cleanup", config: map[string]string{"commit.cleanup": "verbatim"}, editor: typeSubject, wholeMessage: true},
		{name: "editor with scissors cleanup", config: map[string]string{"commit.cleanup": "scissors"}, editor: typeSubject, wholeMessage: true},
		{name: "editor under core.commentChar=auto", config: map[string]string{"core.commentChar": "auto"}, editor: typeSubject, wholeMessage: true},
		{name: "-F of a last paragraph naming a foreign checkpoint", commit: []string{"-F", foreignTrailer}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			env := NewFeatureBranchEnv(t)
			git := gitWithEntireCommitHooksEnv(t, env)
			for key, value := range tt.config {
				setRepoGitConfig(env, key, value)
			}
			var inherits, unlinked []string
			if tt.setup != nil {
				inherits, unlinked = tt.setup(env, git)
			}
			authorityAgentTurn(env, "mine.txt", "my agent work\n")
			_, err := git(nil, "add", "mine.txt")
			require.NoError(t, err)
			editor := tt.editor
			if editor == "" {
				editor = keep // git opens no editor for these; it must not be the developer's
			}

			out, err := git([]string{editor}, append([]string{"commit", "--no-gpg-sign"}, tt.commit...)...)
			require.NoError(t, err, out)

			recorded := env.GetCommitMessage(env.GetHeadHash())
			var linked []id.CheckpointID
			if tt.wholeMessage {
				linked = trailers.ParseAllCheckpoints(recorded)
			} else {
				prefix := tt.prefix
				if prefix == "" {
					prefix = trailers.DefaultCommentPrefix
				}
				linked = trailers.ParseLastParagraphCheckpoints(recorded, prefix)
			}
			require.NotEmpty(t, linked, "the commit stays linked: %q", recorded)
			for _, cpID := range inherits {
				require.Contains(t, linked, id.MustCheckpointID(cpID), "the commit links the checkpoint it inherits: %q", recorded)
			}
			for _, cpID := range unlinked {
				require.NotContains(t, linked, id.MustCheckpointID(cpID), "the commit does not link a checkpoint a squashed or redone message only names: %q", recorded)
			}
			require.True(t, advCheckpointStored(env, linked[len(linked)-1].String()), "post-commit condenses into the checkpoint the commit links: %q", recorded)
			require.False(t, advCheckpointStored(env, advForeignCheckpoint), "no checkpoint the message only names is created: %q", recorded)
		})
	}
}
