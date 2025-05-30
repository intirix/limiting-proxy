package cmd

import (
	"github.com/spf13/cobra"
)

var (
	configFile string
	rootCmd    = &cobra.Command{
		Use:   "limiting_proxy",
		Short: "A rate-limiting reverse proxy",
		Long:  `A rate-limiting reverse proxy that can be configured with different rate limiting strategies.`,
	}
)

func init() {
	rootCmd.PersistentFlags().StringVar(&configFile, "config", "limitproxy-config.yaml", "path to proxy configuration file")
}



// Execute runs the root command
func Execute() error {
	return rootCmd.Execute()
}
