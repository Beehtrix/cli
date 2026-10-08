package strategy

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	testSHA1 = "1111111111111111111111111111111111111111"
	testSHA2 = "2222222222222222222222222222222222222222"
	zeroSHA  = "0000000000000000000000000000000000000000"
)

func TestParsePrePushRefs(t *testing.T) {
	t.Parallel()
	const v1 = "refs/heads/entire/checkpoints/v1"
	refs, err := ParsePrePushRefs(strings.NewReader(
		"refs/heads/main " + testSHA1 + " refs/heads/main " + testSHA2 + "\n" +
			"malformed line\n" +
			"\n" +
			v1 + " " + testSHA1 + " " + v1 + " " + zeroSHA + "\n" +
			"(delete) " + zeroSHA + " refs/entire/checkpoints/ab/cdef " + testSHA2 + "\n"))
	require.NoError(t, err)
	require.Equal(t, []PrePushRef{
		{LocalRef: v1, LocalSHA: testSHA1, RemoteRef: v1, RemoteSHA: zeroSHA},
		{LocalRef: "(delete)", LocalSHA: zeroSHA, RemoteRef: "refs/entire/checkpoints/ab/cdef", RemoteSHA: testSHA2},
	}, refs, "only lines that touch checkpoint refs are kept")
}

// A `--mirror` of a repository with many tags sends a long ref list; the v1
// line at its end must still be seen, not cut off by a size cap.
func TestParsePrePushRefs_LongListKeepsTheLastLine(t *testing.T) {
	t.Parallel()
	const v1 = "refs/heads/entire/checkpoints/v1"
	var b strings.Builder
	for i := 0; b.Len() < 20<<20; i++ {
		fmt.Fprintf(&b, "refs/tags/t%d %s refs/tags/t%d %s\n", i, testSHA1, i, zeroSHA)
	}
	b.WriteString(v1 + " " + testSHA2 + " " + v1 + " " + zeroSHA + "\n")

	refs, err := ParsePrePushRefs(strings.NewReader(b.String()))
	require.NoError(t, err)
	require.Len(t, refs, 1)
	require.Equal(t, testSHA2, refs[0].LocalSHA)
}
