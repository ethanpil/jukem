package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestFadeReachesTheEndLevel(t *testing.T) {
	var levels []int
	set := func(v int) error { levels = append(levels, v); return nil }

	Fade(context.Background(), set, 60, 0, 1)
	if len(levels) != 20 {
		t.Fatalf("twenty steps, got %d", len(levels))
	}
	if levels[len(levels)-1] != 0 {
		t.Fatalf("the last step is the end level, got %d", levels[len(levels)-1])
	}
	for i := 1; i < len(levels); i++ {
		if levels[i] > levels[i-1] {
			t.Fatalf("a fade out only goes down: %v", levels)
		}
	}
}

func TestFadeWithoutTimeSetsTheLevelAtOnce(t *testing.T) {
	var levels []int
	set := func(v int) error { levels = append(levels, v); return nil }

	Fade(context.Background(), set, 0, 70, 0)
	if len(levels) != 1 || levels[0] != 70 {
		t.Fatalf("one step to the end level, got %v", levels)
	}
	// The same level needs no fade either.
	levels = nil
	Fade(context.Background(), set, 40, 40, 5)
	if len(levels) != 1 || levels[0] != 40 {
		t.Fatalf("no fade to the level it already has, got %v", levels)
	}
}

func TestFadeStopsOnCancelOrError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var levels []int
	Fade(ctx, func(v int) error { levels = append(levels, v); return nil }, 50, 0, 1)
	if len(levels) != 0 {
		t.Fatalf("a fade that is cancelled sets nothing, got %v", levels)
	}

	count := 0
	start := time.Now()
	Fade(context.Background(), func(int) error {
		count++
		return errors.New("no mixer")
	}, 50, 0, 2)
	if count != 1 {
		t.Fatalf("a fade stops at the first failure, got %d steps", count)
	}
	if time.Since(start) > time.Second {
		t.Fatal("a fade that fails does not wait out its time")
	}
}
