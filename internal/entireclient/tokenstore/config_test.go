package tokenstore

import (
	"os"
	"path/filepath"
	"testing"
)

// config.json selects the file backend.
func TestFileBackendSelected_ConfigFile(t *testing.T) {
	for _, tc := range []struct {
		name   string
		env    string // empty means unset
		config string
		want   bool
	}{
		{name: "no config", want: false},
		{name: "config file", config: `{"token_store": "file"}`, want: true},
		{name: "config keyring", config: `{"token_store": "keyring"}`, want: false},
		{name: "malformed config", config: `{`, want: false},
		{name: "env wins", env: "keyring", config: `{"token_store": "file"}`, want: false},
		{name: "env file", env: "file", want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("ENTIRE_CONFIG_DIR", dir)
			t.Setenv(BackendEnvVar, tc.env)
			if tc.env == "" {
				if err := os.Unsetenv(BackendEnvVar); err != nil {
					t.Fatal(err)
				}
			}
			if tc.config != "" {
				if err := os.WriteFile(filepath.Join(dir, configFileName), []byte(tc.config), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if got := FileBackendSelected(); got != tc.want {
				t.Errorf("FileBackendSelected() = %v, want %v", got, tc.want)
			}
		})
	}
}
