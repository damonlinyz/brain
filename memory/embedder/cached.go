package embedder

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// CacheClient is the minimal Redis surface we need.
type CacheClient interface {
	Get(ctx context.Context, key string, val any) error
	Set(ctx context.Context, key string, val any, ttl time.Duration) error
}

// redisAdapter wraps *redis.Client to satisfy CacheClient.
type redisAdapter struct{ c *redis.Client }

func (r redisAdapter) Get(ctx context.Context, key string, val any) error {
	b, err := r.c.Get(ctx, key).Bytes()
	if err != nil {
		return err
	}
	return json.Unmarshal(b, val)
}

func (r redisAdapter) Set(ctx context.Context, key string, val any, ttl time.Duration) error {
	b, err := json.Marshal(val)
	if err != nil {
		return err
	}
	return r.c.Set(ctx, key, b, ttl).Err()
}

// NewRedisCache wraps a *redis.Client behind CacheClient.
func NewRedisCache(c *redis.Client) CacheClient {
	if c == nil {
		return nil
	}
	return redisAdapter{c: c}
}

// Cached decorates an Embedder with a content-hash Redis cache.
// Cache misses fall through to Inner; cache hits skip the backend call.
type Cached struct {
	Inner Embedder
	Cache CacheClient
	TTL   time.Duration
}

// NewCached constructs a Cached with sensible defaults. cache may be nil
// (disables caching, falls through to Inner).
func NewCached(inner Embedder, cache CacheClient) *Cached {
	return &Cached{
		Inner: inner,
		Cache: cache,
		TTL:   30 * 24 * time.Hour,
	}
}

func (c *Cached) Embed(ctx context.Context, content string) ([]float32, error) {
	if content == "" {
		return nil, nil
	}
	key := cacheKey(content)
	if c.Cache != nil {
		var cached []float32
		if err := c.Cache.Get(ctx, key, &cached); err == nil && len(cached) > 0 {
			return cached, nil
		}
	}
	vec, err := c.Inner.Embed(ctx, content)
	if err != nil {
		return nil, err
	}
	if c.Cache != nil && len(vec) > 0 {
		_ = c.Cache.Set(ctx, key, vec, c.TTL)
	}
	return vec, nil
}

func (c *Cached) EmbedBatch(ctx context.Context, contents []string) ([][]float32, error) {
	if len(contents) == 0 {
		return nil, nil
	}
	out := make([][]float32, len(contents))
	missIdx := []int{}
	missTexts := []string{}

	if c.Cache != nil {
		for i, text := range contents {
			if text == "" {
				continue
			}
			var cached []float32
			if err := c.Cache.Get(ctx, cacheKey(text), &cached); err == nil && len(cached) > 0 {
				out[i] = cached
			} else {
				missIdx = append(missIdx, i)
				missTexts = append(missTexts, text)
			}
		}
	} else {
		for i, text := range contents {
			if text == "" {
				continue
			}
			missIdx = append(missIdx, i)
			missTexts = append(missTexts, text)
		}
	}

	if len(missTexts) == 0 {
		return out, nil
	}
	vecs, err := c.Inner.EmbedBatch(ctx, missTexts)
	if err != nil {
		return nil, err
	}
	for j, vec := range vecs {
		out[missIdx[j]] = vec
		if c.Cache != nil && len(vec) > 0 {
			_ = c.Cache.Set(ctx, cacheKey(missTexts[j]), vec, c.TTL)
		}
	}
	return out, nil
}

func (c *Cached) Dim() int { return c.Inner.Dim() }

func cacheKey(content string) string {
	h := sha256.Sum256([]byte(content))
	return "memv2:emb:" + hex.EncodeToString(h[:16])
}

// errCacheSentinel kept for callers that want to distinguish cache miss from backend error.
var errCacheSentinel = errors.New("cache miss")
