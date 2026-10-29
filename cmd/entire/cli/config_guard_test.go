package cli

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/auth"
	"github.com/entireio/cli/internal/entireclient/tokenstore"
	"github.com/entireio/cli/internal/entireclient/userdirs"

	"github.com/spf13/cobra"
)

// configCheckAnnotations pins every annotated command and why.
// TestConfigCheckAnnotations fails on a missing or stale entry, so an
// exemption cannot be added quietly to make a failing test pass.
var configCheckAnnotations = map[string]struct{ mode, reason string }{
	"entire hooks":                      {configCheckSkip, "agent and git hooks must never exit non-zero"},
	"entire __send_analytics":           {configCheckSkip, "detached; nobody sees its failure"},
	"entire __sweep_sessions":           {configCheckSkip, "detached from session-start; nobody sees its failure"},
	"entire __refresh_trail_enablement": {configCheckSkip, "detached; fails before the logger starts"},
	"entire curl-bash-post-install":     {configCheckSkip, "install.sh runs it under set -e; a re-install must not report failure"},
	"entire completion":                 {configCheckSkip, "users eval it from a shell rc; never touches the token store"},
	"entire help":                       {configCheckSkip, "prints help; never touches the token store"},
	"entire version":                    {configCheckSkip, "prints build information; never touches the token store"},
	"entire doctor":                     {configCheckSkip, "diagnostic; must run on a broken setup"},
	"entire login":                      {configCheckAlways, "saves to the store even with ENTIRE_TOKEN set"},
	"entire logout":                     {configCheckAlways, "deletes from the store even with ENTIRE_TOKEN set"},
	"entire auth login":                 {configCheckAlways, "saves to the store even with ENTIRE_TOKEN set"},
	"entire auth logout":                {configCheckAlways, "deletes from the store even with ENTIRE_TOKEN set"},
}

// collectAnnotated maps command paths to their own key value.
func collectAnnotated(root *cobra.Command, key string) map[string]string {
	found := map[string]string{}
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		if v := c.Annotations[key]; v != "" {
			found[c.CommandPath()] = v
		}
		for _, child := range c.Commands() {
			walk(child)
		}
	}
	walk(root)
	return found
}

func TestConfigCheckAnnotations(t *testing.T) {
	t.Parallel()

	root := NewRootCmd()
	root.InitDefaultHelpCmd()
	got := collectAnnotated(root, configCheckAnnotation)

	for _, path := range slices.Sorted(maps.Keys(got)) {
		want, ok := configCheckAnnotations[path]
		switch {
		case !ok:
			t.Errorf("%q has config check mode %q but is not in configCheckAnnotations.\n"+
				"Add an entry saying why, or drop the annotation.", path, got[path])
		case want.mode != got[path]:
			t.Errorf("%q has config check mode %q, ledger says %q", path, got[path], want.mode)
		}
	}
	for path, want := range configCheckAnnotations {
		if _, ok := got[path]; !ok {
			t.Errorf("configCheckAnnotations lists %q, but no such command carries the annotation", path)
		}
		if strings.TrimSpace(want.reason) == "" {
			t.Errorf("entry for %q has no reason", path)
		}
	}
}

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

// executeRoot runs the real root command.
func executeRoot(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := NewRootCmd()
	root.SetIn(strings.NewReader(""))
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}

// A bad config.json fails before RunE.
func TestRoot_BadConfigFailsEarly(t *testing.T) {
	writeBadConfig(t)
	t.Chdir(t.TempDir())

	for _, args := range [][]string{{"auth", "status"}, {"login", "--device"}, {"status"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			out, err := executeRoot(t, args...)
			if !errors.Is(err, tokenstore.ErrBadConfig) {
				t.Fatalf("err = %v, want ErrBadConfig", err)
			}
			if out != "" {
				t.Errorf("command ran and printed %q", out)
			}
		})
	}
}

// ENTIRE_TOKEN skips the check, except for login/logout.
func TestRoot_EnvTokenSkipsConfigCheck(t *testing.T) {
	writeBadConfig(t)
	t.Setenv(auth.EnvTokenVar, "x")
	t.Chdir(t.TempDir())

	cmd, _, err := NewRootCmd().Find([]string{"auth", "status"})
	if err != nil {
		t.Fatalf("find auth status: %v", err)
	}
	if err := checkConfigBeforeRun(cmd); err != nil {
		t.Fatalf("auth status with ENTIRE_TOKEN: %v", err)
	}

	for _, args := range [][]string{{"login", "--device"}, {"auth", "login", "--device"}, {"logout"}, {"auth", "logout"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			out, err := executeRoot(t, args...)
			if !errors.Is(err, tokenstore.ErrBadConfig) {
				t.Fatalf("err = %v, want ErrBadConfig", err)
			}
			if out != "" {
				t.Errorf("command ran and printed %q", out)
			}
		})
	}
}

// Exempt commands run despite a bad config.json.
func TestRoot_ExemptCommandsRun(t *testing.T) {
	writeBadConfig(t)
	t.Chdir(t.TempDir())

	for _, args := range [][]string{{"completion", "zsh"}, {"hooks", "git", "pre-push", "origin"}, {"help"}, {"version"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			if _, err := executeRoot(t, args...); err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
		})
	}
}
