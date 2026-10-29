package cli

import (
	"github.com/entireio/cli/cmd/entire/cli/auth"
	"github.com/entireio/cli/internal/entireclient/tokenstore"

	"github.com/spf13/cobra"
)

// configCheckAnnotation sets how a command checks config.json.
// Checked is the default, skipped when ENTIRE_TOKEN is set. Exempt are the
// hooks and detached internal commands, which must never fail (a typo in
// config.json must not block a commit), and commands that never touch the
// token store, such as help, version, completion and doctor. Commands that
// write the store, login and logout, check even with ENTIRE_TOKEN set. The
// annotation is inherited, so a group root covers the whole group.
// TestConfigCheckAnnotations pins every annotated command.
const (
	configCheckAnnotation = "entire_config_check"
	configCheckSkip       = "skip"
	configCheckAlways     = "always"
)

// exemptFromConfigCheck marks cmd and its children exempt.
func exemptFromConfigCheck(cmd *cobra.Command) *cobra.Command {
	return annotateConfigCheck(cmd, configCheckSkip)
}

// alwaysCheckConfig ignores ENTIRE_TOKEN for cmd.
func alwaysCheckConfig(cmd *cobra.Command) *cobra.Command {
	return annotateConfigCheck(cmd, configCheckAlways)
}

func annotateConfigCheck(cmd *cobra.Command, mode string) *cobra.Command {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[configCheckAnnotation] = mode
	return cmd
}

// checkConfigBeforeRun fails on an unusable config.json.
func checkConfigBeforeRun(cmd *cobra.Command) error {
	for c := cmd; c != nil; c = c.Parent() {
		switch c.Annotations[configCheckAnnotation] {
		case configCheckSkip:
			return nil
		case configCheckAlways:
			return tokenstore.CheckConfig() //nolint:wrapcheck // already names the file
		}
	}
	return auth.CheckTokenStoreConfig() //nolint:wrapcheck // already names the file
}
