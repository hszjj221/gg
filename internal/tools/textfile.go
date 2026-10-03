package tools

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

func readTextRange(ctx context.Context, path string, offset, limit int) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	var out strings.Builder
	line, count := 1, 0
	for count < limit {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		fragment, err := reader.ReadSlice('\n')
		if isBinaryContent(fragment) {
			return "", fmt.Errorf("binary file not readable as text")
		}
		if line >= offset {
			remaining := defaultMaxReadBytes - out.Len()
			if len(fragment) > remaining {
				out.Write(fragment[:remaining])
				return normalizeNewlines(strings.ToValidUTF8(out.String(), "")) + truncationMarker(defaultMaxReadBytes) + fmt.Sprintf(" (line %d; use offset %d to continue, or bash for an oversized line)", line, line), nil
			}
			out.Write(fragment)
		}
		if err == nil || err == io.EOF {
			if line >= offset {
				count++
			}
			line++
		}
		if err == io.EOF {
			break
		}
		if err != nil && err != bufio.ErrBufferFull {
			return "", err
		}
	}
	text := strings.TrimSuffix(normalizeNewlines(out.String()), "\n")
	if count == defaultMaxReadLines {
		if _, err := reader.Peek(1); err == nil {
			text += fmt.Sprintf("\n... more lines available; continue with offset %d ...", line)
		}
	}
	return text, nil
}

// Write in the same directory so rename is atomic and existing permissions survive.
func writeFileAtomic(ctx context.Context, path string, data []byte) error {
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".gg-write-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Chmod(mode); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func normalizeNewlines(text string) string {
	return strings.ReplaceAll(text, "\r\n", "\n")
}

func splitTextLines(text string) []string {
	lines := strings.Split(normalizeNewlines(text), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func isBinaryContent(data []byte) bool {
	for _, b := range data {
		if b == 0 {
			return true
		}
	}
	return false
}

func truncationMarker(maxBytes int) string {
	return fmt.Sprintf("\n... truncated after %d bytes ...", maxBytes)
}

// Path locks are shared by all tool instances. Reference counting prevents
// an unbounded map when many different files are edited over the daemon life.
var fileLocks = struct {
	sync.Mutex
	entries map[string]*fileLock
}{entries: make(map[string]*fileLock)}

type fileLock struct {
	gate chan struct{}
	refs int
}

func lockFile(ctx context.Context, path string) (func(), error) {
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	path = filepath.Join(parent, filepath.Base(path))
	fileLocks.Lock()
	lock := fileLocks.entries[path]
	if lock == nil {
		lock = &fileLock{gate: make(chan struct{}, 1)}
		fileLocks.entries[path] = lock
	}
	lock.refs++
	fileLocks.Unlock()
	releaseRef := func() {
		fileLocks.Lock()
		lock.refs--
		if lock.refs == 0 {
			delete(fileLocks.entries, path)
		}
		fileLocks.Unlock()
	}
	select {
	case lock.gate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-lock.gate
			releaseRef()
			return nil, err
		}
		return func() { <-lock.gate; releaseRef() }, nil
	case <-ctx.Done():
		releaseRef()
		return nil, ctx.Err()
	}
}
