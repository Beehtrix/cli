package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/entireio/cli/cmd/entire/cli/api"
	"github.com/entireio/cli/internal/coreapi"
)

func createRepositoryScopedProjectTrail(cmd *cobra.Command, request api.ProjectTrailCreateRequest, key string) error {
	target, repoID, err := resolveRepositoryTrailCreationTarget(cmd)
	if err != nil {
		return err
	}
	request.RepositoryIDs = []string{repoID}
	path := target.BasePath + "?" + url.Values{"project_id": {target.ProjectID}}.Encode()
	var headers http.Header
	if key != "" {
		headers = http.Header{"Idempotency-Key": {projectTrailIdempotencyKey(cmd.ErrOrStderr(), key)}}
	}
	var out api.ProjectTrail
	if _, err := target.Client.ProjectTrailRequest(cmd.Context(), http.MethodPost, path, request, headers, &out); err != nil {
		if key != "" {
			return fmt.Errorf("create repository-scoped trail (retry with --idempotency-key %s and the same fields): %w", key, err)
		}
		return fmt.Errorf("create repository-scoped trail: %w", err)
	}
	if err := target.validateResponse(out); err != nil {
		return err
	}
	return printProjectTrail(cmd, out)
}

// Repository access supplies placement and ownership without project inspection.
func resolveRepositoryTrailCreationTarget(cmd *cobra.Command) (*projectTrailTarget, string, error) {
	ctx, cancel := context.WithTimeout(cmd.Context(), requiredCellResolveTimeout)
	defer cancel()
	forge, owner, repo, err := resolveTrailRepoOrRemote(ctx, trailRepoFlag(cmd))
	if err != nil {
		return nil, "", err
	}
	host, project, err := trailProjectReferenceOr(cmd, forge, owner)
	if err != nil {
		return nil, "", err
	}
	if host != forge || !strings.EqualFold(project, owner) {
		return nil, "", errors.New("--project must match the scoped repository's namespace")
	}
	client, repoID, err := newTrailAPIClient(ctx, trailInsecureHTTP(cmd), forge, owner, repo)
	if err != nil {
		return nil, "", renderDataAPIAuthError(ctx, cmd.ErrOrStderr(), owner+"/"+repo, err)
	}
	core, err := newCellCoreClient()
	if err != nil {
		return nil, "", renderDataAPIAuthError(ctx, cmd.ErrOrStderr(), owner+"/"+repo, err)
	}
	repository, err := core.GetRepo(ctx, coreapi.GetRepoParams{RepoId: repoID})
	if err != nil {
		return nil, "", renderDataAPIAuthError(ctx, cmd.ErrOrStderr(), owner+"/"+repo, fmt.Errorf("resolve repository ownership: %w", err))
	}
	if repository == nil || repository.Response.ID != repoID || !looksLikeULID(repoID) {
		return nil, "", errors.New("repository response identity does not match the request")
	}
	target, err := projectTrailTargetForReference(api.TrailParentReference{
		ProjectID: repository.Response.OwningProjectId, Host: host, Project: project,
	})
	if err != nil {
		return nil, "", err
	}
	target.Client = client
	return target, repoID, nil
}
