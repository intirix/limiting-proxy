package main

import (
	"flag"
	"log"
	"net/http"
	"strings"

	"github.com/redis/go-redis/v9"
	"limiting_proxy/config"
	"limiting_proxy/limiter"
)

func main() {
	listenAddr := flag.String("listen", ":8080", "listen address")
	configFile := flag.String("config", "config.yaml", "path to YAML config file")
	// Redis configuration flags
	redisLocal := flag.Bool("redis-local", false, "Use local Redis instance (localhost:6379)")
	redisAddrs := flag.String("redis-addrs", "", "Comma-separated list of Redis addresses (host:port)")
	redisPassword := flag.String("redis-password", "", "Redis password")
	redisDB := flag.Int("redis-db", 0, "Redis database number")
	redisKey := flag.String("redis-key", "limiting_proxy_config", "Redis key for config")
	redisPoolSize := flag.Int("redis-pool-size", 10, "Redis connection pool size")
	
	// Optional Redis Sentinel configuration
	redisSentinelAddrs := flag.String("redis-sentinel-addrs", "", "Comma-separated list of Redis Sentinel addresses")
	redisSentinelMaster := flag.String("redis-sentinel-master", "", "Name of Redis Sentinel master")
	flag.Parse()

	// No need to create a global proxy since we'll use per-target proxies

	// Create application manager
	manager := limiter.NewApplicationManager()

	// Setup configuration storage
	var storage config.Storage
	if *redisLocal || *redisAddrs != "" {
		// Prepare Redis configuration
		var redisAddresses []string
		if *redisLocal {
			redisAddresses = []string{"localhost:6379"}
			log.Printf("Using local Redis instance at localhost:6379\n")
		} else {
			redisAddresses = strings.Split(*redisAddrs, ",")
			log.Printf("Using Redis cluster with %d nodes\n", len(redisAddresses))
		}

		// Parse Redis Sentinel addresses if provided
		var sentinelAddresses []string
		if *redisSentinelAddrs != "" {
			sentinelAddresses = strings.Split(*redisSentinelAddrs, ",")
			log.Printf("Using Redis Sentinel with %d nodes\n", len(sentinelAddresses))
		}

		// Use Redis storage
		storage = config.NewRedisStorage(config.RedisConfig{
			Addresses:       redisAddresses,
			SentinelAddrs:   sentinelAddresses,
			SentinelMaster:  *redisSentinelMaster,
			Password:        *redisPassword,
			DB:             *redisDB,
			Key:            *redisKey,
			PoolSize:        *redisPoolSize,
		})
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

	// Prepare Redis addresses for rate limiting
	var redisAddresses []string
	if *redisLocal {
		redisAddresses = []string{"localhost:6379"}
	} else if *redisAddrs != "" {
		redisAddresses = strings.Split(*redisAddrs, ",")
	} else {
		redisAddresses = []string{"localhost:6379"} // Default to local Redis
	}

	// Create Redis client for rate limiting
	rlRedisClient := redis.NewUniversalClient(&redis.UniversalOptions{
		Addrs:      redisAddresses,
		MasterName: *redisSentinelMaster,
		Password:   *redisPassword,
		DB:         *redisDB,
		PoolSize:   *redisPoolSize,
		// Enable automatic failover and load balancing
		ReadOnly:   true,
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
