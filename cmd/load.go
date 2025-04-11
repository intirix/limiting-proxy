package cmd

import (
	"fmt"
	"log"

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

	// Save to Redis
	err = redisStorage.Save(cfg)
	if err != nil {
		log.Fatal("Failed to save configuration to Redis:", err)
	}

	fmt.Printf("Successfully loaded route configuration from %s into Redis\n", routeConfigFile)
}
