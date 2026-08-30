package assets

import (
	"bytes"
	"embed"
	"fmt"
	"image"
	_ "image/png"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed hero.png
var HeroPNG []byte

//go:embed zenplayer/*
var ZenPlayerFS embed.FS

// GetHeroImage returns the decoded hero image
func GetHeroImage() (image.Image, error) {
	img, _, err := image.Decode(bytes.NewReader(HeroPNG))
	return img, err
}

// EnsureZenPlayerDir ensures that ZenPlayer python scripts exist on disk and returns the directory path.
func EnsureZenPlayerDir() (string, error) {
	// 1. If running from repository source with ./zenplayer/zenplayer.py
	if st, err := os.Stat(filepath.Join("zenplayer", "zenplayer.py")); err == nil && !st.IsDir() {
		abs, err := filepath.Abs("zenplayer")
		if err == nil {
			return abs, nil
		}
	}

	// 2. If adjacent to binary
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Join(filepath.Dir(exe), "zenplayer")
		if st, err := os.Stat(filepath.Join(dir, "zenplayer.py")); err == nil && !st.IsDir() {
			return dir, nil
		}
	}

	// 3. User home directory fallback: ~/.zentorrent/zenplayer
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	targetDir := filepath.Join(home, ".zentorrent", "zenplayer")
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create zenplayer dir: %w", err)
	}

	// Extract embedded files into targetDir
	entries, err := fs.ReadDir(ZenPlayerFS, "zenplayer")
	if err != nil {
		return "", fmt.Errorf("failed to read embedded zenplayer files: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := fs.ReadFile(ZenPlayerFS, "zenplayer/"+entry.Name())
		if err != nil {
			continue
		}
		destPath := filepath.Join(targetDir, entry.Name())
		_ = os.WriteFile(destPath, data, 0644)
	}

	return targetDir, nil
}
