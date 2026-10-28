package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/auth"
	"github.com/entireio/cli/internal/entireclient/tokenstore"
	"github.com/entireio/cli/internal/entireclient/userdirs"
)

// writeBadConfig isolates a broken config.json.
func writeBadConfig(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv(userdirs.EnvConfigDir, dir)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"tokenstore": "file"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(auth.EnvTokenVar, "")
	if err := os.Unsetenv(auth.EnvTokenVar); err != nil {
		t.Fatal(err)
	}
}

// A bad config.json fails before RunE.
func TestRoot_BadConfigFailsEarly(t *testing.T) {
	writeBadConfig(t)
	t.Chdir(t.TempDir())

	for _, args := range [][]string{{"auth", "status"}, {"login", "--device"}, {"version"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			root := NewRootCmd()
			root.SetOut(&stdout)
			root.SetErr(&stderr)
			root.SetArgs(args)

			err := root.ExecuteContext(context.Background())
			if !errors.Is(err, tokenstore.ErrBadConfig) {
				t.Fatalf("err = %v, want ErrBadConfig", err)
			}
			if out := stdout.String() + stderr.String(); out != "" {
				t.Errorf("command ran and printed %q", out)
			}
		})
	}
}

// ENTIRE_TOKEN skips the config.json check.
func TestRoot_EnvTokenSkipsConfigCheck(t *testing.T) {
	writeBadConfig(t)
	t.Setenv(auth.EnvTokenVar, "x")
	t.Chdir(t.TempDir())

	root := NewRootCmd()
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"version"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("version with ENTIRE_TOKEN: %v", err)
	}
}

// Hooks never fail on config.json.
func TestCheckConfigBeforeRun_HooksExempt(t *testing.T) {
	writeBadConfig(t)

	root := NewRootCmd()
	for _, args := range [][]string{{"hooks", "git", "pre-push"}, {"hooks", "claude-code", "stop"}} {
		cmd, _, err := root.Find(args)
		if err != nil {
			t.Fatalf("find %v: %v", args, err)
		}
		if err := checkConfigBeforeRun(cmd); err != nil {
			t.Errorf("%q: err = %v, want nil", cmd.CommandPath(), err)
		}
	}
	cmd, _, err := root.Find([]string{"auth", "status"})
	if err != nil {
		t.Fatalf("find auth status: %v", err)
	}
	if err := checkConfigBeforeRun(cmd); !errors.Is(err, tokenstore.ErrBadConfig) {
		t.Errorf("%q: err = %v, want ErrBadConfig", cmd.CommandPath(), err)
	}
}
