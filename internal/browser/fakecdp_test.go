package browser

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"strings"
	"testing"
)

func listenTCP(t *testing.T) (net.Listener, string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return ln, ln.Addr().String()
}

// serveFakeCDP accepts one connection, completes the WS handshake, then
// answers CDP requests with canned results.
func serveFakeCDP(ln net.Listener) {
	conn, err := ln.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	br := bufio.NewReader(conn)
	var key string
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		if strings.HasPrefix(strings.ToLower(line), "sec-websocket-key:") {
			key = strings.TrimSpace(line[len("sec-websocket-key:"):])
		}
		if line == "\r\n" {
			break
		}
	}
	h := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	accept := base64.StdEncoding.EncodeToString(h[:])
	conn.Write([]byte("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " + accept + "\r\n\r\n"))

	for {
		payload, err := readMaskedFrame(br)
		if err != nil {
			return
		}
		var req cdpMessage
		if err := json.Unmarshal(payload, &req); err != nil {
			continue
		}
		resp := cdpMessage{ID: req.ID}
		switch req.Method {
		case "Page.enable", "Runtime.enable", "Page.navigate":
			resp.Result = json.RawMessage(`{}`)
		case "Runtime.evaluate":
			var p struct {
				Expression string `json:"expression"`
			}
			json.Unmarshal(req.Params, &p)
			var value string
			switch p.Expression {
			case "document.readyState":
				value = `"complete"`
			case "document.body ? document.body.innerText : ''":
				value = `"Example Domain"`
			case "document.title":
				value = `"Example"`
			default:
				value = `""`
			}
			resp.Result = json.RawMessage(`{"result":{"value":` + value + `}}`)
		case "Page.captureScreenshot":
			resp.Result = json.RawMessage(`{"data":"` +
				base64.StdEncoding.EncodeToString([]byte("fake-png-bytes")) + `"}`)
		default:
			resp.Error = &cdpError{Code: -32601, Message: "method not found"}
		}
		out, _ := json.Marshal(resp)
		if err := writeUnmaskedFrame(conn, out); err != nil {
			return
		}
	}
}

func readMaskedFrame(br *bufio.Reader) ([]byte, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(br, hdr[:]); err != nil {
		return nil, err
	}
	n := int64(hdr[1] & 0x7F)
	switch n {
	case 126:
		var ext [2]byte
		io.ReadFull(br, ext[:])
		n = int64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		io.ReadFull(br, ext[:])
		n = int64(binary.BigEndian.Uint64(ext[:]))
	}
	var mask [4]byte
	io.ReadFull(br, mask[:])
	payload := make([]byte, n)
	io.ReadFull(br, payload)
	for i := range payload {
		payload[i] ^= mask[i%4]
	}
	return payload, nil
}

func writeUnmaskedFrame(conn net.Conn, payload []byte) error {
	n := len(payload)
	var hdr []byte
	switch {
	case n < 126:
		hdr = []byte{0x81, byte(n)}
	case n < 65536:
		hdr = []byte{0x81, 126, byte(n >> 8), byte(n)}
	default:
		hdr = []byte{0x81, 127, 0, 0, 0, 0, byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}
	}
	if _, err := conn.Write(hdr); err != nil {
		return err
	}
	_, err := conn.Write(payload)
	return err
}
