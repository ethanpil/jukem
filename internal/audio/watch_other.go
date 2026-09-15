//go:build !linux

package audio

import (
	"context"
	"errors"
)

func watchDevSnd(ctx context.Context, dir string, fn func()) error {
	return errors.New("device watching needs Linux")
}
