//go:build windows

package tools

import (
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

func resolveRealPath(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	buffer := make([]uint16, 512)
	for {
		length, err := windows.GetFinalPathNameByHandle(windows.Handle(file.Fd()), &buffer[0], uint32(len(buffer)), 0)
		if err != nil {
			return "", err
		}
		if length < uint32(len(buffer)) {
			return cleanFinalWindowsPath(windows.UTF16ToString(buffer[:length])), nil
		}
		buffer = make([]uint16, length+1)
	}
}

func cleanFinalWindowsPath(path string) string {
	const prefix = `\\?\`
	if !strings.HasPrefix(path, prefix) {
		return path
	}
	path = strings.TrimPrefix(path, prefix)
	if strings.HasPrefix(path, `UNC\`) {
		return `\\` + strings.TrimPrefix(path, `UNC\`)
	}
	if len(path) >= 2 && path[1] == ':' {
		return path
	}
	return prefix + path
}
