package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
)


type PlayerType string

const (
	PlayerMPV  PlayerType = "mpv"
	PlayerVLC  PlayerType = "vlc"
	PlayerAuto PlayerType = "auto"
)


func DetectPlayer() PlayerType {
	pref := appConfig.Player
	if pref != "" && pref != "auto" {
		return PlayerType(pref)
	}

	// Auto-detect: prefer mpv > vlc
	if _, err := exec.LookPath("mpv"); err == nil {
		return PlayerMPV
	}

	// Check VLC in common locations
	for _, p := range vlcPaths() {
		if _, err := os.Stat(p); err == nil {
			return PlayerVLC
		}
	}
	if _, err := exec.LookPath("vlc"); err == nil {
		return PlayerVLC
	}

	// Fallback to VLC (user likely has it somewhere)
	return PlayerVLC
}


func vlcPaths() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{"/Applications/VLC.app/Contents/MacOS/VLC"}
	case "windows":
		return []string{
			`C:\Program Files\VideoLAN\VLC\vlc.exe`,
			`C:\Program Files (x86)\VideoLAN\VLC\vlc.exe`,
		}
	default:
		return []string{"/usr/bin/vlc", "/snap/bin/vlc"}
	}
}


func LaunchPlayer(streamURL string, subtitlePath string) (*exec.Cmd, error) {
	player := DetectPlayer()

	switch player {
	case PlayerMPV:
		return launchMPV(streamURL, subtitlePath)
	case PlayerVLC:
		return launchVLC(streamURL, subtitlePath)
	default:
		return launchVLC(streamURL, subtitlePath)
	}
}

func launchMPV(streamURL, subtitlePath string) (*exec.Cmd, error) {
	args := []string{
		streamURL,
		"--cache=yes",
		"--demuxer-max-bytes=500MiB",
		"--demuxer-max-back-bytes=100MiB",
		"--cache-secs=120",
		"--cache-pause-initial=yes",
		"--cache-pause=yes",
		"--title=ZenTorrent",
	}
	if subtitlePath != "" {
		args = append(args, "--sub-file="+subtitlePath)
	}

	cmd := exec.Command("mpv", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Start()
	if err != nil {
		return nil, fmt.Errorf("failed to start mpv: %w", err)
	}
	return cmd, nil
}

func launchVLC(streamURL, subtitlePath string) (*exec.Cmd, error) {
	bin := "vlc"
	for _, p := range vlcPaths() {
		if _, err := os.Stat(p); err == nil {
			bin = p
			break
		}
	}
	if _, err := exec.LookPath("vlc"); err == nil && bin == "vlc" {
		bin = "vlc"
	}

	args := []string{
		streamURL,
		"--network-caching=30000",
		"--file-caching=1000",
		"--disc-caching=1000",
		"--live-caching=1000",
		"--prefetch-buffer-size=131072",
	}
	if subtitlePath != "" {
		args = append(args, "--sub-file="+subtitlePath)
	}

	cmd := exec.Command(bin, args...)
	err := cmd.Start()
	if err != nil {
		return nil, fmt.Errorf("failed to start vlc: %w", err)
	}
	return cmd, nil
}
