// Package ws is a minimal RFC 6455 WebSocket client: text messages only,
// client frames masked, server frames unmasked, with continuation-frame
// reassembly. It exists so the browser package can speak CDP without
// pulling in a third-party websocket dependency.
package ws

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	opContinuation = 0x0
	opText         = 0x1
	opClose        = 0x8
	opPing         = 0x9
	opPong         = 0xA
)

// Conn is a single WebSocket connection. Reads must happen from one
// goroutine at a time; writes are serialized internally.
type Conn struct {
	rw      *bufio.ReadWriter
	conn    net.Conn
	writeMu sync.Mutex
	closed  bool
}

// Dial opens a WebSocket connection to a ws:// or wss:// URL.
// Only ws:// is supported (CDP runs on localhost).
func Dial(wsURL string, timeout time.Duration) (*Conn, error) {
	u, err := url.Parse(wsURL)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "ws" {
		return nil, fmt.Errorf("ws: only ws:// URLs are supported, got %q", u.Scheme)
	}
	host := u.Host
	if !strings.Contains(host, ":") {
		host += ":80"
	}
	conn, err := net.DialTimeout("tcp", host, timeout)
	if err != nil {
		return nil, fmt.Errorf("ws: dial: %w", err)
	}
	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		conn.Close()
		return nil, err
	}
	b64key := base64.StdEncoding.EncodeToString(key)
	path := u.RequestURI()
	fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n",
		path, u.Host, b64key)
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: "GET"})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("ws: handshake: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		conn.Close()
		return nil, fmt.Errorf("ws: handshake status %d", resp.StatusCode)
	}
	accept := resp.Header.Get("Sec-WebSocket-Accept")
	want := wsAcceptKey(b64key)
	if accept != want {
		conn.Close()
		return nil, fmt.Errorf("ws: bad accept key")
	}
	return &Conn{rw: bufio.NewReadWriter(br, bufio.NewWriter(conn)), conn: conn}, nil
}

func wsAcceptKey(key string) string {
	h := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	return base64.StdEncoding.EncodeToString(h[:])
}

// WriteText sends one masked text message. Concurrent writes are
// serialized; a frame is never interleaved with another.
func (c *Conn) WriteText(msg []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.closed {
		return fmt.Errorf("ws: connection closed")
	}
	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		return err
	}
	hdr := []byte{0x80 | opText}
	n := len(msg)
	switch {
	case n < 126:
		hdr = append(hdr, byte(0x80|n))
	case n < 65536:
		hdr = append(hdr, 0x80|126, byte(n>>8), byte(n))
	default:
		hdr = append(hdr, 0x80|127)
		var lb [8]byte
		binary.BigEndian.PutUint64(lb[:], uint64(n))
		hdr = append(hdr, lb[:]...)
	}
	hdr = append(hdr, mask[:]...)
	masked := make([]byte, n)
	for i := range msg {
		masked[i] = msg[i] ^ mask[i%4]
	}
	if _, err := c.rw.Write(hdr); err != nil {
		return err
	}
	if _, err := c.rw.Write(masked); err != nil {
		return err
	}
	return c.rw.Flush()
}

// ReadText reads one complete text message, reassembling continuation
// frames. Control frames are answered (pong) and skipped.
func (c *Conn) ReadText() ([]byte, error) {
	var msg []byte
	for {
		fin, opcode, payload, err := c.readFrame()
		if err != nil {
			return nil, err
		}
		switch opcode {
		case opText, opContinuation:
			msg = append(msg, payload...)
			if fin {
				return msg, nil
			}
		case opPing:
			_ = c.writeControl(opPong, payload)
		case opClose:
			_ = c.writeControl(opClose, nil)
			c.closed = true
			return nil, fmt.Errorf("ws: peer closed")
		default:
			// ignore other opcodes
		}
	}
}

func (c *Conn) readFrame() (fin bool, opcode byte, payload []byte, err error) {
	var hdr [2]byte
	if _, err := io.ReadFull(c.rw, hdr[:]); err != nil {
		return false, 0, nil, err
	}
	fin = hdr[0]&0x80 != 0
	opcode = hdr[0] & 0x0F
	masked := hdr[1]&0x80 != 0
	n := int64(hdr[1] & 0x7F)
	switch n {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(c.rw, ext[:]); err != nil {
			return false, 0, nil, err
		}
		n = int64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(c.rw, ext[:]); err != nil {
			return false, 0, nil, err
		}
		n = int64(binary.BigEndian.Uint64(ext[:]))
	}
	if n > 128<<20 {
		return false, 0, nil, fmt.Errorf("ws: frame too large (%d bytes)", n)
	}
	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(c.rw, mask[:]); err != nil {
			return false, 0, nil, err
		}
	}
	payload = make([]byte, n)
	if _, err := io.ReadFull(c.rw, payload); err != nil {
		return false, 0, nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return fin, opcode, payload, nil
}

func (c *Conn) writeControl(opcode byte, payload []byte) error {
	// Clients must mask all frames, including control frames (RFC 6455 §5.3).
	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		return err
	}
	hdr := []byte{0x80 | opcode, 0x80 | byte(len(payload))}
	hdr = append(hdr, mask[:]...)
	masked := make([]byte, len(payload))
	for i := range payload {
		masked[i] = payload[i] ^ mask[i%4]
	}
	if _, err := c.rw.Write(hdr); err != nil {
		return err
	}
	if _, err := c.rw.Write(masked); err != nil {
		return err
	}
	return c.rw.Flush()
}

// Close sends a close frame and closes the TCP connection.
func (c *Conn) Close() error {
	if !c.closed {
		_ = c.writeControl(opClose, nil)
		c.closed = true
	}
	return c.conn.Close()
}

// SetReadDeadline sets the deadline for subsequent reads.
func (c *Conn) SetReadDeadline(t time.Time) error {
	return c.conn.SetReadDeadline(t)
}
