// Package responsecache caches finished responses of allowlisted public read
// routes in Redis (docs/postgres-master-data-store.md, "Caching"). It is a
// read-through cache: entries are keyed by the build and the region's latest
// successful sync, so a sync or a deploy retires them, and any Redis error
// only bypasses the cache.
package responsecache

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// Store keeps encoded entries. Get reports a miss with found false and a nil
// error.
type Store interface {
	Get(ctx context.Context, key string) ([]byte, bool, error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
}

// RedisStore is a Store in Redis. Every call is bounded by timeout, so a slow
// Redis delays a request by at most that much. Connections are dialed with a
// longer timeout and kept warm in the background, so requests do not pay
// for dialing: a dial within the request budget would fail every time on a
// link whose handshake alone takes longer.
type RedisStore struct {
	client  *redis.Client
	timeout time.Duration
}

// RedisOptions configures NewRedisStore.
type RedisOptions struct {
	Addr     string
	Password string
	DB       int
	Timeout  time.Duration
}

const (
	// dialTimeout bounds connection setup, which happens off the request
	// path once the pool is warm.
	dialTimeout = time.Second
	// minIdleConns keeps connections ready for requests.
	minIdleConns = 2
)

// NewRedisStore does not ping; the pool dials in the background, and an
// unreachable Redis only turns requests into bypasses.
func NewRedisStore(options RedisOptions) *RedisStore {
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = 50 * time.Millisecond
	}
	client := redis.NewClient(&redis.Options{
		Addr:         options.Addr,
		Password:     options.Password,
		DB:           options.DB,
		DialTimeout:  dialTimeout,
		ReadTimeout:  timeout,
		WriteTimeout: timeout,
		PoolTimeout:  timeout,
		MinIdleConns: minIdleConns,
		MaxRetries:   -1,
	})
	return &RedisStore{client: client, timeout: timeout}
}

func (store *RedisStore) Get(ctx context.Context, key string) ([]byte, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, store.timeout)
	defer cancel()
	value, err := store.client.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return value, true, nil
}

func (store *RedisStore) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, store.timeout)
	defer cancel()
	return store.client.Set(ctx, key, value, ttl).Err()
}

// Close releases the connection pool.
func (store *RedisStore) Close() error {
	return store.client.Close()
}
