package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

func createForkFile(dir string, header Header, records []entryRecord) (string, error) {
	for attempt := 0; attempt < 10; attempt++ {
		name := fmt.Sprintf("%s-fork-%s.jsonl", time.Now().UTC().Format("20060102T150405.000000000Z"), newID())
		path := filepath.Join(dir, name)
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		if err := writeSession(file, header, records); err != nil {
			file.Close()
			_ = os.Remove(path)
			return "", err
		}
		if err := file.Close(); err != nil {
			_ = os.Remove(path)
			return "", err
		}
		return path, nil
	}
	return "", fmt.Errorf("could not allocate a fork session path")
}

func rewriteSession(path string, header Header, records []entryRecord) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".session-migrate-*.jsonl")
	if err != nil {
		return err
	}
	tempPath := file.Name()
	keep := false
	defer func() {
		file.Close()
		if !keep {
			_ = os.Remove(tempPath)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	if err := writeSession(file, header, records); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	keep = true
	return nil
}

func writeSession(file *os.File, header Header, records []entryRecord) error {
	writer := bufio.NewWriter(file)
	if err := writeJSON(writer, header); err != nil {
		return err
	}
	for _, record := range records {
		if err := writeJSON(writer, record.value()); err != nil {
			return err
		}
	}
	if err := writer.Flush(); err != nil {
		return err
	}
	return file.Sync()
}

func writeJSON(writer io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = writer.Write(append(data, '\n'))
	return err
}

func writeJSONLine(file *os.File, value any) error {
	if err := writeJSON(file, value); err != nil {
		return err
	}
	return file.Sync()
}
