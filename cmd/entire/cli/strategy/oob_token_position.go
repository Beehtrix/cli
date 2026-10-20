package strategy

import (
	"bytes"
	"encoding/json"
)

// ClaimOutOfBandTokenPosition moves state's stored out-of-band token position
// from `from` to `to`, and reports true, only while state still stores `from`
// (an empty position matches only an empty one). Callers run it under the
// session lock and count usage measured from `from` only when it returns
// true: of two Stops measured from the same position, one counts and the
// other adds nothing, and the position never moves back to an older read.
func ClaimOutOfBandTokenPosition(state *SessionState, from, to json.RawMessage) bool {
	if !samePosition(state.OutOfBandTokenBaseline, from) {
		return false
	}
	state.OutOfBandTokenBaseline = to
	return true
}

// samePosition compares two positions. Saving state re-indents raw JSON, so
// they are compared compacted.
func samePosition(a, b json.RawMessage) bool {
	if len(a) == 0 || len(b) == 0 {
		return len(a) == len(b)
	}
	var ca, cb bytes.Buffer
	if json.Compact(&ca, a) != nil || json.Compact(&cb, b) != nil {
		return false
	}
	return bytes.Equal(ca.Bytes(), cb.Bytes())
}
