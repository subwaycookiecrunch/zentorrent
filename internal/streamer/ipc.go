package streamer

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"
)

const mpvSocketPath = "/tmp/zt_mpv.sock"

// Re-exported socket path for callers that pre-remove stale sockets.
const MPVSocketPath = mpvSocketPath

var (
	ipcConn net.Conn
	ipcMu   sync.Mutex
)

func connectIPC() error {
	ipcMu.Lock()
	defer ipcMu.Unlock()

	if ipcConn != nil {
		return nil
	}

	var err error
	for i := 0; i < 10; i++ {
		ipcConn, err = net.Dial("unix", mpvSocketPath)
		if err == nil {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("failed to connect to mpv ipc: %w", err)
}

func sendIPCCommand(args []interface{}) error {
	if err := connectIPC(); err != nil {
		return err
	}

	ipcMu.Lock()
	defer ipcMu.Unlock()

	cmd := map[string]interface{}{
		"command": args,
	}
	b, _ := json.Marshal(cmd)
	b = append(b, '\n')

	_, err := ipcConn.Write(b)
	if err != nil {
		ipcConn.Close()
		ipcConn = nil
		return err
	}
	return nil
}

func MPVHotSwap(newURL string, pos float64) error {
	err := sendIPCCommand([]interface{}{"loadfile", newURL, "replace"})
	if err != nil {
		return err
	}
	if pos > 0 {
		return sendIPCCommand([]interface{}{"seek", pos, "absolute+keyframes"})
	}
	return nil
}

func ipcGetProperty(prop string) (float64, error) {
	if err := connectIPC(); err != nil {
		return 0, err
	}

	ipcMu.Lock()
	defer ipcMu.Unlock()

	reqID := time.Now().UnixNano()
	cmd := map[string]interface{}{
		"command":    []interface{}{"get_property", prop},
		"request_id": reqID,
	}
	b, _ := json.Marshal(cmd)
	b = append(b, '\n')

	_, err := ipcConn.Write(b)
	if err != nil {
		ipcConn.Close()
		ipcConn = nil
		return 0, err
	}

	ipcConn.SetReadDeadline(time.Now().Add(1 * time.Second))
	reader := bufio.NewReader(ipcConn)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return 0, err
		}
		var resp struct {
			Data      float64 `json:"data"`
			RequestID int64   `json:"request_id"`
			Error     string  `json:"error"`
		}
		if err := json.Unmarshal(line, &resp); err == nil {
			if resp.RequestID == reqID {
				if resp.Error != "success" {
					return 0, fmt.Errorf("mpv error: %s", resp.Error)
				}
				return resp.Data, nil
			}
		}
	}
}

func MPVGetTimePos() (float64, error) {
	return ipcGetProperty("time-pos")
}

func MPVGetDuration() (float64, error) {
	return ipcGetProperty("duration")
}
