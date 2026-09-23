package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

type Config struct {
	Providers map[string]json.RawMessage `json:"providers"`
}

func Load() (*Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cannot get home dir: %w", err)
	}

	configPath := filepath.Join(home, ".config", "opentracker", "config.json")

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{Providers: make(map[string]json.RawMessage)}, nil
		}
		return nil, fmt.Errorf("cannot read config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("cannot parse config: %w", err)
	}

	return &cfg, nil
}

func (c *Config) UpdateProvider(name string, raw json.RawMessage) error {
	if c.Providers == nil {
		c.Providers = make(map[string]json.RawMessage)
	}
	c.Providers[name] = raw
	return c.Save()
}

func (c *Config) Save() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot get home dir: %w", err)
	}

	parentDir := filepath.Join(home, ".config")
	configDir := filepath.Join(parentDir, "opentracker")
	if err := ensureConfigDir(parentDir, configDir); err != nil {
		return fmt.Errorf("cannot create config dir: %w", err)
	}

	configPath := filepath.Join(configDir, "config.json")
	if info, err := os.Lstat(configPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("cannot write config: config path is a symlink")
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("cannot write config: config path is not a regular file")
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("cannot inspect config path: %w", err)
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("cannot marshal config: %w", err)
	}

	temp, err := os.CreateTemp(configDir, ".config.json-*")
	if err != nil {
		return fmt.Errorf("cannot create temporary config: %w", err)
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()

	if err := temp.Chmod(0600); err != nil {
		_ = temp.Close()
		return fmt.Errorf("cannot set temporary config permissions: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return fmt.Errorf("cannot write temporary config: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("cannot sync temporary config: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("cannot close temporary config: %w", err)
	}
	if err := os.Rename(tempPath, configPath); err != nil {
		return fmt.Errorf("cannot replace config: %w", err)
	}

	if runtime.GOOS != "windows" {
		dir, err := os.Open(configDir)
		if err != nil {
			return fmt.Errorf("config replaced but cannot open config dir for sync: %w", err)
		}
		if err := dir.Sync(); err != nil {
			_ = dir.Close()
			return fmt.Errorf("config replaced but cannot sync config dir: %w", err)
		}
		if err := dir.Close(); err != nil {
			return fmt.Errorf("config replaced but cannot close config dir: %w", err)
		}
	}

	return nil
}

func ensureConfigDir(parentDir, configDir string) error {
	if err := os.MkdirAll(parentDir, 0700); err != nil {
		return err
	}
	if info, err := os.Lstat(parentDir); err != nil {
		return err
	} else if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("config parent is not a real directory")
	}

	if err := os.Mkdir(configDir, 0700); err != nil && !os.IsExist(err) {
		return err
	}
	info, err := os.Lstat(configDir)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("config directory is not a real directory")
	}
	return os.Chmod(configDir, 0700)
}
