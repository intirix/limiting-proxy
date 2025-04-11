package limiter

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RateLimitStrategy defines the interface for different rate limiting strategies
type RateLimitStrategy interface {
	IsAllowed(key string) bool
}

// FixedWindowRedis implements a fixed window rate limit using Redis
type FixedWindowRedis struct {
	client      *redis.Client
	limit       int
	window      time.Duration
	keyPrefix   string
}

// NewFixedWindowRedis creates a new fixed window rate limiter using Redis
func NewFixedWindowRedis(client *redis.Client, limit int, window time.Duration, keyPrefix string) *FixedWindowRedis {
	return &FixedWindowRedis{
		client:    client,
		limit:     limit,
		window:    window,
		keyPrefix: keyPrefix,
	}
}

// IsAllowed checks if the request is allowed using a fixed window counter
func (fw *FixedWindowRedis) IsAllowed(key string) bool {
	ctx := context.Background()
	redisKey := fmt.Sprintf("%s:%s:%d", fw.keyPrefix, key, time.Now().Unix()/int64(fw.window.Seconds()))

	// Use MULTI/EXEC for atomic operations
	pipe := fw.client.TxPipeline()
	incr := pipe.Incr(ctx, redisKey)
	pipe.Expire(ctx, redisKey, fw.window)
	_, err := pipe.Exec(ctx)
	if err != nil {
		return false
	}

	return incr.Val() <= int64(fw.limit)
}

// SlidingWindowRedis implements a sliding window rate limit using Redis
type SlidingWindowRedis struct {
	client      *redis.Client
	limit       int
	window      time.Duration
	keyPrefix   string
}

// NewSlidingWindowRedis creates a new sliding window rate limiter using Redis
func NewSlidingWindowRedis(client *redis.Client, limit int, window time.Duration, keyPrefix string) *SlidingWindowRedis {
	return &SlidingWindowRedis{
		client:    client,
		limit:     limit,
		window:    window,
		keyPrefix: keyPrefix,
	}
}

// IsAllowed checks if the request is allowed using a sliding window counter
func (sw *SlidingWindowRedis) IsAllowed(key string) bool {
	ctx := context.Background()
	now := time.Now().UnixNano() / int64(time.Millisecond)
	windowStart := now - int64(sw.window.Milliseconds())
	redisKey := fmt.Sprintf("%s:%s", sw.keyPrefix, key)

	// Use Redis MULTI/EXEC for atomic operations
	pipe := sw.client.TxPipeline()
	
	// Add current timestamp to sorted set
	pipe.ZAdd(ctx, redisKey, redis.Z{Score: float64(now), Member: now})
	
	// Remove old entries
	pipe.ZRemRangeByScore(ctx, redisKey, "0", fmt.Sprintf("%d", windowStart))
	
	// Count requests in current window
	count := pipe.ZCount(ctx, redisKey, fmt.Sprintf("%d", windowStart), fmt.Sprintf("%d", now))
	
	// Set expiration
	pipe.Expire(ctx, redisKey, sw.window*2)
	
	_, err := pipe.Exec(ctx)
	if err != nil {
		return false
	}

	return count.Val() <= int64(sw.limit)
}

// RoundRobin implements a simple round-robin strategy without rate limiting
type RoundRobin struct {
	current int
	total   int
}

// NewRoundRobin creates a new round-robin strategy
func NewRoundRobin(total int) *RoundRobin {
	return &RoundRobin{
		current: 0,
		total:   total,
	}
}

// IsAllowed always returns true for round-robin as it doesn't implement rate limiting
func (rr *RoundRobin) IsAllowed(key string) bool {
	return true
}

// GetNext returns the next index in the round-robin sequence
func (rr *RoundRobin) GetNext() int {
	next := rr.current
	rr.current = (rr.current + 1) % rr.total
	return next
}
