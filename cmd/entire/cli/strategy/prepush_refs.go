package strategy

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/go-git/go-git/v6/plumbing"

	"github.com/entireio/cli/cmd/entire/cli/checkpoint"
	"github.com/entireio/cli/cmd/entire/cli/paths"
)

// PrePushRef is one line of the ref list git writes to a pre-push hook's
// stdin: the local ref and commit being pushed, and the remote ref it updates.
type PrePushRef struct {
	LocalRef  string
	LocalSHA  string
	RemoteRef string
	RemoteSHA string
}

// ParsePrePushRefs reads the "<local ref> <local sha> <remote ref> <remote sha>"
// lines git passes to a pre-push hook and keeps the ones that touch checkpoint
// refs, the only ones Entire needs. It reads to the end with no size cap: a
// `--mirror` of a repository with many tags can be large, and a truncated list
// could hide a checkpoint ref line. Memory stays bounded because other lines
// are dropped as they are read. Malformed lines are skipped; a line too long to
// be a ref line is an error.
func ParsePrePushRefs(r io.Reader) ([]PrePushRef, error) {
	refs := []PrePushRef{}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 4096), 64*1024)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 4 {
			continue
		}
		ref := PrePushRef{LocalRef: fields[0], LocalSHA: fields[1], RemoteRef: fields[2], RemoteSHA: fields[3]}
		if isCheckpointRefName(ref.LocalRef) || isCheckpointRefName(ref.RemoteRef) {
			refs = append(refs, ref)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read pre-push refs: %w", err)
	}
	return refs, nil
}

type prePushRefsKey struct{}

// WithPrePushRefs records the refs the user's own push is about to update.
// Without it (an old hook script, a hook manager line, or a caller that is not
// a hook) the refs are not known, which is not the same as an empty list.
func WithPrePushRefs(ctx context.Context, refs []PrePushRef) context.Context {
	if refs == nil {
		refs = []PrePushRef{} // known to be empty, unlike absent
	}
	return context.WithValue(ctx, prePushRefsKey{}, refs)
}

// isCheckpointRefName reports whether name holds checkpoint content: the v1
// branch or a per-checkpoint ref.
func isCheckpointRefName(name string) bool {
	return name == plumbing.NewBranchReferenceName(paths.MetadataBranchName).String() ||
		strings.HasPrefix(name, checkpoint.CheckpointRefPrefix)
}
