//go:build !linux

package library

func diskSpace(dir string) (total, free int64) { return 0, 0 }
