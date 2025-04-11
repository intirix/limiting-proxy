package limiter

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// RateLimitStrategy defines the interface for different rate limiting strategies
type RateLimitStrategy interface {
	IsAllowed(key string) bool
}

// FixedWindowRedis implements a fixed window rate limit using Redis
type FixedWindowRedis struct {
	client           *redis.Client
	limit            int
	window           time.Duration
	keyPrefix        string
	checkInterval    int
	slowStartDuration time.Duration
	startTime        time.Time
	localCounters    *sync.Map
	lastSync         *sync.Map
}

// NewFixedWindowRedis creates a new fixed window rate limiter using Redis
func NewFixedWindowRedis(client *redis.Client, limit int, window time.Duration, keyPrefix string, checkInterval int, slowStartDuration time.Duration) *FixedWindowRedis {
	return &FixedWindowRedis{
		client:           client,
		limit:            limit,
		window:           window,
		keyPrefix:        keyPrefix,
		checkInterval:    checkInterval,
		startTime:        time.Now(),
		localCounters:    &sync.Map{},
		lastSync:         &sync.Map{},
	}
}

// IsAllowed checks if the request is allowed using a fixed window counter
func (fw *FixedWindowRedis) IsAllowed(key string) bool {
	windowKey := fmt.Sprintf("%s:%d", key, time.Now().Unix()/int64(fw.window.Seconds()))

	currentLimit := int64(fw.limit)

	// Get or initialize local counter
	counterVal, _ := fw.localCounters.LoadOrStore(windowKey, int64(0))
	counter := counterVal.(int64)

	// Check if we need to sync with Redis
	lastSyncVal, _ := fw.lastSync.LoadOrStore(windowKey, time.Now().Add(-fw.window))
	lastSync := lastSyncVal.(time.Time)

	// Increment local counter
	counter++
	fw.localCounters.Store(windowKey, counter)

	// Check if we need to sync with Redis
	if counter%int64(fw.checkInterval) == 0 || time.Since(lastSync) >= fw.window {
		ctx := context.Background()
		redisKey := fmt.Sprintf("%s:%s", fw.keyPrefix, windowKey)

		// Use MULTI/EXEC for atomic operations
		pipe := fw.client.TxPipeline()
		incr := pipe.IncrBy(ctx, redisKey, counter)
		pipe.Expire(ctx, redisKey, fw.window)
		_, err := pipe.Exec(ctx)

		if err != nil {
			return false
		}

		// Reset local counter and update last sync time
		fw.localCounters.Store(windowKey, int64(0))
		fw.lastSync.Store(windowKey, time.Now())

		v := incr.Val()

		log.Printf("currentValue: %d, currentLimit: %d", v, currentLimit)

		return v <= currentLimit
	}

	// If we haven't synced, estimate based on local counter
	return counter <= currentLimit
}

// SlidingWindowRedis implements a sliding window rate limit using Redis
type SlidingWindowRedis struct {
	client           *redis.Client
	limit            int
	window           time.Duration
	keyPrefix        string
	checkInterval    int
	slowStartDuration time.Duration
	startTime        time.Time
	localCounters    *sync.Map
	lastSync         *sync.Map
	localEvents      *sync.Map
}

// NewSlidingWindowRedis creates a new sliding window rate limiter using Redis
func NewSlidingWindowRedis(client *redis.Client, limit int, window time.Duration, keyPrefix string, checkInterval int, slowStartDuration time.Duration) *SlidingWindowRedis {
	return &SlidingWindowRedis{
		client:           client,
		limit:            limit,
		window:           window,
		keyPrefix:        keyPrefix,
		checkInterval:    checkInterval,
		slowStartDuration: slowStartDuration,
		startTime:        time.Now(),
		localCounters:    &sync.Map{},
		lastSync:         &sync.Map{},
		localEvents:      &sync.Map{},
	}
}

// IsAllowed checks if the request is allowed using a sliding window counter
func (sw *SlidingWindowRedis) IsAllowed(key string) bool {
	now := time.Now()

	currentLimit := int64(sw.limit)

	// Get or initialize local events list
	eventsVal, _ := sw.localEvents.LoadOrStore(key, []time.Time{})
	events := eventsVal.([]time.Time)

	// Remove events outside the window
	windowStart := now.Add(-sw.window)
	validEvents := events[:0]
	for _, t := range events {
		if t.After(windowStart) {
			validEvents = append(validEvents, t)
		}
	}

	// Add current event
	validEvents = append(validEvents, now)
	sw.localEvents.Store(key, validEvents)

	// Get local counter
	counterVal, _ := sw.localCounters.LoadOrStore(key, int64(0))
	counter := counterVal.(int64)
	counter++
	sw.localCounters.Store(key, counter)

	// Check if we need to sync with Redis
	lastSyncVal, _ := sw.lastSync.LoadOrStore(key, now.Add(-sw.window))
	lastSync := lastSyncVal.(time.Time)

	if counter%int64(sw.checkInterval) == 0 || time.Since(lastSync) >= sw.window {
		ctx := context.Background()
		redisKey := fmt.Sprintf("%s:%s", sw.keyPrefix, key)

		// Use Redis MULTI/EXEC for atomic operations
		pipe := sw.client.TxPipeline()

		// Add all local events to Redis
		for _, t := range validEvents {
			timestamp := float64(t.UnixNano() / int64(time.Millisecond))
			pipe.ZAdd(ctx, redisKey, redis.Z{Score: timestamp, Member: timestamp})
		}

		// Remove old entries
		windowStartMs := float64(windowStart.UnixNano() / int64(time.Millisecond))
		pipe.ZRemRangeByScore(ctx, redisKey, "0", fmt.Sprintf("%f", windowStartMs))

		// Count requests in current window
		nowMs := float64(now.UnixNano() / int64(time.Millisecond))
		count := pipe.ZCount(ctx, redisKey, fmt.Sprintf("%f", windowStartMs), fmt.Sprintf("%f", nowMs))

		// Set expiration
		pipe.Expire(ctx, redisKey, sw.window*2)

		_, err := pipe.Exec(ctx)
		if err != nil {
			return false
		}

		// Reset local counter and update last sync time
		sw.localCounters.Store(key, int64(0))
		sw.lastSync.Store(key, now)

		v := count.Val()
		//log.Printf("currentValue: %d, currentLimit: %d", v, currentLimit)

		return v <= currentLimit
	}

	// If we haven't synced, use local count
	return int64(len(validEvents)) <= currentLimit
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

// SlowStart implements a slow rampup period by randomly rejecting requests
type SlowStart struct {
	slowStartDuration time.Duration
	startTime        time.Time
}

// NewSlowStart creates a new slow start strategy
func NewSlowStart(slowStartDuration time.Duration, startTime time.Time) *SlowStart {
	return &SlowStart{
		slowStartDuration: slowStartDuration,
		startTime:         startTime,
	}
}

// IsAllowed checks if the request is allowed based on the slow start duration
func (ss *SlowStart) IsAllowed(key string) bool {
	if ss.slowStartDuration == 0 {
		return true
	}

	elapsed := time.Since(ss.startTime)
	if elapsed >= ss.slowStartDuration {
		return true
	}

	// Calculate percentage of slow start duration completed
	percentage := float64(elapsed) / float64(ss.slowStartDuration)

	// return true randomly based on percentage
	v := rand.Float64()
	//log.Printf("v: %f, percentage: %f", v, percentage)
	return v < percentage
}

type RateLimitList struct {
	list []RateLimitStrategy
}

// NewRateLimitList creates a new rate limit list
func NewRateLimitList() *RateLimitList {
	return &RateLimitList{
		list: make([]RateLimitStrategy, 0),
	}
}

// AddStrategy adds a new rate limit strategy to the list
func (rl *RateLimitList) AddStrategy(strategy RateLimitStrategy) {
	rl.list = append(rl.list, strategy)
}

// IsAllowed checks if the request is allowed based on the rate limit list
func (rl *RateLimitList) IsAllowed(key string) bool {
	for _, strategy := range rl.list {
		if !strategy.IsAllowed(key) {
			return false
		}
	}
	return true
}

