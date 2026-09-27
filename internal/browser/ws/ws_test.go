package ws

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// fakeServer is a minimal websocket server for tests: it completes the
// handshake and echoes text frames back unmasked.
type fakeServer struct {
	ln   net.Listener
	done chan struct{}
}

func startFakeServer(t *testing.T, onText func(string) string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &fakeServer{ln: ln, done: make(chan struct{})}
	go s.serve(onText)
	t.Cleanup(func() { ln.Close(); <-s.done })
	return "ws://" + ln.Addr().String() + "/ws"
}

func (s *fakeServer) serve(onText func(string) string) {
	defer close(s.done)
	conn, err := s.ln.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	br := bufio.NewReader(conn)
	// Read the HTTP upgrade request.
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
	resp := "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " + accept + "\r\n\r\n"
	if _, err := conn.Write([]byte(resp)); err != nil {
		return
	}
	for {
		opcode, payload, err := readServerFrame(br)
		if err != nil {
			return
		}
		switch opcode {
		case opText:
			reply := onText(string(payload))
			writeServerFrame(conn, opText, []byte(reply))
		case opClose:
			writeServerFrame(conn, opClose, nil)
			return
		case opPing:
			writeServerFrame(conn, opPong, payload)
		}
	}
}

func readServerFrame(br *bufio.Reader) (byte, []byte, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(br, hdr[:]); err != nil {
		return 0, nil, err
	}
	opcode := hdr[0] & 0x0F
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
	return opcode, payload, nil
}

func writeServerFrame(conn net.Conn, opcode byte, payload []byte) {
	n := len(payload)
	var hdr []byte
	switch {
	case n < 126:
		hdr = []byte{0x80 | opcode, byte(n)}
	case n < 65536:
		hdr = []byte{0x80 | opcode, 126, byte(n >> 8), byte(n)}
	default:
		hdr = []byte{0x80 | opcode, 127, 0, 0, 0, 0, byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}
	}
	conn.Write(hdr)
	conn.Write(payload)
}

func TestDialAndEcho(t *testing.T) {
	url := startFakeServer(t, func(s string) string { return "echo:" + s })
	c, err := Dial(url, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	// A message big enough to exercise the 16-bit extended length path.
	big := strings.Repeat("x", 70000)
	if err := c.WriteText([]byte(big)); err != nil {
		t.Fatal(err)
	}
	got, err := c.ReadText()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "echo:"+big {
		t.Fatalf("echo mismatch: len %d", len(got))
	}
}

func TestDialBadHandshake(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		br := bufio.NewReader(conn)
		for {
			line, _ := br.ReadString('\n')
			if line == "\r\n" {
				break
			}
		}
		conn.Write([]byte("HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\n\r\n"))
	}()
	_, err = Dial("ws://"+ln.Addr().String()+"/", 5*time.Second)
	if err == nil {
		t.Fatal("expected handshake error")
	}
}
