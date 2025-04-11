package config

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// RedisStorage implements Storage interface using Redis
type RedisStorage struct {
	client *redis.Client
	key    string
}

// RedisConfig holds Redis connection configuration
type RedisConfig struct {
	Host     string
	Port     int
	Password string
	DB       int
	Key      string // Redis key to store configuration
}

// NewRedisStorage creates a new Redis storage instance
func NewRedisStorage(cfg RedisConfig) *RedisStorage {
	client := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Password: cfg.Password,
		DB:       cfg.DB,
	})

	return &RedisStorage{
		client: client,
		key:    cfg.Key,
	}
}

// Load loads configuration from Redis
func (s *RedisStorage) Load() (*Config, error) {
	ctx := context.Background()
	data, err := s.client.Get(ctx, s.key).Bytes()
	if err != nil {
		if err == redis.Nil {
			return &Config{Applications: make([]ApplicationConfig, 0)}, nil
		}
		return nil, fmt.Errorf("reading from redis: %w", err)
	}

	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("parsing redis data: %w", err)
	}

	return &config, nil
}

// Save saves configuration to Redis
func (s *RedisStorage) Save(config *Config) error {
	ctx := context.Background()
	data, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	if err := s.client.Set(ctx, s.key, data, 0).Err(); err != nil {
		return fmt.Errorf("writing to redis: %w", err)
	}

	return nil
}

// Close closes the Redis connection
func (s *RedisStorage) Close() error {
	return s.client.Close()
}

// Watch watches for configuration changes in Redis
func (s *RedisStorage) Watch(ctx context.Context, onChange func(*Config)) error {
	pubsub := s.client.Subscribe(ctx, s.key+"_changes")
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
		case msg := <-pubsub.Channel():
			var config Config
			if err := json.Unmarshal([]byte(msg.Payload), &config); err != nil {
				continue
			}
			onChange(&config)
		}
	}
}

// NotifyChange notifies other instances about configuration changes
func (s *RedisStorage) NotifyChange(config *Config) error {
	ctx := context.Background()
	data, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	return s.client.Publish(ctx, s.key+"_changes", data).Err()
}
