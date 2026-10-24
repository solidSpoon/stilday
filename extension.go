package main

import (
	"bytes"
	_ "embed"
	"os"
	"path/filepath"
)

// stildayExtension is the Pi extension the app loads: day overview, photo
// detail, and the JSONL output tool.
//
//go:embed pi/stilday.ts
var stildayExtension []byte

// installExtension writes the embedded extension where Pi can load it,
// and returns its path.
func installExtension() (string, error) {
	dir := filepath.Join(dataDir(), "pi")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "stilday.ts")
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, stildayExtension) {
		return path, nil
	}
	if err := os.WriteFile(path, stildayExtension, 0o644); err != nil {
		return "", err
	}
	return path, nil
}
