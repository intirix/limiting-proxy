package cmd

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/http"
	httpprof "net/http/pprof" // For HTTP pprof handlers
	"os"
	"strings"
	"sync"

	"limiting_proxy/internal/tlsutil"

	"github.com/redis/go-redis/v9"
	"github.com/spf13/cobra"
	"limiting_proxy/config"
	"limiting_proxy/limiter"
)

var (
	adminListen string

	runCmd = &cobra.Command{
		Use:   "run",
		Short: "Run the proxy server",
		Long:  `Start the rate-limiting proxy server with the specified configuration.`,
		Run:   runProxy,
	}
)

func init() {
	runCmd.Flags().StringVar(&adminListen, "admin-listen", "", "address for admin HTTP server (e.g., :8081 or /path/to/socket.sock for Unix domain socket)")
	// Add an alias for the flag to support both formats
	runCmd.Flags().StringVar(&adminListen, "admin-listener", "", "alias for admin-listen (e.g., :8081 or /path/to/socket.sock for Unix domain socket)")
	rootCmd.AddCommand(runCmd)
}

func runProxy(cmd *cobra.Command, args []string) {
	// Create a WaitGroup to wait for all servers to shut down
	var wg sync.WaitGroup
	var mainProxyErr error
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
					// maps to store all the target's health statuses
					targetHealth := make(map[string]bool)
					targetDeepHealth := make(map[string]bool)
					targetShallowHealth := make(map[string]bool)
					targetDeepSuccesses := make(map[string]int)
					targetDeepFailures := make(map[string]int)
					targetShallowSuccesses := make(map[string]int)
					targetShallowFailures := make(map[string]int)

					// Stop health check timers in old applications
					for _, app := range manager.Applications {
						for _, instance := range app.Instances {
							for _, pool := range instance.Pools {
								for _, subpool := range pool.Subpools {
									subpool.Stop()
									// save the health status of the target
									for _, target := range subpool.Targets {
										url := target.URL.String()
										targetHealth[url] = target.IsHealthy
										targetDeepHealth[url] = target.DeepHealthy
										targetShallowHealth[url] = target.ShallowHealthy
										targetDeepSuccesses[url] = target.GetConsecutiveDeepSuccesses()
										targetDeepFailures[url] = target.GetConsecutiveDeepFailures()
										targetShallowSuccesses[url] = target.GetConsecutiveShallowSuccesses()
										targetShallowFailures[url] = target.GetConsecutiveShallowFailures()
									}
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
									// restore the health status of the target
									for _, target := range subpool.Targets {
										url := target.URL.String()
										if _, ok := targetHealth[url]; ok {
											// Restore legacy health status
											target.IsHealthy = targetHealth[url]

											// Restore deep health status
											if deepHealthy, ok := targetDeepHealth[url]; ok {
												target.DeepHealthy = deepHealthy
											}

											// Restore shallow health status
											if shallowHealthy, ok := targetShallowHealth[url]; ok {
												target.ShallowHealthy = shallowHealthy
											}

											// Restore deep health counters
											if successes, ok := targetDeepSuccesses[url]; ok {
												target.SetConsecutiveDeepSuccesses(successes)
											}
											if failures, ok := targetDeepFailures[url]; ok {
												target.SetConsecutiveDeepFailures(failures)
											}

											// Restore shallow health counters
											if successes, ok := targetShallowSuccesses[url]; ok {
												target.SetConsecutiveShallowSuccesses(successes)
											}
											if failures, ok := targetShallowFailures[url]; ok {
												target.SetConsecutiveShallowFailures(failures)
											}
										}
									}
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

	// Determine if we should start the admin server
	adminAddress := adminListen // Get value from command-line flag
	if adminAddress == "" {      // If flag is not set, use value from config
		adminAddress = proxyConfig.AdminListen
	}

	if adminAddress != "" {
		// Create a separate ServeMux for the admin server
		adminMux := http.NewServeMux()

		// Register pprof handlers
		adminMux.HandleFunc("/debug/pprof/", httpprof.Index)
		adminMux.HandleFunc("/debug/pprof/cmdline", httpprof.Cmdline)
		adminMux.HandleFunc("/debug/pprof/profile", httpprof.Profile)
		adminMux.HandleFunc("/debug/pprof/symbol", httpprof.Symbol)
		adminMux.HandleFunc("/debug/pprof/trace", httpprof.Trace)
		// Register handlers for heap, goroutine, etc.
		adminMux.Handle("/debug/pprof/goroutine", httpprof.Handler("goroutine"))
		adminMux.Handle("/debug/pprof/heap", httpprof.Handler("heap"))
		adminMux.Handle("/debug/pprof/threadcreate", httpprof.Handler("threadcreate"))
		adminMux.Handle("/debug/pprof/block", httpprof.Handler("block"))
		adminMux.Handle("/debug/pprof/mutex", httpprof.Handler("mutex"))

		// Add admin endpoints
		adminMux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("OK"))
		})

		// Add status endpoint that shows application statistics
		adminMux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)

			fmt.Fprintf(w, "Limiting Proxy Status\n")
			fmt.Fprintf(w, "====================\n\n")

			// Print applications information
			fmt.Fprintf(w, "Applications: %d\n", len(manager.Applications))
			for i, app := range manager.Applications {
				fmt.Fprintf(w, "\nApplication %d: %s\n", i+1, app.Name)
				fmt.Fprintf(w, "  Instances: %d\n", len(app.Instances))
				for j, instance := range app.Instances {
					fmt.Fprintf(w, "  Instance %d:\n", j+1)
					fmt.Fprintf(w, "    Host: %s\n", instance.Filter.HostHeader)
					fmt.Fprintf(w, "    Path: %s\n", instance.Filter.PathPrefix)
					fmt.Fprintf(w, "    Methods: %v\n", instance.Filter.Methods)
					fmt.Fprintf(w, "    Pools: %d\n", len(instance.Pools))
				}
			}
		})

		// Start admin server in a goroutine
		wg.Add(1)
		go func() {
			defer wg.Done()
			log.Printf("Starting admin server on %s\n", adminAddress)

			// Check if adminAddress is a Unix socket path
			// A simple check: if it starts with '/' or './' and doesn't contain ':', assume Unix socket
			// For more robustness, one might use a prefix like "unix:"
			if (strings.HasPrefix(adminAddress, "/") || strings.HasPrefix(adminAddress, "./")) && !strings.Contains(adminAddress, ":") {
				// Remove the socket file if it already exists
				if _, err := os.Stat(adminAddress); err == nil {
					if err := os.Remove(adminAddress); err != nil {
						log.Printf("Failed to remove existing socket file %s: %v\n", adminAddress, err)
						return
					}
				}
				listener, err := net.Listen("unix", adminAddress)
				if err != nil {
					log.Printf("Failed to listen on Unix socket %s: %v\n", adminAddress, err)
					return
				}
				defer listener.Close()
				// Ensure the socket file has appropriate permissions (e.g., 0777 or more restrictive)
				if err := os.Chmod(adminAddress, 0777); err != nil {
					log.Printf("Failed to chmod socket file %s: %v\n", adminAddress, err)
				}
				log.Printf("Admin server listening on Unix socket %s\n", adminAddress)
				if err := http.Serve(listener, adminMux); err != nil {
					log.Printf("Admin server (Unix socket) error: %v\n", err)
				}
			} else {
				// Assume TCP socket
				log.Printf("Admin server listening on TCP %s\n", adminAddress)
				if err := http.ListenAndServe(adminAddress, adminMux); err != nil {
					log.Printf("Admin server (TCP) error: %v\n", err)
				}
			}
		}()
	}

	// Start the main proxy server
	mainProxyServer := &http.Server{
		Addr:    proxyConfig.Listen,
		Handler: http.DefaultServeMux, // Assuming http.DefaultServeMux is used for main proxy handlers
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		log.Printf("Attempting to start main proxy server on %s\n", proxyConfig.Listen)
		if proxyConfig.ListenTLS.Enable {
			if proxyConfig.ListenTLS.CertFile != "" && proxyConfig.ListenTLS.KeyFile != "" {
				log.Printf("Main proxy server listening with provided TLS certificate on https://%s\n", proxyConfig.Listen)
				mainProxyServer.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12} // Example: Enforce TLS 1.2+
				mainProxyErr = mainProxyServer.ListenAndServeTLS(proxyConfig.ListenTLS.CertFile, proxyConfig.ListenTLS.KeyFile)
			} else if proxyConfig.ListenTLS.GenerateCert {
				log.Printf("Generating self-signed certificate for main proxy server on %s\n", proxyConfig.Listen)
				certPEM, keyPEM, genErr := tlsutil.GenerateSelfSignedCertBytes()
				if genErr != nil {
					log.Fatalf("Failed to generate self-signed certificate for main proxy: %v", genErr)
				}
				cert, err := tls.X509KeyPair(certPEM, keyPEM)
				if err != nil {
					log.Fatalf("Failed to load generated key pair for main proxy: %v", err)
				}
				mainProxyServer.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}}
				log.Printf("Main proxy server listening with generated TLS certificate on https://%s\n", proxyConfig.Listen)
				mainProxyErr = mainProxyServer.ListenAndServeTLS("", "") // Certs are in TLSConfig
			} else {
				log.Printf("Main proxy server configured for TLS but no certs provided or to generate. Starting plain HTTP on %s as fallback.\n", proxyConfig.Listen)
				mainProxyErr = mainProxyServer.ListenAndServe()
			}
		} else {
			log.Printf("Main proxy server listening on http://%s\n", proxyConfig.Listen)
			mainProxyErr = mainProxyServer.ListenAndServe()
		}

		if mainProxyErr != nil && mainProxyErr != http.ErrServerClosed {
			log.Printf("Main proxy server error: %v\n", mainProxyErr)
		}
	}()

	// Wait for admin server to shut down (though we'll likely never reach this point)
	wg.Wait()
}
