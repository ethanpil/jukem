// Package library browses and searches the music through MPD, and manages
// files under the music root.
package library

import (
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// ErrBadPath reports a relative path that must not reach the file system.
var ErrBadPath = errors.New("path is not allowed")

// CleanRel validates a path relative to the music root. It returns the
// path with forward slashes and no trailing slash. It rejects absolute
// paths, ".." and "." segments, empty segments, control characters, and
// segments over 255 bytes. It also rejects names that begin with a dot.
// MPD skips hidden names, so a file there would never appear in the
// library.
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
	return path.Clean(p), nil
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
	if !inside(root, abs) {
		return "", ErrBadPath
	}
	return abs, nil
}

// Resolve is Abs followed by symlink resolution. The resolved path must
// still be inside the resolved root, so a link that points out of the
// music root is rejected. The target must exist.
func Resolve(root, rel string) (string, error) {
	abs, err := Abs(root, rel)
	if err != nil {
		return "", err
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", err
		}
		return "", ErrBadPath
	}
	if !inside(realRoot, real) {
		return "", ErrBadPath
	}
	return real, nil
}

// inside reports whether p is root or below it.
func inside(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
