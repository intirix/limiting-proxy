package cmd

import (
	"fmt"
	"log"

	"github.com/spf13/cobra"
	"limiting_proxy/config"
)

var (
	outputFile string
	dumpCmd = &cobra.Command{
		Use:   "dump",
		Short: "Dump route configuration from Redis to YAML",
		Long:  `Retrieve the current route configuration from Redis and save it to a YAML file.`,
		Run:   dumpConfig,
	}
)

func init() {
	dumpCmd.Flags().StringVar(&outputFile, "output", "route-config.yaml", "path to output YAML file")
	rootCmd.AddCommand(dumpCmd)
}

func dumpConfig(cmd *cobra.Command, args []string) {
	// Load proxy configuration for Redis settings
	proxyConfig, err := config.LoadProxyConfig(configFile)
	if err != nil {
		log.Fatal("Failed to load proxy configuration:", err)
	}

	if !proxyConfig.Redis.Local && len(proxyConfig.Redis.Addresses) == 0 {
		log.Fatal("Redis configuration is required for dumping route config")
	}

	// Create Redis storage
	redisStorage := config.NewRedisStorage(proxyConfig.Redis)

	// Load configuration from Redis
	cfg, err := redisStorage.Load()
	if err != nil {
		log.Fatal("Failed to load configuration from Redis:", err)
	}

	// Create YAML storage for output
	yamlStorage := config.NewYAMLStorage(outputFile)

	// Save to YAML file
	err = yamlStorage.Save(cfg)
	if err != nil {
		log.Fatal("Failed to save configuration to YAML:", err)
	}

	fmt.Printf("Successfully dumped route configuration from Redis to %s\n", outputFile)
}
