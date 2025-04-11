package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"

	"github.com/redis/go-redis/v9"
	"limiting_proxy/config"
	"limiting_proxy/limiter"
)

func main() {
	listenAddr := flag.String("listen", ":8080", "listen address")
	configFile := flag.String("config", "config.yaml", "path to YAML config file")
	redisHost := flag.String("redis-host", "", "Redis host (if using Redis for config)")
	redisPort := flag.Int("redis-port", 6379, "Redis port")
	redisPassword := flag.String("redis-password", "", "Redis password")
	redisDB := flag.Int("redis-db", 0, "Redis database number")
	redisKey := flag.String("redis-key", "limiting_proxy_config", "Redis key for config")
	flag.Parse()

	// No need to create a global proxy since we'll use per-target proxies

	// Create application manager
	manager := limiter.NewApplicationManager()

	// Setup configuration storage
	var storage config.Storage
	if *redisHost != "" {
		// Use Redis storage
		storage = config.NewRedisStorage(config.RedisConfig{
			Host:     *redisHost,
			Port:     *redisPort,
			Password: *redisPassword,
			DB:       *redisDB,
			Key:      *redisKey,
		})
		log.Printf("Using Redis for configuration storage: %s:%d\n", *redisHost, *redisPort)
	} else {
		// Use YAML storage
		storage = config.NewYAMLStorage(*configFile)
		log.Printf("Using YAML file for configuration storage: %s\n", *configFile)
	}

	// Load initial configuration
	cfg, err := storage.Load()
	if err != nil {
		log.Fatal("Failed to load configuration:", err)
	}

	// Create Redis client for rate limiting
	rlRedisClient := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", *redisHost, *redisPort),
		Password: *redisPassword,
		DB:       *redisDB,
	})

	// Convert configuration to applications
	apps := cfg.ToApplications(rlRedisClient)
	for _, app := range apps {
		manager.AddApplication(app)
	}

	// Create handler
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Find the appropriate application for this request
		app := manager.GetApplication(r.Host, r.URL.Path, r.Method)
		if app == nil {
			http.Error(w, "No rate limit configuration found", http.StatusInternalServerError)
			return
		}

		// Use path and method as the key for different rate limits per endpoint
		key := r.Method + ":" + r.URL.Path

		// Get rate limiter and proxy for this endpoint from matching instance
		strategy, proxy := app.GetLimiter(key, r.Host, r.URL.Path, r.Method)
		if strategy == nil || proxy == nil {
			http.Error(w, "No target available", http.StatusServiceUnavailable)
			return
		}

		proxy.ServeHTTP(w, r)
	})

	log.Printf("Starting proxy server on %s\n", *listenAddr)

	if err := http.ListenAndServe(*listenAddr, nil); err != nil {
		log.Fatal(err)
	}
}
