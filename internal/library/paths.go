// Package library browses and searches the music through MPD, and manages
// files under the music root.
package library

import (
	"errors"
	"path"
	"path/filepath"
	"strings"
)

// ErrBadPath reports a relative path that must not reach the file system.
var ErrBadPath = errors.New("path is not allowed")

// CleanRel validates a path relative to the music root and returns it in
// canonical form with forward slashes. It rejects absolute paths, ".."
// segments, empty segments, control characters, segments over 255 bytes,
// and names that begin with a dot: MPD skips hidden names, so a file
// there would never appear in the library.
func CleanRel(p string) (string, error) {
	p = strings.ReplaceAll(p, "\\", "/")
	// A bare slash means the root; any other leading slash is absolute.
	if p == "/" {
		return "", nil
	}
	p = strings.TrimSuffix(p, "/")
	if p == "" {
		return "", nil
	}
	if strings.HasPrefix(p, "/") || filepath.IsAbs(p) || filepath.VolumeName(p) != "" {
		return "", ErrBadPath
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." || strings.HasPrefix(seg, ".") {
			return "", ErrBadPath
		}
		if len(seg) > 255 {
			return "", ErrBadPath
		}
		for _, r := range seg {
			if r < 0x20 || r == 0x7f {
				return "", ErrBadPath
			}
		}
	}
	cleaned := path.Clean(p)
	if cleaned != p {
		return "", ErrBadPath
	}
	return cleaned, nil
}

// Abs joins a validated relative path onto the root and confirms the
// result stays inside it.
func Abs(root, rel string) (string, error) {
	rel, err := CleanRel(rel)
	if err != nil {
		return "", err
	}
	root = filepath.Clean(root)
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if abs != root && !strings.HasPrefix(abs, root+string(filepath.Separator)) {
		return "", ErrBadPath
	}
	return abs, nil
}
