package cache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type item struct {
	Data      json.RawMessage `json:"data"`
	ExpiresAt time.Time       `json:"expires_at"`
}

type Cache struct {
	dir string
}

func New(dir string) *Cache {
	return &Cache{dir: dir}
}

func (c *Cache) Get(key string, dest interface{}) bool {
	path := filepath.Join(c.dir, key+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}

	var it item
	if err := json.Unmarshal(data, &it); err != nil {
		return false
	}

	if time.Now().After(it.ExpiresAt) {
		os.Remove(path)
		return false
	}

	if err := json.Unmarshal(it.Data, dest); err != nil {
		return false
	}

	return true
}

func (c *Cache) Set(key string, data interface{}, ttl time.Duration) error {
	if err := os.MkdirAll(c.dir, 0755); err != nil {
		return fmt.Errorf("cannot create cache dir: %w", err)
	}

	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("cannot marshal cache data: %w", err)
	}

	it := item{
		Data:      raw,
		ExpiresAt: time.Now().Add(ttl),
	}

	path := filepath.Join(c.dir, key+".json")
	fileData, err := json.Marshal(it)
	if err != nil {
		return fmt.Errorf("cannot marshal cache item: %w", err)
	}

	if err := os.WriteFile(path, fileData, 0644); err != nil {
		return fmt.Errorf("cannot write cache file: %w", err)
	}

	return nil
}

func (c *Cache) Invalidate(key string) error {
	path := filepath.Join(c.dir, key+".json")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
