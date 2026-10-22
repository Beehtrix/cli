package testutil

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/testutil/gitenv"
)

// CommitV1Checkpoints commits a tree on top of parent ("" for a root commit)
// that adds or rewrites each checkpoint's folder the way entire/checkpoints/v1
// lays it out (<id[:2]>/<id[2:]>/metadata.json), and returns the commit hash.
// No ref is moved. The content depends on parent, so writing an ID again in a
// later commit changes its folder, as a real second write does.
func CommitV1Checkpoints(t *testing.T, repoDir, parent string, checkpointIDs ...string) string {
	t.Helper()
	index := filepath.Join(t.TempDir(), "index")
	git := func(stdin string, args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = repoDir
		cmd.Env = append(gitenv.Isolated(), "GIT_INDEX_FILE="+index)
		cmd.Stdin = strings.NewReader(stdin)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return strings.TrimSpace(string(out))
	}

	if parent != "" {
		git("", "read-tree", parent)
	}
	for _, cid := range checkpointIDs {
		blob := git(fmt.Sprintf("{\"checkpoint_id\":%q,\"parent\":%q}\n", cid, parent), "hash-object", "-w", "--stdin")
		git("", "update-index", "--add", "--cacheinfo", "100644,"+blob+","+cid[:2]+"/"+cid[2:]+"/metadata.json")
	}
	args := []string{"commit-tree", git("", "write-tree"), "-m", "checkpoints"}
	if parent != "" {
		args = append(args, "-p", parent)
	}
	return git("", args...)
}
