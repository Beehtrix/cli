package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/entireio/cli/internal/entireclient/userdirs"
)

// setupPluginUpdateCheck isolates the managed plugin dir and the global
// config dir (where the check's cache lives), installs a manifest for
// "brain" at v0.1.0, and stubs the remote tag listing.
func setupPluginUpdateCheck(t *testing.T, manifest *PluginManifest, tags []string, tagsErr error) *int {
	t.Helper()
	withPluginDir(t)
	t.Setenv(userdirs.EnvConfigDir, t.TempDir())
	if manifest != nil {
		if err := SavePluginManifest(manifest); err != nil {
			t.Fatalf("SavePluginManifest: %v", err)
		}
	}
	calls := 0
	orig := listPluginTagsForUpdateCheck
	listPluginTagsForUpdateCheck = func(context.Context, string) ([]string, error) {
		calls++
		return tags, tagsErr
	}
	t.Cleanup(func() { listPluginTagsForUpdateCheck = orig })
	return &calls
}

func brainManifest() *PluginManifest {
	return &PluginManifest{Name: "brain", RepoURL: "https://github.com/entireio/entire-brain", Tag: "v0.1.0"}
}

func TestPluginUpdateCheck_NotifiesWhenOutdated(t *testing.T) { //nolint:paralleltest // mutates env
	calls := setupPluginUpdateCheck(t, brainManifest(), []string{"v0.2.0", "v0.1.0"}, nil)

	var out strings.Builder
	checkPluginUpdateAndNotify(t.Context(), &out, "brain")

	if *calls != 1 {
		t.Fatalf("tag listings = %d, want 1", *calls)
	}
	got := out.String()
	for _, want := range []string{"Plugin update available! brain 0.1.0 -> 0.2.0", "entire plugin upgrade brain"} {
		if !strings.Contains(got, want) {
			t.Errorf("notice %q missing %q", got, want)
		}
	}
}

func TestPluginUpdateCheck_SilentWhenUpToDate(t *testing.T) { //nolint:paralleltest // mutates env
	// "0.1.0" vs installed "v0.1.0": equal under semver, so no notice —
	// same spelling tolerance UpgradeInstalledPlugin has.
	setupPluginUpdateCheck(t, brainManifest(), []string{"0.1.0"}, nil)

	var out strings.Builder
	checkPluginUpdateAndNotify(t.Context(), &out, "brain")

	if out.Len() != 0 {
		t.Fatalf("expected no output, got %q", out.String())
	}
}

func TestPluginUpdateCheck_CacheSuppressesRecheck(t *testing.T) { //nolint:paralleltest // mutates env
	calls := setupPluginUpdateCheck(t, brainManifest(), []string{"v0.2.0"}, nil)

	var first, second strings.Builder
	checkPluginUpdateAndNotify(t.Context(), &first, "brain")
	checkPluginUpdateAndNotify(t.Context(), &second, "brain")

	if *calls != 1 {
		t.Fatalf("tag listings = %d, want 1 (second call must hit the 24h cache)", *calls)
	}
	if second.Len() != 0 {
		t.Fatalf("expected no output on cached call, got %q", second.String())
	}
}

func TestPluginUpdateCheck_FetchFailureIsSilentAndStampsCache(t *testing.T) { //nolint:paralleltest // mutates env
	calls := setupPluginUpdateCheck(t, brainManifest(), nil, errors.New("network down"))

	var out strings.Builder
	checkPluginUpdateAndNotify(t.Context(), &out, "brain")
	checkPluginUpdateAndNotify(t.Context(), &out, "brain")

	if out.Len() != 0 {
		t.Fatalf("expected no output, got %q", out.String())
	}
	if *calls != 1 {
		t.Fatalf("tag listings = %d, want 1 (a failed check must not retry within 24h)", *calls)
	}
	state := loadPluginVersionCache().Plugins["brain"]
	if time.Since(state.LastCheckTime) > time.Minute {
		t.Fatalf("cache not stamped after failed fetch: %v", state.LastCheckTime)
	}
}

func TestPluginUpdateCheck_SkipsPinned(t *testing.T) { //nolint:paralleltest // mutates env
	m := brainManifest()
	m.Pinned = true
	calls := setupPluginUpdateCheck(t, m, []string{"v9.9.9"}, nil)

	var out strings.Builder
	checkPluginUpdateAndNotify(t.Context(), &out, "brain")

	if *calls != 0 || out.Len() != 0 {
		t.Fatalf("pinned plugin checked remote (%d calls) or printed %q", *calls, out.String())
	}
}

func TestPluginUpdateCheck_SkipsWithoutManifest(t *testing.T) { //nolint:paralleltest // mutates env
	calls := setupPluginUpdateCheck(t, nil, []string{"v9.9.9"}, nil)

	var out strings.Builder
	checkPluginUpdateAndNotify(t.Context(), &out, "brain")

	if *calls != 0 || out.Len() != 0 {
		t.Fatalf("manifest-less plugin checked remote (%d calls) or printed %q", *calls, out.String())
	}
}
