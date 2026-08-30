package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/subwaycookiecrunch/zentorrent/internal/assets"
)

// findPythonExecutable searches for an available python runtime across platforms.
func findPythonExecutable(zpDir string) string {
	// 1. Check virtual environments
	var venvCandidates []string
	if runtime.GOOS == "windows" {
		venvCandidates = []string{
			filepath.Join(zpDir, "venv", "Scripts", "python.exe"),
			filepath.Join(zpDir, ".venv", "Scripts", "python.exe"),
		}
	} else {
		venvCandidates = []string{
			filepath.Join(zpDir, "venv", "bin", "python"),
			filepath.Join(zpDir, ".venv", "bin", "python"),
		}
	}

	for _, cand := range venvCandidates {
		if st, err := os.Stat(cand); err == nil && !st.IsDir() {
			return cand
		}
	}

	// 2. Check system PATH
	var sysCandidates []string
	if runtime.GOOS == "windows" {
		sysCandidates = []string{"python.exe", "py.exe", "python3.exe", "python"}
	} else {
		sysCandidates = []string{"python3", "python"}
	}

	for _, name := range sysCandidates {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}

	return ""
}

// openBrowserMusic launches the built-in ZenPlayer Web Audio Studio in the browser.
func openBrowserMusic(query string) error {
	startServicesOrDie(true)
	if err := ensureVODServer(); err != nil {
		return fmt.Errorf("failed to start streaming engine: %w", err)
	}

	url := fmt.Sprintf("http://localhost:%d/?tab=music", appConfig.StreamPort)
	if query != "" {
		url += fmt.Sprintf("&q=%s", query)
	}

	fmt.Println("------------------------------------------------------------")
	fmt.Println("  🎵 ZenPlayer Music & Audio Studio")
	fmt.Println("  Launching in browser: " + url)
	fmt.Println("------------------------------------------------------------")

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
	time.Sleep(1 * time.Second)
	return nil
}

// LaunchZenPlayer launches the ZenPlayer retro cassette music player.
func LaunchZenPlayer(query string) error {
	zpDir, err := assets.EnsureZenPlayerDir()
	if err != nil {
		return openBrowserMusic(query)
	}

	pyBin := findPythonExecutable(zpDir)
	if pyBin == "" {
		// Python not installed on user's machine (e.g. Windows standalone binary user)
		return openBrowserMusic(query)
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
	if runErr := cmd.Run(); runErr != nil {
		// If textual/mpv dependencies fail, fallback to browser studio
		return openBrowserMusic(query)
	}

	// Clean up terminal mode on unix
	if runtime.GOOS != "windows" {
		_ = exec.Command("stty", "sane").Run()
	}
	fmt.Print("\033[?1049l\033[?25h\033[0m\033[2J\033[H\r")

	return nil
}
