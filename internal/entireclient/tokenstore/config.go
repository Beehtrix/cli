package tokenstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path/filepath"
	"slices"

	"github.com/entireio/cli/cmd/entire/cli/osroot"
	"github.com/entireio/cli/internal/entireclient/userdirs"
)

// configFileName holds per-user CLI settings.
const configFileName = "config.json"

// ErrBadConfig marks an unusable config.json.
var ErrBadConfig = errors.New("invalid Entire config")

// tokenStoreKey is config.json's only key.
const tokenStoreKey = "token_store"

// CheckConfig reports an unusable config.json.
func CheckConfig() error {
	_, err := configTokenStore()
	return err
}

// ConfigFilePath is config.json's full path.
func ConfigFilePath() string {
	dir, _ := userdirs.ConfigDirChecked() //nolint:errcheck // display only
	return filepath.Join(dir, configFileName)
}

// configTokenStore reads token_store; missing means unset.
func configTokenStore() (string, error) {
	// Bad override: its consumers report it.
	if _, err := userdirs.ConfigDirChecked(); err != nil {
		return "", nil //nolint:nilerr // see above
	}
	root, err := userdirs.ConfigRootForRead()
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", badConfig(err)
	}
	data, err := osroot.ReadFileNoFollow(root, configFileName)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", badConfig(err)
	}
	// Unknown keys fail; json matching ignores case.
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		return "", badConfig(err)
	}
	for _, key := range slices.Sorted(maps.Keys(keys)) {
		if key != tokenStoreKey {
			return "", badConfig(fmt.Errorf("unknown key %q", key))
		}
	}
	var cfg struct {
		TokenStore string `json:"token_store"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return "", badConfig(err)
	}
	switch cfg.TokenStore {
	case "", fileBackend, "keyring":
		return cfg.TokenStore, nil
	}
	return "", badConfig(fmt.Errorf(`token_store is %q, want "file" or "keyring"`, cfg.TokenStore))
}

func badConfig(err error) error {
	return fmt.Errorf("%w %s: %w; fix or remove it", ErrBadConfig, ConfigFilePath(), err)
}
