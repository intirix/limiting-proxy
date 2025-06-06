package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// TLSConfigSettings holds configuration for TLS
type TLSConfigSettings struct {
	Enable       bool   `yaml:"enable"`
	GenerateCert bool   `yaml:"generate_cert"`
	CertFile     string `yaml:"cert_file"`
	KeyFile      string `yaml:"key_file"`
}

// ProxyConfig represents the main proxy configuration
type ProxyConfig struct {
	Listen      string      `yaml:"listen"`
	AdminListen    string            `yaml:"admin_listen"`
	ListenTLS      TLSConfigSettings `yaml:"listen_tls"`
	AdminListenTLS TLSConfigSettings `yaml:"admin_listen_tls"`
	Redis          RedisConfig       `yaml:"redis"`
	Routes      RouteConfig `yaml:"routes"`
}

// LoadProxyConfig loads the proxy configuration from a YAML file
func LoadProxyConfig(configPath string) (*ProxyConfig, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Return default config if file doesn't exist
			return &ProxyConfig{
				Listen: ":8080",
				Redis: RedisConfig{
					Addresses: []string{"localhost:6379"},
					DB:       0,
					Key:      "limiting_proxy_config",
					PoolSize: 10,
				},
			}, nil
		}
		return nil, fmt.Errorf("reading proxy config file: %w", err)
	}

	var config ProxyConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("parsing proxy config file: %w", err)
	}

	return &config, nil
}
