package config

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// RedisStorage implements Storage interface using Redis
type RedisStorage struct {
	client redis.UniversalClient
	prefix string // Key prefix for all application configs
}

// RedisSentinelConfig holds Redis Sentinel configuration
type RedisSentinelConfig struct {
	Addresses  []string `yaml:"addresses,omitempty"`
	MasterName string   `yaml:"masterName,omitempty"`
}

// RedisConfig holds Redis connection configuration
type RedisConfig struct {
	Local     bool              `yaml:"local,omitempty"`
	Addresses []string         `yaml:"addresses,omitempty"`
	Password  string           `yaml:"password,omitempty"`
	DB        int              `yaml:"db"`
	Key       string           `yaml:"key"` // Redis key to store configuration
	PoolSize  int              `yaml:"poolSize"`
	Sentinel  RedisSentinelConfig `yaml:"sentinel,omitempty"`
}

// NewRedisStorage creates a new Redis storage instance
func NewRedisStorage(cfg RedisConfig) *RedisStorage {
	// Determine Redis addresses
	addresses := cfg.Addresses
	if cfg.Local {
		addresses = []string{"localhost:6379"}
	}

	client := redis.NewUniversalClient(&redis.UniversalOptions{
		Addrs:      addresses,
		MasterName: cfg.Sentinel.MasterName,
		Password:   cfg.Password,
		DB:         cfg.DB,
		PoolSize:   cfg.PoolSize,
		// Enable automatic failover and load balancing
		ReadOnly:   true,
	})

	return &RedisStorage{
		client: client,
		prefix: cfg.Key + ":",
	}
}

// Load loads route configuration from Redis
func (s *RedisStorage) Load() (*RouteConfig, error) {
	ctx := context.Background()

	// Get all keys matching our prefix
	keys, err := s.client.Keys(ctx, s.prefix+"*").Result()
	if err != nil {
		return nil, fmt.Errorf("listing redis keys: %w", err)
	}

	// Create empty config if no keys found
	if len(keys) == 0 {
		return &RouteConfig{Applications: make([]ApplicationConfig, 0)}, nil
	}

	// Load all applications in parallel using a pipeline
	pipe := s.client.Pipeline()
	gets := make(map[string]*redis.StringCmd)
	for _, key := range keys {
		gets[key] = pipe.Get(ctx, key)
	}

	_, err = pipe.Exec(ctx)
	if err != nil && err != redis.Nil {
		return nil, fmt.Errorf("executing redis pipeline: %w", err)
	}

	// Collect all applications
	apps := make([]ApplicationConfig, 0, len(keys))
	for key, cmd := range gets {
		data, err := cmd.Bytes()
		if err != nil {
			if err == redis.Nil {
				continue
			}
			return nil, fmt.Errorf("reading application data: %w", err)
		}

		var app ApplicationConfig
		if err := json.Unmarshal(data, &app); err != nil {
			return nil, fmt.Errorf("parsing application data for key %s: %w", key, err)
		}
		apps = append(apps, app)
	}

	return &RouteConfig{Applications: apps}, nil
}

// Save saves route configuration to Redis
func (s *RedisStorage) Save(config *RouteConfig) error {
	ctx := context.Background()

	// First, get existing keys to clean up removed applications
	existingKeys, err := s.client.Keys(ctx, s.prefix+"*").Result()
	if err != nil {
		return fmt.Errorf("listing redis keys: %w", err)
	}

	// Create a set of new application names for quick lookup
	newApps := make(map[string]struct{}, len(config.Applications))
	for _, app := range config.Applications {
		newApps[app.Name] = struct{}{}
	}

	// Delete keys for applications that no longer exist
	for _, key := range existingKeys {
		appName := key[len(s.prefix):] // Remove prefix to get app name
		if _, exists := newApps[appName]; !exists {
			if err := s.client.Del(ctx, key).Err(); err != nil {
				return fmt.Errorf("deleting removed application %s: %w", appName, err)
			}
		}
	}

	// Save each application in parallel using a pipeline
	pipe := s.client.Pipeline()
	for _, app := range config.Applications {
		data, err := json.Marshal(app)
		if err != nil {
			return fmt.Errorf("marshaling application %s: %w", app.Name, err)
		}
		pipe.Set(ctx, s.prefix+app.Name, data, 0)
	}

	_, err = pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("executing redis pipeline: %w", err)
	}

	return nil
}

// Close closes the Redis connection
func (s *RedisStorage) Close() error {
	return s.client.Close()
}

// Watch watches for route configuration changes in Redis
func (s *RedisStorage) Watch(ctx context.Context, onChange func(*RouteConfig)) error {
	// Subscribe to changes channel
	changeChannel := s.prefix + "changes"
	pubsub := s.client.Subscribe(ctx, changeChannel)
	defer pubsub.Close()

	// Initial load
	config, err := s.Load()
	if err != nil {
		return err
	}
	onChange(config)

	// Watch for changes
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-pubsub.Channel():
			// Load fresh configuration when we receive a change notification
			config, err := s.Load()
			if err != nil {
				continue
			}
			onChange(config)
		}
	}
}

// Clear removes all route configuration from Redis
func (s *RedisStorage) Clear() error {
	ctx := context.Background()

	// Get all keys matching our prefix
	keys, err := s.client.Keys(ctx, s.prefix+"*").Result()
	if err != nil {
		return fmt.Errorf("listing redis keys: %w", err)
	}

	if len(keys) > 0 {
		// Delete all keys in a single operation
		if err := s.client.Del(ctx, keys...).Err(); err != nil {
			return fmt.Errorf("clearing redis keys: %w", err)
		}
	}

	return nil
}

// NotifyChange notifies other instances about route configuration changes
func (s *RedisStorage) NotifyChange(config *RouteConfig) error {
	ctx := context.Background()
	// Just publish a notification - subscribers will load the fresh config
	return s.client.Publish(ctx, s.prefix+"changes", "updated").Err()
}
