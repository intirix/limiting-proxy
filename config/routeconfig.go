package config

import (
	"fmt"
	"os"
	"time"

	"github.com/redis/go-redis/v9"
	"gopkg.in/yaml.v3"
	"limiting_proxy/limiter"
)

// RouteConfig represents the complete proxy route configuration
type RouteConfig struct {
	Applications []ApplicationConfig `yaml:"applications"`
}

// TargetConfig represents a backend target configuration
type TargetConfig struct {
	// Name is a unique identifier for this target
	Name string `yaml:"name"`
	// URL is the full URL to the target (including scheme and host)
	URL string `yaml:"url"`
	// StartTime is the time when the target was added
	StartTime time.Time `yaml:"startTime"`
}

// SubpoolConfig represents a group of targets with shared configuration
type SubpoolConfig struct {
	// Name is a unique identifier for this subpool
	Name string `yaml:"name"`
	// Weight determines the probability of this subpool being chosen
	Weight int `yaml:"weight"`
	// Targets contains the backend targets
	Targets []TargetConfig `yaml:"targets"`
	// Limit is the number of requests allowed per window
	Limit int `yaml:"limit"`
	// Window is the duration of the rate limiting window (in seconds)
	Window int `yaml:"window"`
	// InsecureSkipVerify disables SSL certificate validation
	InsecureSkipVerify bool `yaml:"insecureSkipVerify,omitempty"`
	// RateLimitType determines which strategy to use (fixed-window, sliding-window, no-limit)
	RateLimitType string `yaml:"rateLimitType,omitempty"`
	// CheckInterval determines how often to sync with Redis (in number of requests)
	CheckInterval int `yaml:"checkInterval,omitempty"`
	// SlowStartDuration is the duration over which to gradually increase the rate limit (in seconds)
	SlowStartDuration int `yaml:"slowStartDuration,omitempty"`
	// DeepHealthCheckPath is the path to use for deep health checks (e.g. /deep-health)
	DeepHealthCheckPath string `yaml:"deepHealthCheckPath,omitempty"`
	// ShallowHealthCheckPath is the path to use for shallow health checks (e.g. /shallow-health)
	ShallowHealthCheckPath string `yaml:"shallowHealthCheckPath,omitempty"`
	// HealthCheckPath is the legacy path for health checks (deprecated, use DeepHealthCheckPath and ShallowHealthCheckPath instead)
	HealthCheckPath string `yaml:"healthCheckPath,omitempty"`
	// HealthCheckInterval is how often to perform health checks (in seconds)
	HealthCheckInterval int `yaml:"healthCheckInterval,omitempty"`
	// HealthCheckTimeout is the timeout for health check requests (in seconds)
	HealthCheckTimeout int `yaml:"healthCheckTimeout,omitempty"`
	// RequiredSuccessfulChecks is the number of successful health checks required before adding a target
	RequiredSuccessfulChecks int `yaml:"requiredSuccessfulChecks,omitempty"`
	// AllowedFailedChecks is the number of consecutive failed health checks before removing a target
	AllowedFailedChecks int `yaml:"allowedFailedChecks,omitempty"`
}

// PoolConfig represents a group of subpools
type PoolConfig struct {
	// Name is a unique identifier for this pool
	Name string `yaml:"name"`
	// Weight determines the probability of this pool being chosen
	Weight int `yaml:"weight"`
	// Subpools contains different groups of targets
	Subpools []SubpoolConfig `yaml:"subpools"`
}

// InstanceConfig represents a single instance's configuration
type InstanceConfig struct {
	// Name is a unique identifier for this instance
	Name string `yaml:"name"`
	// Filter defines which requests this instance handles
	Filter FilterConfig `yaml:"filter"`
	// Pools contains the rate limiting pools for this instance
	Pools []PoolConfig `yaml:"pools"`
}

// ApplicationConfig represents the configuration for a single rate limiting application
type ApplicationConfig struct {
	// Name is a unique identifier for this application
	Name string `yaml:"name"`
	// Filter defines which requests this application handles
	Filter FilterConfig `yaml:"filter"`
	// Instances contains the backend instances for this application
	Instances []InstanceConfig `yaml:"instances"`
}

// FilterConfig represents the configuration for request filtering
type FilterConfig struct {
	// HostHeader is the expected host header (can contain wildcards)
	HostHeader string `yaml:"hostHeader,omitempty"`
	// PathPrefix is the URI path prefix to match
	PathPrefix string `yaml:"pathPrefix,omitempty"`
	// Methods are the HTTP methods this filter applies to
	Methods []string `yaml:"methods,omitempty"`
}

// Storage defines the interface for configuration storage backends
type Storage interface {
	// Load loads the route configuration from storage
	Load() (*RouteConfig, error)
	// Save saves the route configuration to storage
	Save(*RouteConfig) error
}

// YAMLStorage implements Storage interface using a YAML file
type YAMLStorage struct {
	FilePath string
}

// NewYAMLStorage creates a new YAML storage instance
func NewYAMLStorage(filePath string) *YAMLStorage {
	return &YAMLStorage{FilePath: filePath}
}

// Load loads route configuration from YAML file
func (s *YAMLStorage) Load() (*RouteConfig, error) {
	data, err := os.ReadFile(s.FilePath)
	if err != nil {
		return nil, fmt.Errorf("reading route config file: %w", err)
	}

	var config RouteConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("parsing route config file: %w", err)
	}

	return &config, nil
}

// Save saves route configuration to YAML file
func (s *YAMLStorage) Save(config *RouteConfig) error {
	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("marshaling route config: %w", err)
	}

	if err := os.WriteFile(s.FilePath, data, 0644); err != nil {
		return fmt.Errorf("writing route config file: %w", err)
	}

	return nil
}

// ToApplications converts route configuration to runtime applications
func (c *RouteConfig) ToApplications(redisClient redis.UniversalClient) []*limiter.Application {
	var apps []*limiter.Application
	for _, appConfig := range c.Applications {
		app := limiter.NewApplication(
			appConfig.Name,
			limiter.Filter{
				HostHeader: appConfig.Filter.HostHeader,
				PathPrefix: appConfig.Filter.PathPrefix,
				Methods:    appConfig.Filter.Methods,
			},
		)

		// Add instances to the application
		for _, instConfig := range appConfig.Instances {
			inst := limiter.NewInstance(
				instConfig.Name,
				limiter.Filter{
					HostHeader: instConfig.Filter.HostHeader,
					PathPrefix: instConfig.Filter.PathPrefix,
					Methods:    instConfig.Filter.Methods,
				},
			)

			// Add pools to the instance
			for _, poolConfig := range instConfig.Pools {
				pool := limiter.NewPool(
					poolConfig.Name,
					poolConfig.Weight,
				)

				// Add subpools to the pool
				for _, subpoolConfig := range poolConfig.Subpools {
					subpool := limiter.NewSubpool(
						subpoolConfig.Name,
						subpoolConfig.Weight,
						subpoolConfig.Limit,
						time.Duration(subpoolConfig.Window)*time.Second,
						subpoolConfig.InsecureSkipVerify,
						subpoolConfig.CheckInterval,
						time.Duration(subpoolConfig.SlowStartDuration)*time.Second,
						subpoolConfig.DeepHealthCheckPath,
						subpoolConfig.ShallowHealthCheckPath,
						subpoolConfig.HealthCheckPath, // Legacy field for backward compatibility
						time.Duration(subpoolConfig.HealthCheckInterval)*time.Second,
						time.Duration(subpoolConfig.HealthCheckTimeout)*time.Second,
						subpoolConfig.RequiredSuccessfulChecks,
						subpoolConfig.AllowedFailedChecks,
						subpoolConfig.RateLimitType,
					)

					// Add targets to the subpool
					for _, targetConfig := range subpoolConfig.Targets {
						subpool.AddTarget(targetConfig.Name, targetConfig.URL, targetConfig.StartTime, redisClient.(*redis.Client))
					}

					pool.AddSubpool(subpool)
				}

				inst.AddPool(pool)
			}
			app.AddInstance(inst)
		}

		apps = append(apps, app)
	}
	return apps
}
