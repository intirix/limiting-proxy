package cmd

import (
	"fmt"
	"log"
	"time"

	"github.com/spf13/cobra"
	"limiting_proxy/config"
)

var (
	routeConfigFile string
	loadCmd = &cobra.Command{
		Use:   "load",
		Short: "Load route configuration into Redis",
		Long:  `Load a route configuration from a YAML file into Redis for use by the proxy server.`,
		Run:   loadConfig,
	}
)

func init() {
	loadCmd.Flags().StringVar(&routeConfigFile, "route-config", "route-config.yaml", "path to route configuration file")
	rootCmd.AddCommand(loadCmd)
}

func loadConfig(cmd *cobra.Command, args []string) {
	// Load proxy configuration for Redis settings
	proxyConfig, err := config.LoadProxyConfig(configFile)
	if err != nil {
		log.Fatal("Failed to load proxy configuration:", err)
	}

	if !proxyConfig.Redis.Local && len(proxyConfig.Redis.Addresses) == 0 {
		log.Fatal("Redis configuration is required for loading route config")
	}

	// Create Redis storage
	redisStorage := config.NewRedisStorage(proxyConfig.Redis)

	// Load route configuration from YAML
	yamlStorage := config.NewYAMLStorage(routeConfigFile)
	cfg, err := yamlStorage.Load()
	if err != nil {
		log.Fatal("Failed to load route configuration from YAML:", err)
	}


	// Get the config that is currently in Redis
	currentCfg, err := redisStorage.Load()
	if err != nil {
		log.Fatal("Failed to load current configuration from Redis:", err)
	}

	// For any Targets in the new config, reuse the startTime from the current config
	for i := range cfg.Applications {
		app := &cfg.Applications[i]
		for j := range app.Instances {
			instance := &app.Instances[j]
			for k := range instance.Pools {
				pool := &instance.Pools[k]
				for l := range pool.Subpools {
					subpool := &pool.Subpools[l]
					for m := range subpool.Targets {
						target := &subpool.Targets[m]
						target.StartTime = time.Now()
						if currentCfg != nil {
							for _, currentApp := range currentCfg.Applications {
								for _, currentInstance := range currentApp.Instances {
									for _, currentPool := range currentInstance.Pools {
										for _, currentSubpool := range currentPool.Subpools {
											for _, currentTarget := range currentSubpool.Targets {
												if currentTarget.Name == target.Name {
													if currentTarget.StartTime.IsZero() {
														target.StartTime = time.Now()
														log.Printf("Setting startTime for new target %s: %v\n", target.Name, target.StartTime)
													} else {
														log.Printf("Reusing startTime for target %s: %v\n", target.Name, currentTarget.StartTime)
														target.StartTime = currentTarget.StartTime
													}
													break
												}
											}
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}

	// Save to Redis
	err = redisStorage.Save(cfg)
	if err != nil {
		log.Fatal("Failed to save configuration to Redis:", err)
	}

	// Notify other instances about the change
	err = redisStorage.NotifyChange(cfg)
	if err != nil {
		log.Printf("Warning: Failed to notify about configuration change: %v\n", err)
	}

	fmt.Printf("Successfully loaded route configuration from %s into Redis\n", routeConfigFile)
}
