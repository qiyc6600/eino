// Package storage provides small file-persistence helpers shared by the
// file-backed store implementations (SessionStore, MemoryStore,
// CheckpointStore decorators).
package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// LoadJSONFile reads a JSON file into v; a missing or empty file leaves v
// untouched (callers start with an empty default state).
func LoadJSONFile(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, v)
}

// WriteJSONFileAtomic writes v as indented JSON via a temp file + rename,
// so a crash never leaves a truncated store file.
func WriteJSONFileAtomic(path string, v any) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
