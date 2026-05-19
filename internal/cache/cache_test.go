package cache

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

type testData struct {
	Message string `json:"message"`
	Value   int    `json:"value"`
}

func TestCache_SetAndGet(t *testing.T) {
	dir := t.TempDir()
	c := New(dir)

	data := testData{Message: "hello", Value: 42}
	if err := c.Set("key1", data, 1*time.Hour); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	var result testData
	if !c.Get("key1", &result) {
		t.Fatal("expected cache hit, got miss")
	}

	if result.Message != "hello" || result.Value != 42 {
		t.Errorf("unexpected data: %+v", result)
	}
}

func TestCache_Get_Miss(t *testing.T) {
	dir := t.TempDir()
	c := New(dir)

	var result testData
	if c.Get("nonexistent", &result) {
		t.Fatal("expected cache miss")
	}
}

func TestCache_Get_Expired(t *testing.T) {
	dir := t.TempDir()
	c := New(dir)

	data := testData{Message: "expired", Value: 0}
	if err := c.Set("expired", data, -1*time.Hour); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	var result testData
	if c.Get("expired", &result) {
		t.Fatal("expected cache miss for expired item")
	}

	// File should be cleaned up
	path := filepath.Join(dir, "expired.json")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("expected expired cache file to be removed")
	}
}

func TestCache_Invalidate(t *testing.T) {
	dir := t.TempDir()
	c := New(dir)

	data := testData{Message: "delete me", Value: 99}
	if err := c.Set("del", data, 1*time.Hour); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	if err := c.Invalidate("del"); err != nil {
		t.Fatalf("Invalidate failed: %v", err)
	}

	var result testData
	if c.Get("del", &result) {
		t.Fatal("expected cache miss after invalidation")
	}
}

func TestCache_Invalidate_NonExistent(t *testing.T) {
	dir := t.TempDir()
	c := New(dir)

	// Should not error when file doesn't exist
	if err := c.Invalidate("missing"); err != nil {
		t.Fatalf("Invalidate on non-existent key should not error: %v", err)
	}
}

func TestCache_Get_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	c := New(dir)

	// Write malformed cache item
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("not json"), 0644); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	var result testData
	if c.Get("bad", &result) {
		t.Fatal("expected cache miss for invalid JSON")
	}
}

func TestCache_Set_CreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "sub", "cache")
	c := New(nested)

	data := testData{Message: "nested", Value: 1}
	if err := c.Set("key", data, 1*time.Hour); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	if _, err := os.Stat(nested); os.IsNotExist(err) {
		t.Fatal("expected cache directory to be created")
	}
}
