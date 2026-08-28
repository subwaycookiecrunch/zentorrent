package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// findZenPlayerDir locates the zenplayer directory.
func findZenPlayerDir() string {
	// 1. Current working directory ./zenplayer
	if st, err := os.Stat("zenplayer/zenplayer.py"); err == nil && !st.IsDir() {
		abs, err := filepath.Abs("zenplayer")
		if err == nil {
			return abs
		}
	}

	// 2. Relative to executable
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Join(filepath.Dir(exe), "zenplayer")
		if st, err := os.Stat(filepath.Join(dir, "zenplayer.py")); err == nil && !st.IsDir() {
			return dir
		}
	}

	// 3. Known fallback locations
	fallbacks := []string{
		"/Users/raj/Desktop/zentorrent/zenplayer",
		"/Users/raj/.gemini/antigravity/scratch/zenplayer",
	}
	for _, fb := range fallbacks {
		if st, err := os.Stat(filepath.Join(fb, "zenplayer.py")); err == nil && !st.IsDir() {
			return fb
		}
	}

	return ""
}

// LaunchZenPlayer launches the ZenPlayer retro cassette music player.
func LaunchZenPlayer(query string) error {
	zpDir := findZenPlayerDir()
	if zpDir == "" {
		return fmt.Errorf("zenplayer directory not found (expected at ./zenplayer)")
	}

	// Determine python binary
	pyBin := filepath.Join(zpDir, "venv", "bin", "python")
	if _, err := os.Stat(pyBin); err != nil {
		// Try scratch venv if available
		altPy := "/Users/raj/.gemini/antigravity/scratch/zenplayer/venv/bin/python"
		if _, err := os.Stat(altPy); err == nil {
			pyBin = altPy
		} else {
			// Fallback to system python3
			if sysPy, err := exec.LookPath("python3"); err == nil {
				pyBin = sysPy
			} else {
				pyBin = "python"
			}
		}
	}

	scriptPath := filepath.Join(zpDir, "zenplayer.py")
	args := []string{scriptPath}
	if query != "" {
		args = append(args, query)
	}

	cmd := exec.Command(pyBin, args...)
	cmd.Dir = zpDir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Run ZenPlayer with full terminal takeover
	_ = cmd.Run()

	// Ensure terminal screen, cursor, and tty modes are cleanly restored for Bubbletea
	_ = exec.Command("stty", "sane").Run()
	fmt.Print("\033[?1049l\033[?25h\033[0m\033[2J\033[H\r")

	return nil
}
