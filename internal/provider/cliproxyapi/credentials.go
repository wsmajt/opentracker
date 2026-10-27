package cliproxyapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/zalando/go-keyring"
	"opentracker/internal/config"
)

const credentialService = "opentracker"
const credentialUserPrefix = "cliproxyapi:"

type CredentialStore interface {
	Put(id, value string) error
	Get(id string) (string, error)
	Delete(id string) error
}

type keyringStore struct{}

func NewCredentialStore() CredentialStore { return keyringStore{} }
func (keyringStore) Put(id, value string) error {
	return keyring.Set(credentialService, credentialUserPrefix+id, value)
}
func (keyringStore) Get(id string) (string, error) {
	return keyring.Get(credentialService, credentialUserPrefix+id)
}
func (keyringStore) Delete(id string) error {
	return keyring.Delete(credentialService, credentialUserPrefix+id)
}

type credentialConfig struct {
	CredentialID string `json:"credentialId"`
}

// ConfiguredWithConfig reports whether a CLIProxyAPI key source is selected.
func ConfiguredWithConfig(cfg *config.Config) bool {
	if strings.TrimSpace(os.Getenv(EnvKey)) != "" {
		return true
	}
	if cfg == nil {
		return false
	}
	_, ok := cfg.Providers["cliproxyapi"]
	return ok
}

func Configured() bool {
	if strings.TrimSpace(os.Getenv(EnvKey)) != "" {
		return true
	}
	cfg, err := config.Load()
	return err == nil && ConfiguredWithConfig(cfg)
}

// ResolveManagementKey returns the environment override or the selected keyring value.
func ResolveManagementKey(cfg *config.Config, store CredentialStore) (string, bool, error) {
	if key := strings.TrimSpace(os.Getenv(EnvKey)); key != "" {
		return key, true, nil
	}
	if cfg == nil {
		return "", false, errors.New("CLIProxyAPI configuration is unavailable")
	}
	raw, ok := cfg.Providers["cliproxyapi"]
	if !ok {
		return "", false, nil
	}
	var selected credentialConfig
	if err := json.Unmarshal(raw, &selected); err != nil || strings.TrimSpace(selected.CredentialID) == "" {
		return "", true, errors.New("CLIProxyAPI credential reference is invalid")
	}
	if store == nil {
		return "", true, errors.New("CLIProxyAPI system keyring is unavailable")
	}
	secret, err := store.Get(selected.CredentialID)
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return "", true, errors.New("CLIProxyAPI key is missing from the system keyring; run 'opentracker login cliproxyapi'")
		}
		return "", true, errors.New("cannot read CLIProxyAPI key from the system keyring; unlock or configure the keyring")
	}
	if strings.TrimSpace(secret) == "" {
		return "", true, errors.New("CLIProxyAPI key in the system keyring is empty")
	}
	return secret, true, nil
}

// SaveManagementKey verifies and stores a new immutable keyring entry before changing config.
func SaveManagementKey(cfg *config.Config, secret string, store CredentialStore) error {
	if cfg == nil || store == nil {
		return errors.New("CLIProxyAPI credential storage is unavailable")
	}
	if strings.TrimSpace(secret) == "" {
		return errors.New("CLIProxyAPI key cannot be empty")
	}
	rawID := make([]byte, 32)
	if _, err := rand.Read(rawID); err != nil {
		return errors.New("cannot create CLIProxyAPI credential reference")
	}
	id := hex.EncodeToString(rawID)
	if err := store.Put(id, secret); err != nil {
		_ = store.Delete(id)
		return errors.New("cannot save CLIProxyAPI key to the system keyring")
	}
	stored, err := store.Get(id)
	if err != nil || stored != secret {
		_ = store.Delete(id)
		return errors.New("cannot verify CLIProxyAPI key in the system keyring")
	}
	oldID := ""
	preserveNewKey := false
	encoded, err := json.Marshal(credentialConfig{CredentialID: id})
	if err != nil {
		return errors.New("cannot encode CLIProxyAPI credential reference")
	}
	if err := config.WithCredentialLock(func() error {
		latest, err := config.Load()
		if err != nil {
			return err
		}
		if latest.Providers == nil {
			latest.Providers = make(map[string]json.RawMessage)
		}
		oldID = ""
		if raw, ok := latest.Providers["cliproxyapi"]; ok {
			var previous credentialConfig
			if json.Unmarshal(raw, &previous) == nil {
				oldID = previous.CredentialID
			}
		}
		latest.Providers["cliproxyapi"] = encoded
		if err := latest.Save(); err != nil {
			active, loadErr := config.Load()
			if loadErr != nil {
				preserveNewKey = true
			}
			if loadErr == nil {
				var current credentialConfig
				if json.Unmarshal(active.Providers["cliproxyapi"], &current) == nil && current.CredentialID == id {
					if cfg.Providers == nil {
						cfg.Providers = make(map[string]json.RawMessage)
					}
					cfg.Providers["cliproxyapi"] = encoded
					return nil
				}
			}
			return fmt.Errorf("cannot save CLIProxyAPI credential reference")
		}
		active, err := config.Load()
		if err != nil {
			preserveNewKey = true
			return errors.New("cannot verify CLIProxyAPI credential reference")
		}
		var current credentialConfig
		if json.Unmarshal(active.Providers["cliproxyapi"], &current) != nil || current.CredentialID != id {
			return errors.New("cannot verify CLIProxyAPI credential reference")
		}
		if cfg.Providers == nil {
			cfg.Providers = make(map[string]json.RawMessage)
		}
		cfg.Providers["cliproxyapi"] = encoded
		return nil
	}); err != nil {
		active, loadErr := config.Load()
		selected := false
		if loadErr == nil {
			var current credentialConfig
			if json.Unmarshal(active.Providers["cliproxyapi"], &current) == nil && current.CredentialID == id {
				selected = true
			}
		}
		if !selected && !preserveNewKey {
			_ = store.Delete(id)
		}
		return err
	}
	if oldID != "" && oldID != id {
		_ = store.Delete(oldID)
	}
	return nil
}
