package strategy

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
)

// PrePushRef is one line of the ref list git writes to a pre-push hook's
// stdin: the local ref and commit being pushed, and the remote ref it updates.
type PrePushRef struct {
	LocalRef  string
	LocalSHA  string
	RemoteRef string
}

// maxPrePushRefsInput bounds how much of the hook's stdin is read. A ref line
// is under 300 bytes, so this covers tens of thousands of refs.
const maxPrePushRefsInput = 16 << 20

// ParsePrePushRefs reads the "<local ref> <local sha> <remote ref> <remote sha>"
// lines git passes to a pre-push hook. Malformed lines are skipped.
func ParsePrePushRefs(r io.Reader) ([]PrePushRef, error) {
	var refs []PrePushRef
	scanner := bufio.NewScanner(io.LimitReader(r, maxPrePushRefsInput))
	scanner.Buffer(make([]byte, 0, 4096), 64*1024)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 4 {
			continue
		}
		refs = append(refs, PrePushRef{LocalRef: fields[0], LocalSHA: fields[1], RemoteRef: fields[2]})
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
