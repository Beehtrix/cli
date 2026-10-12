package tokenstore

import (
	"errors"
	"testing"
)

func TestCheckNotFound(t *testing.T) {
	t.Parallel()

	other := errors.New("backend exploded")
	tests := []struct {
		name      string
		val       string
		err       error
		reachable bool
		wantVal   string
		wantErr   error
		wantProbe bool
	}{
		{name: "found", val: "tok", reachable: false, wantVal: "tok"},
		{name: "not found, keyring reachable", err: ErrNotFound, reachable: true, wantErr: ErrNotFound, wantProbe: true},
		{name: "not found, keyring unreachable", err: ErrNotFound, reachable: false, wantErr: ErrKeyringUnavailable, wantProbe: true},
		{name: "other error passes through", err: other, reachable: false, wantErr: other},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			probed := false
			got, err := checkNotFound(tt.val, tt.err, func() bool {
				probed = true
				return tt.reachable
			})
			if got != tt.wantVal {
				t.Errorf("val = %q, want %q", got, tt.wantVal)
			}
			if !errors.Is(err, tt.wantErr) || (tt.wantErr == nil && err != nil) {
				t.Errorf("err = %v, want %v", err, tt.wantErr)
			}
			// An unreachable keyring is not a "not found": callers that
			// tolerate a missing slot must see it as a store failure.
			if errors.Is(err, ErrKeyringUnavailable) && errors.Is(err, ErrNotFound) {
				t.Errorf("err = %v must not also match ErrNotFound", err)
			}
			if probed != tt.wantProbe {
				t.Errorf("probed = %v, want %v", probed, tt.wantProbe)
			}
		})
	}
}
