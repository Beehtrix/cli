package cli

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/entireio/cli/cmd/entire/cli/gitremote"
)

// whereamiOutput is the contract entire-brain's `connect` decodes; the JSON
// tags are lockstep with that consumer (COR-2030). repo_id is the region-local
// ULID entire-api keys repo-scoped routes on, api_url the cell serving it.
type whereamiOutput struct {
	RepoID       string `json:"repo_id"`
	APIURL       string `json:"api_url"`
	Jurisdiction string `json:"jurisdiction"`
}

// newRepoWhereamiCmd is the hidden `repo whereami`: the current repo's hosted
// coordinates for scripts and plugins. Hidden because the only consumers are
// machine callers; the human-facing surface stays uncommitted, which is also
// why output without --json is refused rather than given a table.
func newRepoWhereamiCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "whereami",
		Short:  "Print the current repo's hosted coordinates (repo ULID, cell API URL, jurisdiction)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !jsonRequested(cmd) {
				return errors.New("only --json output is supported")
			}
			ctx := cmd.Context()
			forge, owner, repo, err := gitremote.ResolveRemoteRepo(ctx, "origin")
			if err != nil {
				return fmt.Errorf("resolve origin remote: %w", err)
			}
			// resolveForgeRepoCellPlacement bounds itself with
			// requiredCellResolveTimeout and distinguishes not-logged-in from
			// repo-not-onboarded; both propagate to stderr via main.go.
			// Not re-wrapped: resolveForgeRepoCellPlacement's errors already
			// name the repo, and adding it again here prints it twice.
			placement, err := resolveForgeRepoCellPlacement(ctx, forge, owner, repo)
			if err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(whereamiOutput{
				RepoID:       placement.RepoID,
				APIURL:       placement.Target.BaseURL,
				Jurisdiction: placement.Target.Jurisdiction,
			})
		},
	}
	addJSONFlag(cmd)
	return cmd
}
