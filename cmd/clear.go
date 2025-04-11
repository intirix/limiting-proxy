package cmd

import (
	"fmt"
	"log"

	"github.com/spf13/cobra"
	"limiting_proxy/config"
)

var clearCmd = &cobra.Command{
	Use:   "clear",
	Short: "Clear route configuration from Redis",
	Long:  `Remove all route configuration data from Redis.`,
	Run:   clearConfig,
}

func init() {
	rootCmd.AddCommand(clearCmd)
}

func clearConfig(cmd *cobra.Command, args []string) {
	// Load proxy configuration for Redis settings
	proxyConfig, err := config.LoadProxyConfig(configFile)
	if err != nil {
		log.Fatal("Failed to load proxy configuration:", err)
	}

	if !proxyConfig.Redis.Local && len(proxyConfig.Redis.Addresses) == 0 {
		log.Fatal("Redis configuration is required for clearing route config")
	}

	// Create Redis storage
	redisStorage := config.NewRedisStorage(proxyConfig.Redis)

	// Clear configuration from Redis
	err = redisStorage.Clear()
	if err != nil {
		log.Fatal("Failed to clear configuration from Redis:", err)
	}

	fmt.Println("Successfully cleared route configuration from Redis")
}
