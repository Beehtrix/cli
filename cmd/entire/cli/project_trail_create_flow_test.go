package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/entireio/cli/cmd/entire/cli/api"
)

func repositoryTrailCreationHandler(t *testing.T, forge string, inspectChange func(api.ChangeCreateRequest)) http.HandlerFunc {
	t.Helper()
	base := "/api/v1/" + forge + "/acme/trails"
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == base:
			assert.Empty(t, r.Header.Get("Idempotency-Key"), "parent creation must not require project management")
			assert.Equal(t, projectTrailTestProject, r.URL.Query().Get("project_id"))
			var request api.ProjectTrailCreateRequest
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&request))
			assert.Empty(t, request.Changes)
			assert.Equal(t, []string{projectTrailTestChange}, request.RepositoryIDs)
			out := projectTrailTestResource()
			out.Changes = nil
			out.RepositoryIDs = request.RepositoryIDs
			w.Header().Set("ETag", `W/"created-parent"`)
			assert.NoError(t, json.NewEncoder(w).Encode(out))
		case r.Method == http.MethodPost && r.URL.Path == base+"/"+projectTrailTestID+"/changes":
			assert.Equal(t, `W/"created-parent"`, r.Header.Get("If-Match"))
			assert.NotEmpty(t, r.Header.Get("Idempotency-Key"))
			var request api.ChangeCreateRequest
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&request))
			assert.Equal(t, projectTrailTestChange, request.RepositoryID)
			assert.NotEmpty(t, request.BranchName)
			if inspectChange != nil {
				inspectChange(request)
			}
			assert.NoError(t, json.NewEncoder(w).Encode(api.ChangeCreateResponse{ID: projectTrailTestChange, TrailID: projectTrailTestID, RepositoryID: projectTrailTestChange}))
		case r.Method == http.MethodGet && r.URL.Path == base+"/"+projectTrailTestID:
			assert.NoError(t, json.NewEncoder(w).Encode(projectTrailTestResource()))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			http.Error(w, "unexpected request", http.StatusInternalServerError)
		}
	}
}

func TestProjectTrailCreateRetriesOnlyChangeWithLink(t *testing.T) {
	parents, changes := 0, 0
	var first api.ChangeCreateRequest
	_, project := setupRepositoryTrailCreation(t, "gh", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && !strings.HasSuffix(r.URL.Path, "/changes"):
			parents++
			assert.Empty(t, r.Header.Get("Idempotency-Key"))
			w.Header().Set("ETag", `W/"original"`)
			assert.NoError(t, json.NewEncoder(w).Encode(projectTrailTestResource()))
		case r.Method == http.MethodGet:
			w.Header().Set("ETag", `W/"current"`)
			assert.NoError(t, json.NewEncoder(w).Encode(projectTrailTestResource()))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/changes"):
			changes++
			assert.Equal(t, "change-retry", r.Header.Get("Idempotency-Key"))
			var request api.ChangeCreateRequest
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&request))
			if changes == 1 {
				first = request
				http.Error(w, "response lost", http.StatusServiceUnavailable)
				return
			}
			assert.Equal(t, first, request)
			assert.Equal(t, `W/"current"`, r.Header.Get("If-Match"))
			assert.NoError(t, json.NewEncoder(w).Encode(api.ChangeCreateResponse{ID: projectTrailTestChange, TrailID: projectTrailTestID, RepositoryID: projectTrailTestChange}))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
	})
	fields := []string{"--repo", "gh/acme/widget", "--branch", "feature/work", "--branch-action", "create", "--base", "main", "--title", "Intent", "--body", "Plan", "--priority", "high", "--assignee", "alice", "--idempotency-key", "change-retry"}
	_, _, err := executeProjectTrailTest(t, append([]string{"create"}, fields...)...)
	require.ErrorContains(t, err, "entire trail link "+projectTrailTestID)
	_, _, err = executeProjectTrailTest(t, append([]string{"link", projectTrailTestID}, fields...)...)
	require.NoError(t, err)
	require.Equal(t, 1, parents)
	require.Equal(t, 2, changes)
	require.Zero(t, project.resolveCalls)
}

func TestProjectTrailCreateChangeFailurePreservesParent(t *testing.T) {
	for _, failure := range []string{"forbidden", "conflict", "unavailable", "missing etag", "wrong change identity", "read failure"} {
		t.Run(failure, func(t *testing.T) {
			parents, changes := 0, 0
			handler := repositoryTrailCreationHandler(t, "gh", nil)
			setupRepositoryTrailCreation(t, "gh", func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost && !strings.HasSuffix(r.URL.Path, "/changes") {
					parents++
					if failure == "missing etag" {
						assert.NoError(t, json.NewEncoder(w).Encode(projectTrailTestResource()))
						return
					}
				}
				if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/changes") {
					changes++
					switch failure {
					case "forbidden", "conflict", "unavailable":
						status := map[string]int{"forbidden": http.StatusForbidden, "conflict": http.StatusPreconditionFailed, "unavailable": http.StatusServiceUnavailable}[failure]
						http.Error(w, failure, status)
						return
					case "wrong change identity":
						assert.NoError(t, json.NewEncoder(w).Encode(api.ChangeCreateResponse{ID: projectTrailTestChange, TrailID: projectTrailTestProject, RepositoryID: projectTrailTestChange}))
						return
					}
				}
				if r.Method == http.MethodGet && failure == "read failure" {
					http.Error(w, "read failed", http.StatusServiceUnavailable)
					return
				}
				handler(w, r)
			})
			out, errOut, err := executeProjectTrailTest(t, "create", "--repo", "gh/acme/widget", "--title", "Intent", "--branch", "feature/work", "--idempotency-key", "change-retry", "--json")
			require.Error(t, err)
			require.Equal(t, 1, parents)
			require.Contains(t, errOut, projectTrailTestID)
			require.Contains(t, err.Error(), "do not rerun trail create")
			require.Empty(t, out)
			if failure == "missing etag" {
				require.Zero(t, changes)
				require.ErrorContains(t, err, "no ETag")
			} else {
				require.Equal(t, 1, changes)
			}
			if failure != "read failure" {
				require.ErrorContains(t, err, "entire trail link "+projectTrailTestID)
				require.ErrorContains(t, err, "change-retry")
			} else {
				require.ErrorContains(t, err, "was created and branch linked")
			}
		})
	}
}
