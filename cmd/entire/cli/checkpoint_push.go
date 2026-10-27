package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/spf13/cobra"

	"github.com/entireio/cli/cmd/entire/cli/checkpoint"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint/id"
	"github.com/entireio/cli/cmd/entire/cli/interactive"
	"github.com/entireio/cli/cmd/entire/cli/strategy"
)

type checkpointPushFlags struct {
	all  bool
	json bool
}

// checkpointPushJSON is `entire checkpoint push --json`. Results is never null.
type checkpointPushJSON struct {
	Remote  string                     `json:"checkpoint_sync_remote"`
	Results []checkpointPushResultJSON `json:"results"`
}

type checkpointPushResultJSON struct {
	CheckpointID    string `json:"checkpoint_id,omitempty"`
	Ref             string `json:"ref"`
	Commit          string `json:"commit,omitempty"`
	RequestedCommit string `json:"requested_commit,omitempty"`
	Status          string `json:"status"`
	Error           string `json:"error,omitempty"`
}

func newCheckpointPushCmd() *cobra.Command {
	return newCheckpointPushCmdWithPrompt(interactive.CanPromptInteractively)
}

// newCheckpointPushCmdWithPrompt takes the "is a person at a terminal?" probe
// so tests can exercise both paths.
func newCheckpointPushCmdWithPrompt(canPrompt func() bool) *cobra.Command {
	var flags checkpointPushFlags
	cmd := &cobra.Command{
		Use:   "push [<checkpoint-id>[@<commit>]...]",
		Short: "Upload waiting checkpoints now",
		Long: `Upload the named checkpoints to the checkpoint sync remote now, the same way
git push would. Use it when the clone's checkpoint push policy is manual, so
git push leaves checkpoints on this machine, or to upload ahead of your next
push under automatic.

Each checkpoint is uploaded at its current version, with every earlier version
not yet uploaded. Run 'entire checkpoint list --outgoing' to see what is
waiting. <checkpoint-id>@<commit> uploads that checkpoint only while <commit>
is still its current version: if it changed since you looked, it is not sent.

--all and a bare <checkpoint-id> need a terminal. Without one, name each
checkpoint as <checkpoint-id>@<commit>, using the commit --outgoing --json
reports.

Examples:
  entire checkpoint push <id>
  entire checkpoint push --all
  entire checkpoint push <id>@<commit> --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			return runCheckpointPush(cmd, args, flags, canPrompt())
		},
	}
	cmd.Flags().BoolVar(&flags.all, "all", false, "Upload every waiting checkpoint (needs a terminal)")
	cmd.Flags().BoolVar(&flags.json, "json", false, "Output as JSON")
	return cmd
}

func runCheckpointPush(cmd *cobra.Command, args []string, flags checkpointPushFlags, canPrompt bool) error {
	ctx := cmd.Context()
	out, errOut := cmd.OutOrStdout(), cmd.ErrOrStderr()
	if flags.all && len(args) > 0 {
		return errors.New("--all cannot be combined with checkpoint IDs")
	}
	if checkDisabledGuard(ctx, errOut) {
		return NewSilentError(errors.New("entire is disabled in this repository"))
	}

	var requests []strategy.CheckpointPushRequest
	switch {
	case flags.all:
		if !canPrompt {
			return refuseCheckpointPushWithoutTerminal(errOut, "--all")
		}
		outgoing, err := strategy.ListOutgoingCheckpoints(ctx)
		if err != nil {
			return reportOutgoingErr(errOut, err)
		}
		for _, oc := range outgoing.Checkpoints {
			requests = append(requests, strategy.CheckpointPushRequest{Ref: oc.Ref})
		}
	case len(args) == 0:
		return printWaitingForCheckpointPush(cmd, errOut)
	default:
		parsed, err := parseCheckpointPushArgs(args, canPrompt, errOut)
		if err != nil {
			return err
		}
		requests = parsed
	}
	if len(requests) == 0 {
		fmt.Fprintln(errOut, "No checkpoints are waiting to upload.")
		if flags.json {
			return writeCheckpointPushJSON(out, strategy.CheckpointPushReport{})
		}
		return nil
	}

	report, err := strategy.PushCheckpoints(ctx, requests)
	if err != nil {
		if errors.Is(err, strategy.ErrCheckpointPushDisabled) {
			fmt.Fprintln(errOut, "Checkpoint pushing is disabled in settings (push_sessions=false), so nothing was uploaded.")
			return NewSilentError(err)
		}
		return reportOutgoingErr(errOut, err)
	}

	if flags.json {
		if err := writeCheckpointPushJSON(out, report); err != nil {
			return err
		}
	} else {
		writeCheckpointPushText(out, report)
	}
	for _, outcome := range report.Outcomes {
		if outcome.Status != strategy.CheckpointPushUploaded {
			return NewSilentError(errors.New("some checkpoints were not uploaded"))
		}
	}
	return nil
}

// parseCheckpointPushArgs reads <id> and <id>@<commit>. Without a terminal
// every argument must pin a commit, so a caller sends only a version it saw.
func parseCheckpointPushArgs(args []string, canPrompt bool, errOut io.Writer) ([]strategy.CheckpointPushRequest, error) {
	requests := make([]strategy.CheckpointPushRequest, 0, len(args))
	for _, arg := range args {
		rawID, rawCommit, pinned := strings.Cut(arg, "@")
		if !pinned && !canPrompt {
			return nil, refuseCheckpointPushWithoutTerminal(errOut, arg)
		}
		cid, err := id.NewCheckpointID(rawID)
		if err != nil {
			return nil, fmt.Errorf("invalid checkpoint ID %q: pass the full ID (prefixes are not accepted)", rawID)
		}
		ref, err := checkpoint.RefName(cid)
		if err != nil {
			return nil, fmt.Errorf("checkpoint ref for %s: %w", cid, err)
		}
		request := strategy.CheckpointPushRequest{Ref: ref}
		if pinned {
			// The zero hash is how a request says "not pinned".
			if !plumbing.IsHash(rawCommit) || plumbing.NewHash(rawCommit).IsZero() {
				return nil, fmt.Errorf("invalid commit %q in %q: pass the full commit from 'entire checkpoint list --outgoing --json'", rawCommit, arg)
			}
			request.Commit = plumbing.NewHash(rawCommit)
		}
		requests = append(requests, request)
	}
	return requests, nil
}

func refuseCheckpointPushWithoutTerminal(errOut io.Writer, what string) error {
	fmt.Fprintf(errOut, "entire checkpoint push %s needs a terminal. Without one, name each checkpoint as <checkpoint-id>@<commit>, using the commit 'entire checkpoint list --outgoing --json' reports.\n", what)
	return NewSilentError(errors.New("checkpoint push needs exact versions without a terminal"))
}

// printWaitingForCheckpointPush answers a bare `entire checkpoint push`: it
// names what is waiting and how to send it, and exits non-zero because nothing
// was uploaded.
func printWaitingForCheckpointPush(cmd *cobra.Command, errOut io.Writer) error {
	outgoing, err := strategy.ListOutgoingCheckpoints(cmd.Context())
	if err != nil {
		return reportOutgoingErr(errOut, err)
	}
	if len(outgoing.Checkpoints) == 0 {
		fmt.Fprintln(errOut, "No checkpoints are waiting to upload.")
		return NewSilentError(errors.New("no checkpoints named"))
	}
	fmt.Fprintf(errOut, "%d waiting to upload:\n", len(outgoing.Checkpoints))
	for _, oc := range outgoing.Checkpoints {
		fmt.Fprintf(errOut, "  %s\n", checkpointPushLabel(oc.CheckpointID, oc.Ref))
	}
	fmt.Fprintln(errOut, "Upload with 'entire checkpoint push <checkpoint-id>@<commit>' or 'entire checkpoint push --all'.")
	fmt.Fprintln(errOut, "See each one with 'entire checkpoint list --outgoing'.")
	return NewSilentError(errors.New("no checkpoints named"))
}

func checkpointPushLabel(cid id.CheckpointID, ref plumbing.ReferenceName) string {
	if cid.IsEmpty() {
		return ref.String()
	}
	return cid.String()
}

func checkpointPushResults(report strategy.CheckpointPushReport) []checkpointPushResultJSON {
	results := make([]checkpointPushResultJSON, 0, len(report.Outcomes))
	for _, outcome := range report.Outcomes {
		result := checkpointPushResultJSON{
			Ref:    outcome.Target.Ref.String(),
			Status: string(outcome.Status),
		}
		if cid, ok := checkpoint.ParseRef(outcome.Target.Ref); ok {
			result.CheckpointID = cid.String()
		}
		if !outcome.Commit.IsZero() {
			result.Commit = outcome.Commit.String()
		}
		if !outcome.Target.Commit.IsZero() {
			result.RequestedCommit = outcome.Target.Commit.String()
		}
		if outcome.Err != nil {
			result.Error = outcome.Err.Error()
		}
		results = append(results, result)
	}
	return results
}

func writeCheckpointPushJSON(w io.Writer, report strategy.CheckpointPushReport) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(checkpointPushJSON{Remote: report.Remote, Results: checkpointPushResults(report)}); err != nil {
		return fmt.Errorf("write checkpoint push JSON: %w", err)
	}
	return nil
}

func writeCheckpointPushText(w io.Writer, report strategy.CheckpointPushReport) {
	for _, result := range checkpointPushResults(report) {
		label := result.CheckpointID
		if label == "" {
			label = result.Ref
		}
		switch strategy.CheckpointPushStatus(result.Status) {
		case strategy.CheckpointPushUploaded:
			fmt.Fprintf(w, "%s  uploaded to %s\n", label, report.Remote)
		case strategy.CheckpointPushChanged:
			fmt.Fprintf(w, "%s  not sent: it changed since you looked; it is now %s. Check it with 'entire checkpoint list --outgoing'.\n", label, result.Commit)
		case strategy.CheckpointPushNotWaiting:
			fmt.Fprintf(w, "%s  not sent: it is not waiting to upload\n", label)
		case strategy.CheckpointPushFailed:
			fmt.Fprintf(w, "%s  failed: %s; it stays queued\n", label, result.Error)
		}
	}
}
