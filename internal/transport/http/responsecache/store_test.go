package responsecache

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func TestRedisStoreGetSetAndTTL(t *testing.T) {
	server := miniredis.RunT(t)
	store := NewRedisStore(RedisOptions{Addr: server.Addr(), Timeout: time.Second})
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()

	if _, found, err := store.Get(ctx, "missing"); err != nil || found {
		t.Fatalf("missing key: found=%t err=%v", found, err)
	}
	if err := store.Set(ctx, "key", []byte("value"), time.Minute); err != nil {
		t.Fatal(err)
	}
	if value, found, err := store.Get(ctx, "key"); err != nil || !found || string(value) != "value" {
		t.Fatalf("stored key: %q found=%t err=%v", value, found, err)
	}
	if ttl := server.TTL("key"); ttl != time.Minute {
		t.Fatalf("ttl = %s, want 1m", ttl)
	}
	server.FastForward(time.Minute + time.Second)
	if _, found, _ := store.Get(ctx, "key"); found {
		t.Fatal("expected the key to expire")
	}
}

func TestRedisStoreFailsFastWhenUnreachable(t *testing.T) {
	server := miniredis.RunT(t)
	addr := server.Addr()
	server.Close()
	store := NewRedisStore(RedisOptions{Addr: addr, Timeout: 50 * time.Millisecond})
	t.Cleanup(func() { _ = store.Close() })

	start := time.Now()
	if _, _, err := store.Get(context.Background(), "key"); err == nil {
		t.Fatal("expected an error from an unreachable Redis")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("unreachable Redis took %s", elapsed)
	}
}
