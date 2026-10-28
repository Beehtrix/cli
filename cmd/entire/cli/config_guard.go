package cli

import (
	"github.com/entireio/cli/cmd/entire/cli/auth"

	"github.com/spf13/cobra"
)

// skipConfigCheckAnnotation exempts a command from the config.json check.
// Checked is the default. Exempt are the hooks, which promise never to
// fail (a typo in config.json must not block a commit), and help and
// version, which never touch the token store. Set it on a group root to
// cover the whole group.
const (
	skipConfigCheckAnnotation = "entire_skip_config_check"
	skipConfigCheckEnabled    = "true"
)

// exemptFromConfigCheck marks cmd and its children exempt.
func exemptFromConfigCheck(cmd *cobra.Command) *cobra.Command {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[skipConfigCheckAnnotation] = skipConfigCheckEnabled
	return cmd
}

// checkConfigBeforeRun fails on an unusable config.json.
func checkConfigBeforeRun(cmd *cobra.Command) error {
	for c := cmd; c != nil; c = c.Parent() {
		if c.Annotations[skipConfigCheckAnnotation] == skipConfigCheckEnabled {
			return nil
		}
	}
	return auth.CheckTokenStoreConfig() //nolint:wrapcheck // already names the file
}
