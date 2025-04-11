package cmd

import (
	"context"
	"log"
	"net/http"

	"github.com/redis/go-redis/v9"
	"github.com/spf13/cobra"
	"limiting_proxy/config"
	"limiting_proxy/limiter"
)

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run the proxy server",
	Long:  `Start the rate-limiting proxy server with the specified configuration.`,
	Run:   runProxy,
}

func init() {
	rootCmd.AddCommand(runCmd)
}

func runProxy(cmd *cobra.Command, args []string) {
	// Load proxy configuration
	proxyConfig, err := config.LoadProxyConfig(configFile)
	if err != nil {
		log.Fatal("Failed to load proxy configuration:", err)
	}

	// Create application manager
	manager := limiter.NewApplicationManager()

	// Create Redis client for rate limiting
	var redisAddresses []string
	if proxyConfig.Redis.Local {
		redisAddresses = []string{"localhost:6379"}
	} else if len(proxyConfig.Redis.Addresses) > 0 {
		redisAddresses = proxyConfig.Redis.Addresses
	} else {
		redisAddresses = []string{"localhost:6379"} // Default to local Redis
	}

	rlRedisClient := redis.NewUniversalClient(&redis.UniversalOptions{
		Addrs:      redisAddresses,
		MasterName: proxyConfig.Redis.Sentinel.MasterName,
		Password:   proxyConfig.Redis.Password,
		DB:         proxyConfig.Redis.DB,
		PoolSize:   proxyConfig.Redis.PoolSize,
		ReadOnly:   true,
	})

	// Setup configuration storage
	var storage config.Storage
	if proxyConfig.Redis.Local || len(proxyConfig.Redis.Addresses) > 0 {
		// Use Redis storage
		redisStorage := config.NewRedisStorage(proxyConfig.Redis)
		storage = redisStorage

		// Log Redis configuration
		if proxyConfig.Redis.Local {
			log.Printf("Using local Redis instance at localhost:6379\n")
		} else {
			log.Printf("Using Redis cluster with %d nodes\n", len(proxyConfig.Redis.Addresses))
		}

		if len(proxyConfig.Redis.Sentinel.Addresses) > 0 {
			log.Printf("Using Redis Sentinel with %d nodes\n", len(proxyConfig.Redis.Sentinel.Addresses))
		}

		// Start watching for Redis configuration changes
		go func() {
			log.Printf("Watching for Redis configuration changes\n")
			if err := redisStorage.Watch(context.Background(), func(newCfg *config.RouteConfig) {
				if newCfg != nil {
					log.Printf("Updating configuration from Redis\n")
					// Stop health check timers in old applications
					for _, app := range manager.Applications {
						for _, instance := range app.Instances {
							for _, pool := range instance.Pools {
								for _, subpool := range pool.Subpools {
									subpool.Stop()
								}
							}
						}
					}
					// Create new applications with the updated config
					newApps := newCfg.ToApplications(rlRedisClient)
					// Update the application manager
					manager.Applications = newApps
					// Start health checks for all subpools
					for _, app := range newApps {
						for _, instance := range app.Instances {
							for _, pool := range instance.Pools {
								for _, subpool := range pool.Subpools {
									subpool.StartHealthChecks()
								}
							}
						}
					}
				}
			}); err != nil {
				log.Printf("Error watching Redis configuration: %v\n", err)
			}
		}()
	} else {
		// Use embedded route configuration or default to route-config.yaml
		routeConfigFile := "route-config.yaml"
		storage = config.NewYAMLStorage(routeConfigFile)
		log.Printf("Using YAML file for configuration storage: %s\n", routeConfigFile)
	}

	// Load initial configuration
	cfg, err := storage.Load()
	if err != nil {
		log.Fatal("Failed to load configuration:", err)
	}

	// Convert configuration to applications
	apps := cfg.ToApplications(rlRedisClient)

	// Start health checks for all subpools
	for _, app := range apps {
		for _, instance := range app.Instances {
			for _, pool := range instance.Pools {
				for _, subpool := range pool.Subpools {
					subpool.StartHealthChecks()
				}
			}
		}
	}
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

	log.Printf("Starting proxy server on %s\n", proxyConfig.Listen)

	if err := http.ListenAndServe(proxyConfig.Listen, nil); err != nil {
		log.Fatal(err)
	}
}
