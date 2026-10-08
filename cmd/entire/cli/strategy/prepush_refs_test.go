package strategy

import (
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
	refs, err := ParsePrePushRefs(strings.NewReader(
		"refs/heads/main " + testSHA1 + " refs/heads/main " + testSHA2 + "\n" +
			"malformed line\n" +
			"\n" +
			"(delete) " + zeroSHA + " refs/heads/old " + testSHA2 + "\n"))
	require.NoError(t, err)
	require.Equal(t, []PrePushRef{
		{LocalRef: "refs/heads/main", LocalSHA: testSHA1, RemoteRef: "refs/heads/main"},
		{LocalRef: "(delete)", LocalSHA: zeroSHA, RemoteRef: "refs/heads/old"},
	}, refs)
}
