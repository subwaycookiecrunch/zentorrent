package main

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
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

	if _, err := exec.LookPath("mpv"); err == nil {
		return PlayerMPV
	}

	for _, p := range vlcPaths() {
		if _, err := os.Stat(p); err == nil {
			return PlayerVLC
		}
	}
	if _, err := exec.LookPath("vlc"); err == nil {
		return PlayerVLC
	}

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

func LaunchPlayer(streamURL string, subtitlePath string, startTimeSec int) (*exec.Cmd, error) {
	player := DetectPlayer()

	switch player {
	case PlayerMPV:
		return launchMPV(streamURL, subtitlePath, startTimeSec)
	case PlayerVLC:
		return launchVLC(streamURL, subtitlePath, startTimeSec)
	default:
		return launchVLC(streamURL, subtitlePath, startTimeSec)
	}
}

func checkStream(url string) bool {
	readyURL := strings.Replace(url, "/stream", "/ready", 1)
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(readyURL)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func playerAlive(cmd *exec.Cmd) bool {
	if cmd == nil || cmd.Process == nil {
		return false
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		if cmd.ProcessState != nil {
			return false
		}
		if runtime.GOOS != "windows" {
			if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
				return false
			}
		}
	}
	return true
}

func launchMPV(streamURL, subtitlePath string, startTimeSec int) (*exec.Cmd, error) {
	os.Remove("/tmp/zt_mpv.sock")

	args := []string{
		streamURL,
		"--cache=yes",
		"--demuxer-max-bytes=500MiB",
		"--demuxer-max-back-bytes=100MiB",
		"--cache-secs=60",
		"--demuxer-readahead-secs=30",
		"--network-timeout=120",
		"--stream-lavf-o=reconnect=1,reconnect_streamed=1,reconnect_delay_max=5",
		"--title=ZenTorrent",
		"--input-ipc-server=/tmp/zt_mpv.sock",
		"--really-quiet",
	}
	if startTimeSec > 0 {
		args = append(args, fmt.Sprintf("--start=%d", startTimeSec))
	}
	if subtitlePath != "" {
		args = append(args, "--sub-file="+subtitlePath)
	}

	cmd := exec.Command("mpv", args...)

	logPath := filepath.Join(os.TempDir(), "zt_mpv.log")
	logFile, err := os.Create(logPath)
	if err == nil {
		cmd.Stderr = logFile
	}

	err = cmd.Start()
	if err != nil {
		if logFile != nil {
			logFile.Close()
		}
		return nil, fmt.Errorf("failed to start mpv: %w", err)
	}

	if !playerAlive(cmd) {
		cmd.Wait()
		if logFile != nil {
			logFile.Close()
			data, _ := os.ReadFile(logPath)
			if len(data) > 0 {
				fmt.Fprintf(os.Stderr, "> mpv error log:\n%s\n", string(data))
			}
		}
		return nil, fmt.Errorf("mpv exited immediately — see %s for details", logPath)
	}

	if logFile != nil {
		logFile.Close()
	}
	return cmd, nil
}

func launchVLC(streamURL, subtitlePath string, startTimeSec int) (*exec.Cmd, error) {
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
		"--network-caching=3000",
		"--file-caching=3000",
		"--disc-caching=1000",
		"--live-caching=1000",
		"--prefetch-buffer-size=131072",
	}
	if startTimeSec > 0 {
		args = append(args, fmt.Sprintf("--start-time=%d", startTimeSec))
	}
	if subtitlePath != "" {
		args = append(args, "--sub-file="+subtitlePath)
	}

	cmd := exec.Command(bin, args...)
	err := cmd.Start()
	if err != nil {
		return nil, fmt.Errorf("failed to start vlc: %w", err)
	}

	if !playerAlive(cmd) {
		cmd.Wait()
		return nil, fmt.Errorf("vlc exited immediately — check stream availability")
	}

	return cmd, nil
}
