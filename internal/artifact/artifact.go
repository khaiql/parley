package artifact

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	MaxFileBytes            int64 = 100 * 1024 * 1024
	MaxFilesPerMessage            = 10
	MaxTotalBytesPerMessage int64 = 250 * 1024 * 1024
)

type Metadata struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type Limits struct {
	MaxFileBytes            int64 `json:"max_file_bytes"`
	MaxFilesPerMessage      int   `json:"max_files_per_message"`
	MaxTotalBytesPerMessage int64 `json:"max_total_bytes_per_message"`
}

type LocalFileInfo struct {
	Path string
	Name string
	Size int64
}

type Error struct {
	Code    string
	Message string
}

func (e Error) Error() string {
	return e.Message
}

func DefaultLimits() Limits {
	return Limits{
		MaxFileBytes:            MaxFileBytes,
		MaxFilesPerMessage:      MaxFilesPerMessage,
		MaxTotalBytesPerMessage: MaxTotalBytesPerMessage,
	}
}

func IsCode(err error, code string) bool {
	var artifactErr Error
	return errors.As(err, &artifactErr) && artifactErr.Code == code
}

func SanitizeName(path string) string {
	name := filepath.Base(path)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == string(filepath.Separator) {
		return "artifact"
	}
	return name
}

func ValidateLocalFile(path string) (LocalFileInfo, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return LocalFileInfo{}, Error{Code: "file_not_found", Message: fmt.Sprintf("file does not exist: %s", path)}
	}
	if err != nil {
		return LocalFileInfo{}, err
	}
	if info.IsDir() {
		return LocalFileInfo{}, Error{Code: "artifact_must_be_file", Message: fmt.Sprintf("artifact path must be a file: %s", path)}
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return LocalFileInfo{}, Error{Code: "artifact_must_be_regular_file", Message: fmt.Sprintf("artifact path must be a regular file: %s", path)}
	}
	if info.Size() > MaxFileBytes {
		return LocalFileInfo{}, Error{Code: "artifact_too_large", Message: fmt.Sprintf("artifact exceeds %d bytes: %s", MaxFileBytes, path)}
	}
	return LocalFileInfo{Path: path, Name: SanitizeName(path), Size: info.Size()}, nil
}
