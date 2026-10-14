//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/execx"
	"github.com/entireio/cli/cmd/entire/cli/testutil"
	"github.com/entireio/cli/internal/entireclient/contexts"
	"github.com/entireio/cli/internal/entireclient/tokenstore"
)

func TestAuthToken_LockDirectoryIsolation(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"set", "empty", "unset"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()

			home := t.TempDir()
			configDir := t.TempDir()
			tokensPath := filepath.Join(t.TempDir(), "tokens.json")
			refreshedJWT := fakeLoginJWT("https://core.example")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != pathOAuthToken || r.FormValue("refresh_token") != "old-refresh" {
					t.Errorf("unexpected refresh request: %s", r.URL.Path)
					http.Error(w, "unexpected request", http.StatusBadRequest)
					return
				}
				writeJSON(t, w, http.StatusOK, map[string]any{
					"access_token": refreshedJWT, "refresh_token": "new-refresh",
					"token_type": "Bearer", "expires_in": 3600,
				})
			}))
			defer server.Close()

			service := tokenstore.CoreKeyringService(server.URL)
			if err := contexts.Save(configDir, &contexts.File{
				CurrentContext: "test",
				Contexts:       []*contexts.Context{{Name: "test", CoreURL: server.URL, Handle: "alice", KeychainService: service}},
			}); err != nil {
				t.Fatal(err)
			}
			expiredJWT := fakeJWT(server.URL, 1)
			data, err := json.Marshal(map[string]map[string]string{
				service:                            {"alice": expiredJWT + tokenstore.TokenExpirationSeparator + "0"},
				tokenstore.RefreshService(service): {"alice": "old-refresh"},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(tokensPath, data, 0o600); err != nil {
				t.Fatal(err)
			}

			if os.Getenv("ENTIRE_AUTH_LOCK_DIR") == "" {
				t.Fatal("test harness did not set ENTIRE_AUTH_LOCK_DIR")
			}
			cmd := execx.NonInteractive(t.Context(), getTestBinary(), "auth", "token", "--insecure-http-auth")
			cmd.Dir = home
			cmd.Env = slices.DeleteFunc(testutil.GitIsolatedEnv(), func(entry string) bool {
				return strings.HasPrefix(entry, "ENTIRE_AUTH_LOCK_DIR=")
			})
			cmd.Env = append(cmd.Env,
				"HOME="+home, "XDG_CACHE_HOME="+home, "LocalAppData="+home,
				"ENTIRE_CONFIG_DIR="+configDir, "ENTIRE_TOKEN_STORE=file", "ENTIRE_TOKEN_STORE_PATH="+tokensPath,
			)
			// HOME, XDG_CACHE_HOME and LocalAppData all point at home, so
			// auth-go's default lock dir lands under it on every OS. The harness
			// lock dir is shared by every spawned binary in this run, so the
			// "set" case gets its own directory to count locks in.
			lockDir := ""
			switch mode {
			case "set":
				lockDir = t.TempDir()
				cmd.Env = append(cmd.Env, "ENTIRE_AUTH_LOCK_DIR="+lockDir)
			case "empty":
				cmd.Env = append(cmd.Env, "ENTIRE_AUTH_LOCK_DIR=")
			}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("auth token: %v\n%s", err, out)
			}
			if got := strings.TrimSpace(string(out)); got != refreshedJWT {
				t.Fatalf("auth token returned %q, want refreshed token", got)
			}
			homeLocks := lockFiles(t, home)
			if mode == "set" {
				if locks := lockFiles(t, lockDir); len(locks) != 1 {
					t.Fatalf("lock files in %s = %v, want exactly one from the refresh", lockDir, locks)
				}
				if len(homeLocks) != 0 {
					t.Fatalf("refresh wrote locks into the default user cache: %v", homeLocks)
				}
			} else if len(homeLocks) != 1 {
				t.Fatalf("lock files under %s = %v, want exactly one in auth-go's default dir", home, homeLocks)
			}
		})
	}
}

func lockFiles(t *testing.T, root string) []string {
	t.Helper()
	var locks []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".lock") {
			locks = append(locks, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return locks
}
