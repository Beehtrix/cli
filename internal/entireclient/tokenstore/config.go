package tokenstore

import (
	"encoding/json"

	"github.com/entireio/cli/cmd/entire/cli/osroot"
	"github.com/entireio/cli/internal/entireclient/userdirs"
)

// configFileName holds per-user CLI settings.
const configFileName = "config.json"

// configTokenStore reads token_store; errors mean unset.
func configTokenStore() string {
	root, err := userdirs.ConfigRootForRead()
	if err != nil {
		return ""
	}
	data, err := osroot.ReadFileNoFollow(root, configFileName)
	if err != nil {
		return ""
	}
	var cfg struct {
		TokenStore string `json:"token_store"`
	}
	if json.Unmarshal(data, &cfg) != nil {
		return ""
	}
	return cfg.TokenStore
}
