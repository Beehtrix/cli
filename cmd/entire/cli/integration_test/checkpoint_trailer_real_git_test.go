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

	// commitLinked commits path with -m as an agent's work.
	commitLinked := func(env *TestEnv, git func([]string, ...string) (string, error), path string) {
		env.T.Helper()
		authorityAgentTurn(env, path, "work on "+path+"\n")
		_, err := git(nil, "add", path)
		require.NoError(env.T, err)
		out, err := git(nil, "commit", "--no-gpg-sign", "-m", "My change")
		require.NoError(env.T, err, out)
		require.NotEmpty(env.T, trailers.ParseLastParagraphCheckpoints(env.GetCommitMessage(env.GetHeadHash()), trailers.DefaultCommentPrefix), "precondition: the commit is linked")
	}

	tests := []struct {
		name   string
		config map[string]string
		// setup makes the commit the one under test amends.
		setup  func(env *TestEnv, git func([]string, ...string) (string, error))
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
		{name: "--amend", setup: func(env *TestEnv, git func([]string, ...string) (string, error)) { commitLinked(env, git, "first.txt") }, commit: []string{"--amend"}, editor: keep},
		{name: "--amend --no-edit", setup: func(env *TestEnv, git func([]string, ...string) (string, error)) { commitLinked(env, git, "first.txt") }, commit: []string{"--amend", "--no-edit"}},
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
			if tt.setup != nil {
				tt.setup(env, git)
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
			require.True(t, advCheckpointStored(env, linked[len(linked)-1].String()), "post-commit condenses into the checkpoint the commit links: %q", recorded)
			require.False(t, advCheckpointStored(env, advForeignCheckpoint), "no checkpoint the message only names is created: %q", recorded)
		})
	}
}
