package limiter

import (
	"crypto/tls"
	"log"
	"math/rand"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Filter defines the criteria for matching requests to an Application
type Filter struct {
	// HostHeader is the expected host header (can contain wildcards)
	HostHeader string
	// PathPrefix is the URI path prefix to match
	PathPrefix string
	// Methods are the HTTP methods this filter applies to (empty means all methods)
	Methods []string
}

// Matches checks if a request matches this filter
func (f *Filter) Matches(host, reqPath, method string) bool {
	// Check host header (if specified)
	if f.HostHeader != "" {
		if !matchPattern(f.HostHeader, host) {
			return false
		}
	}

	// Check path prefix (if specified)
	if f.PathPrefix != "" {
		if !strings.HasPrefix(path.Clean(reqPath), path.Clean(f.PathPrefix)) {
			return false
		}
	}

	// Check method (if methods are specified)
	if len(f.Methods) > 0 {
		for _, m := range f.Methods {
			if strings.EqualFold(m, method) {
				return true
			}
		}
		return false
	}

	return true
}

// matchPattern checks if a string matches a pattern (supports * wildcard)
func matchPattern(pattern, str string) bool {
	if pattern == "*" {
		return true
	}

	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == str
	}

	if !strings.HasPrefix(str, parts[0]) {
		return false
	}

	str = str[len(parts[0]):]
	for i := 1; i < len(parts)-1; i++ {
		idx := strings.Index(str, parts[i])
		if idx == -1 {
			return false
		}
		str = str[idx+len(parts[i]):]
	}

	return strings.HasSuffix(str, parts[len(parts)-1])
}

// Target represents a backend target with its own rate limiter
type Target struct {
	// Name is a unique identifier for this target
	Name string
	// URL is the full URL to the target (including scheme and host)
	URL *url.URL
	// Strategy is the rate limiting strategy for this target
	Strategy RateLimitStrategy
	// Proxy is the reverse proxy for this target
	Proxy *httputil.ReverseProxy
}

// RateLimitType represents the type of rate limiting strategy to use
type RateLimitType string

const (
	FixedWindow  RateLimitType = "fixed-window"
	SlidingWindow RateLimitType = "sliding-window"
	NoLimit      RateLimitType = "no-limit"
)

// Subpool represents a group of targets with shared configuration
type Subpool struct {
	// Name is a unique identifier for this subpool
	Name string
	// Weight determines the probability of this subpool being chosen
	Weight int
	// Targets holds the backend targets
	Targets []*Target
	// RequestLimit is the number of requests allowed per window
	RequestLimit int
	// RateLimitType determines which strategy to use
	RateLimitType RateLimitType
	// TimeWindow is the duration of the rate limiting window
	TimeWindow time.Duration
	// InsecureSkipVerify disables SSL certificate validation
	InsecureSkipVerify bool
	// CheckInterval determines how often to sync with Redis (in number of requests)
	CheckInterval int
	// SlowStartDuration is the duration over which to gradually increase the rate limit
	SlowStartDuration time.Duration
	// mu protects the targets list
	mu sync.RWMutex
	// totalWeight is the sum of target weights
	totalWeight int
}

// NewSubpool creates a new Subpool
func NewSubpool(name string, weight, limit int, window time.Duration, insecureSkipVerify bool, checkInterval int, slowStartDuration time.Duration) *Subpool {
	return &Subpool{
		Name:              name,
		Weight:            weight,
		Targets:           make([]*Target, 0),
		InsecureSkipVerify: insecureSkipVerify,
		RequestLimit:       limit,
		TimeWindow:         window,
		CheckInterval:      checkInterval,
		SlowStartDuration:  slowStartDuration,
	}
}

// AddTarget adds a new target to the subpool
func (s *Subpool) AddTarget(name, rawURL string, redisClient *redis.Client) *Target {
	s.mu.Lock()
	defer s.mu.Unlock()

	targetURL, err := url.Parse(rawURL)
	if err != nil {
		log.Printf("Error parsing target URL for %s: %v", name, err)
		return nil
	}

	// Default to HTTP if no scheme provided
	if targetURL.Scheme == "" {
		targetURL.Scheme = "http"
	}

	proxy := httputil.NewSingleHostReverseProxy(targetURL)

	// Configure TLS if using HTTPS
	if targetURL.Scheme == "https" {
		proxy.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: s.InsecureSkipVerify,
			},
		}
	}

	// Create rate limiting strategy based on configuration
	checkInterval := s.CheckInterval
	if checkInterval <= 0 {
		checkInterval = 10 // Default to sync with Redis every 10 requests
	}

	var strategy RateLimitStrategy
	switch s.RateLimitType {
	case FixedWindow:
		strategy = NewFixedWindowRedis(redisClient, s.RequestLimit, s.TimeWindow, "fixed", checkInterval, s.SlowStartDuration)
	case SlidingWindow:
		strategy = NewSlidingWindowRedis(redisClient, s.RequestLimit, s.TimeWindow, "sliding", checkInterval, s.SlowStartDuration)
	case NoLimit:
		strategy = NewRoundRobin(len(s.Targets) + 1) // +1 for the new target
	default:
		strategy = NewFixedWindowRedis(redisClient, s.RequestLimit, s.TimeWindow, "fixed", checkInterval, s.SlowStartDuration)
	}

	target := &Target{
		Name:     name,
		URL:      targetURL,
		Strategy: strategy,
		Proxy:    proxy,
	}
	s.Targets = append(s.Targets, target)
	return target
}

// GetAvailableTarget returns a target that hasn't hit its rate limit
func (s *Subpool) GetAvailableTarget() *Target {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.Targets) == 0 {
		return nil
	}

	// Try targets in random order
	indices := rand.Perm(len(s.Targets))
	for _, i := range indices {
		target := s.Targets[i]
		if target.Strategy.IsAllowed(target.Name) {
			return target
		}
	}

	return nil
}

// Pool represents a group of subpools
type Pool struct {
	// Name is a unique identifier for this pool
	Name string
	// Weight determines the probability of this pool being chosen
	Weight int
	// Subpools contains different groups of targets
	Subpools []*Subpool
	// mu protects the subpools list
	mu sync.RWMutex
	// totalWeight is the sum of all subpool weights
	totalWeight int
}

// NewPool creates a new Pool
func NewPool(name string, weight int) *Pool {
	return &Pool{
		Name:     name,
		Weight:   weight,
		Subpools: make([]*Subpool, 0),
	}
}

// AddSubpool adds a new subpool to the pool
func (p *Pool) AddSubpool(subpool *Subpool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.Subpools = append(p.Subpools, subpool)
	p.updateTotalWeight()
}

// updateTotalWeight recalculates the total weight of all subpools
func (p *Pool) updateTotalWeight() {
	total := 0
	for _, subpool := range p.Subpools {
		total += subpool.Weight
	}
	p.totalWeight = total
}

// GetRandomSubpool returns a randomly selected subpool based on weights
func (p *Pool) GetRandomSubpool() *Subpool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if len(p.Subpools) == 0 {
		return nil
	}

	// If there's only one subpool, return it
	if len(p.Subpools) == 1 {
		return p.Subpools[0]
	}

	// Pick a random number between 0 and total weight
	target := rand.Intn(p.totalWeight)
	current := 0

	// Find the subpool that contains the target weight
	for _, subpool := range p.Subpools {
		current += subpool.Weight
		if target < current {
			return subpool
		}
	}

	// Fallback to first subpool (shouldn't happen)
	return p.Subpools[0]
}

// GetLimiter returns a rate limiter and proxy from an available target in any subpool
func (p *Pool) GetLimiter(key string) (RateLimitStrategy, *httputil.ReverseProxy) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	// Try subpools in random order
	indices := rand.Perm(len(p.Subpools))
	for _, i := range indices {
		subpool := p.Subpools[i]
		target := subpool.GetAvailableTarget()
		if target != nil {
			return target.Strategy, target.Proxy
		}
	}

	return nil, nil
}

// Instance represents a backend instance with its own rate limiting configuration
type Instance struct {
	// Name is a unique identifier for this instance
	Name string
	// Filter defines which requests this instance handles
	Filter Filter
	// Pools contains different rate limiting pools
	Pools []*Pool
	// totalWeight is the sum of all pool weights
	totalWeight int
	// mu protects the pools list and totalWeight
	mu sync.RWMutex
}

// NewInstance creates a new Instance
func NewInstance(name string, filter Filter) *Instance {
	return &Instance{
		Name:    name,
		Filter:  filter,
		Pools:   make([]*Pool, 0),
	}
}

// AddPool adds a new pool to the instance
func (i *Instance) AddPool(pool *Pool) {
	i.mu.Lock()
	defer i.mu.Unlock()

	i.Pools = append(i.Pools, pool)
	i.updateTotalWeight()
}

// updateTotalWeight recalculates the total weight of all pools
func (i *Instance) updateTotalWeight() {
	total := 0
	for _, pool := range i.Pools {
		total += pool.Weight
	}
	i.totalWeight = total
}

// GetRandomPool returns a randomly selected pool based on weights
func (i *Instance) GetRandomPool() *Pool {
	i.mu.RLock()
	defer i.mu.RUnlock()

	if len(i.Pools) == 0 {
		return nil
	}

	// If there's only one pool, return it
	if len(i.Pools) == 1 {
		return i.Pools[0]
	}

	// Pick a random number between 0 and total weight
	target := rand.Intn(i.totalWeight)
	current := 0

	// Find the pool that contains the target weight
	for _, pool := range i.Pools {
		current += pool.Weight
		if target < current {
			return pool
		}
	}

	// Fallback to first pool (shouldn't happen)
	return i.Pools[0]
}

// GetLimiter returns a rate limiter and proxy from an available target in any pool
func (i *Instance) GetLimiter(key string) (RateLimitStrategy, *httputil.ReverseProxy) {
	i.mu.RLock()
	defer i.mu.RUnlock()

	// Try pools in random order
	indices := rand.Perm(len(i.Pools))
	for _, idx := range indices {
		pool := i.Pools[idx]
		rl, proxy := pool.GetLimiter(key)
		if rl != nil && proxy != nil {
			return rl, proxy
		}
	}

	return nil, nil
}

// Application represents the rate limiting application with its configuration and state
type Application struct {
	// Name is a unique identifier for this application
	Name string
	// Instances contains the backend instances for this application
	Instances []*Instance
	// Filter defines which requests this application handles
	Filter Filter
	// mu protects the instances list
	mu sync.RWMutex
}

// ApplicationManager manages multiple rate limiting applications
type ApplicationManager struct {
	// Applications is a list of applications in priority order
	Applications []*Application
	// mu protects the applications list
	mu sync.RWMutex
}

// NewApplication creates a new Application with the given filter
func NewApplication(name string, filter Filter) *Application {
	return &Application{
		Name:      name,
		Instances: make([]*Instance, 0),
		Filter:    filter,
	}
}

// NewApplicationManager creates a new ApplicationManager
func NewApplicationManager() *ApplicationManager {
	return &ApplicationManager{
		Applications: make([]*Application, 0),
	}
}

// AddApplication adds a new application to the manager
func (am *ApplicationManager) AddApplication(app *Application) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.Applications = append(am.Applications, app)
}

// GetApplication returns the first matching application for the given request
func (am *ApplicationManager) GetApplication(host, path, method string) *Application {
	am.mu.RLock()
	defer am.mu.RUnlock()

	for _, app := range am.Applications {
		if app.Filter.Matches(host, path, method) {
			return app
		}
	}

	return nil
}

// AddInstance adds a new instance to the application
func (app *Application) AddInstance(instance *Instance) {
	app.mu.Lock()
	defer app.mu.Unlock()

	app.Instances = append(app.Instances, instance)
}

// RemoveInstance removes an instance by name
func (app *Application) RemoveInstance(name string) {
	app.mu.Lock()
	defer app.mu.Unlock()

	for i, inst := range app.Instances {
		if inst.Name == name {
			app.Instances = append(app.Instances[:i], app.Instances[i+1:]...)
			break
		}
	}
}



// GetMatchingInstance returns the first instance that matches the request
func (app *Application) GetMatchingInstance(host, reqPath, method string) *Instance {
	app.mu.RLock()
	defer app.mu.RUnlock()

	if len(app.Instances) == 0 {
		return nil
	}

	// Return first matching instance
	for _, inst := range app.Instances {
		if inst.Filter.Matches(host, reqPath, method) {
			return inst
		}
	}

	// If no instance matches, return the first instance with an empty filter
	for _, inst := range app.Instances {
		if inst.Filter.HostHeader == "" && inst.Filter.PathPrefix == "" && len(inst.Filter.Methods) == 0 {
			return inst
		}
	}

	// No matching instance found
	return nil
}

// GetLimiter returns a rate limiter and proxy from a matching instance
func (app *Application) GetLimiter(key, host, reqPath, method string) (RateLimitStrategy, *httputil.ReverseProxy) {
	inst := app.GetMatchingInstance(host, reqPath, method)
	if inst == nil {
		return nil, nil
	}
	return inst.GetLimiter(key)
}



// RateLimiter handles rate limiting logic per target
type RateLimiter struct {
	requests []time.Time
	mu       sync.Mutex
	limit    int
	window   time.Duration
}

// New creates a new rate limiter instance
func New(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		requests: make([]time.Time, 0),
		limit:    limit,
		window:   window,
	}
}

// IsAllowed checks if a request is allowed based on rate limits
func (rl *RateLimiter) IsAllowed() bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	windowStart := now.Add(-rl.window)

	// Remove old requests
	var validRequests []time.Time
	for _, t := range rl.requests {
		if t.After(windowStart) {
			validRequests = append(validRequests, t)
		}
	}

	rl.requests = validRequests

	// Check if limit is exceeded
	if len(validRequests) >= rl.limit {
		return false
	}

	// Add new request
	rl.requests = append(rl.requests, now)
	return true
}
