package shared

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func TestRevisionCacheReusesValueUntilRevisionChanges(t *testing.T) {
	var cache RevisionCache[int]
	builds := 0
	build := func(context.Context) (int, error) {
		builds++
		return builds, nil
	}
	ctx := context.Background()

	for _, step := range []struct {
		revision string
		want     int
	}{
		{revision: "r1", want: 1},
		{revision: "r1", want: 1},
		{revision: "r2", want: 2},
		{revision: "", want: 3},
		{revision: "", want: 4},
		{revision: "r2", want: 2},
	} {
		got, err := cache.Load(ctx, "jp", step.revision, build)
		if err != nil {
			t.Fatalf("load revision %q: %v", step.revision, err)
		}
		if got != step.want {
			t.Fatalf("load revision %q: expected %d, got %d", step.revision, step.want, got)
		}
	}

	other, err := cache.Load(ctx, "en", "r2", build)
	if err != nil || other != 5 {
		t.Fatalf("expected regions to be cached independently, got %d, %v", other, err)
	}
}

func TestRevisionCacheBuildsOnceForConcurrentColdLoads(t *testing.T) {
	var cache RevisionCache[int]
	var builds atomic.Int32
	var wait sync.WaitGroup
	for range 8 {
		wait.Go(func() {
			got, err := cache.Load(context.Background(), "jp", "r1", func(context.Context) (int, error) {
				return int(builds.Add(1)), nil
			})
			if err != nil || got != 1 {
				t.Errorf("expected shared value 1, got %d, %v", got, err)
			}
		})
	}
	wait.Wait()

	if builds.Load() != 1 {
		t.Fatalf("expected one build for concurrent cold loads, got %d", builds.Load())
	}
}

func TestRevisionCacheDoesNotStoreFailedBuilds(t *testing.T) {
	var cache RevisionCache[int]
	ctx := context.Background()
	buildErr := errors.New("list failed")

	if _, err := cache.Load(ctx, "jp", "r1", func(context.Context) (int, error) { return 0, buildErr }); !errors.Is(err, buildErr) {
		t.Fatalf("expected build error, got %v", err)
	}
	got, err := cache.Load(ctx, "jp", "r1", func(context.Context) (int, error) { return 7, nil })
	if err != nil || got != 7 {
		t.Fatalf("expected failed build to be retried, got %d, %v", got, err)
	}
}
