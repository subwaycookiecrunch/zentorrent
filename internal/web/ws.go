package web

// ws.go — minimal dependency-free WebSocket server: RFC 6455 handshake,
// server-push text frames only, tolerant of clients that never speak.
// Enough for a live status strip; deliberately not a full WS stack.

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" || r.Method != http.MethodGet {
		http.Error(w, "websocket upgrade required", http.StatusBadRequest)
		return
	}

	h := sha1.New()
	h.Write([]byte(key + wsGUID))
	accept := base64.StdEncoding.EncodeToString(h.Sum(nil))

	hij, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack unsupported", http.StatusInternalServerError)
		return
	}
	conn, rw, err := hij.Hijack()
	if err != nil {
		return
	}
	defer conn.Close()

	rw.WriteString("HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + accept + "\r\n\r\n")
	rw.Flush()

	// Reader goroutine: consume (and discard) client frames so TCP buffers
	// never back up; exits on close/error.
	done := make(chan struct{})
	go func() {
		defer close(done)
		discardFrames(rw.Reader)
	}()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
		}

		payload := []byte("null") // replaced below with real snapshot when set
		if s.Status != nil {
			if snap := s.Status(); snap != nil {
				if b, err := json.Marshal(snap); err == nil {
					payload = b
				}
			}
		}
		conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if err := writeFrame(conn, payload); err != nil {
			return
		}
	}
}

// writeFrame emits one unmasked server text frame.
func writeFrame(conn net.Conn, payload []byte) error {
	header := make([]byte, 0, 10)
	header = append(header, 0x81) // FIN + text opcode
	n := len(payload)
	switch {
	case n < 126:
		header = append(header, byte(n))
	case n < 65536:
		header = append(header, 126, byte(n>>8), byte(n))
	default:
		ext := make([]byte, 8)
		binary.BigEndian.PutUint64(ext, uint64(n))
		header = append(header, 127)
		header = append(header, ext...)
	}
	if _, err := conn.Write(header); err != nil {
		return err
	}
	_, err := conn.Write(payload)
	return err
}

// discardFrames reads and drops client frames until EOF or a close frame.
func discardFrames(r *bufio.Reader) {
	for {
		fin, err := readByte(r)
		if err != nil {
			return
		}
		_ = fin // text/continuation frames only; opcodes ignored
		mask, _ := readByte(r)
		length := int(mask & 0x7F)
		switch length {
		case 126:
			var ext [2]byte
			if _, err := io.ReadFull(r, ext[:]); err != nil {
				return
			}
			length = int(binary.BigEndian.Uint16(ext[:]))
		case 127:
			var ext [8]byte
			if _, err := io.ReadFull(r, ext[:]); err != nil {
				return
			}
		}
		var maskKey [4]byte
		if mask&0x80 != 0 {
			if _, err := io.ReadFull(r, maskKey[:]); err != nil {
				return
			}
		}
		if _, err := io.CopyN(io.Discard, r, int64(length)); err != nil {
			return
		}
		if mask&0x80 == 0 && length == 0 {
			return // close without mask (browsers always mask; be safe)
		}
	}
}

func readByte(r *bufio.Reader) (byte, error) {
	b, err := r.ReadByte()
	return b, err
}

// ---- test helpers (same package; exported nowhere else) ----

// dialWS performs a raw client handshake against srv over real TCP.
func dialWS(t testing.TB, rawURL string) (net.Conn, *bufio.ReadWriter, error) {
	t.Helper()
	addr := strings.TrimPrefix(rawURL, "http://")
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return nil, nil, err
	}
	req := "GET /ws HTTP/1.1\r\nHost: " + addr +
		"\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		return nil, nil, err
	}
	br := bufio.NewReader(conn)
	status, err := br.ReadString('\n')
	if err != nil {
		return nil, nil, err
	}
	if !strings.Contains(status, "101") {
		return nil, nil, errors.New("handshake rejected: " + strings.TrimSpace(status))
	}
	for {
		line, err := br.ReadString('\n')
		if err != nil || line == "\r\n" {
			break
		}
	}
	return conn, bufio.NewReadWriter(br, bufio.NewWriter(conn)), nil
}

// readServerFrame reads one text frame payload from the server.
func readServerFrame(t testing.TB, rw *bufio.ReadWriter) string {
	op, _ := rw.ReadByte()
	maskLen, _ := rw.ReadByte()
	_ = op
	length := int(maskLen & 0x7F)
	if length == 126 {
		var ext [2]byte
		rw.Read(ext[:])
		length = int(binary.BigEndian.Uint16(ext[:]))
	} else if length == 127 {
		var ext [8]byte
		rw.Read(ext[:])
	}
	buf := make([]byte, length)
	rw.Read(buf)
	return string(buf)
}
