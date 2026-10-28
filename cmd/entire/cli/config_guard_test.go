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

	for _, args := range [][]string{{"auth", "status"}, {"login", "--device"}, {"status"}} {
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

	cmd, _, err := NewRootCmd().Find([]string{"auth", "status"})
	if err != nil {
		t.Fatalf("find auth status: %v", err)
	}
	if err := checkConfigBeforeRun(cmd); err != nil {
		t.Fatalf("auth status with ENTIRE_TOKEN: %v", err)
	}
}

// Hooks, help and version skip the check.
func TestCheckConfigBeforeRun_Exemptions(t *testing.T) {
	writeBadConfig(t)

	root := NewRootCmd()
	root.InitDefaultHelpCmd()
	for _, args := range [][]string{{"hooks", "git", "pre-push"}, {"hooks", "claude-code", "stop"}, {"help"}, {"version"}} {
		cmd, _, err := root.Find(args)
		if err != nil {
			t.Fatalf("find %v: %v", args, err)
		}
		if cmd == root {
			t.Fatalf("find %v: got root", args)
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
