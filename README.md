# Limiting Proxy

A Go-based HTTP proxy server that implements rate limiting functionality. This proxy can be used to control and manage traffic to backend services by implementing various rate limiting strategies.

## Features

- HTTP proxy functionality
- Rate limiting with configurable limits
- Request forwarding to backend services
- Simple configuration

## Getting Started

1. Clone the repository
2. Run `go mod tidy` to install dependencies
3. Run `go run main.go` to start the proxy server

## Configuration

The proxy server supports both file-based and Redis-based configuration storage.

### Route Configuration

Route configuration can be stored in a YAML file (default: `route-config.yaml`) or in Redis. Use the `--route-config` flag to specify a different YAML file path.

### Redis Configuration

For Redis-based configuration storage and rate limiting, the following options are available:

- `--redis-local`: Use local Redis instance (localhost:6379)
- `--redis-addrs`: Comma-separated list of Redis addresses (host:port)
- `--redis-password`: Redis password
- `--redis-db`: Redis database number
- `--redis-key`: Redis key for config storage
- `--redis-pool-size`: Connection pool size

#### Optional Redis Sentinel Support

- `--redis-sentinel-addrs`: Comma-separated list of Redis Sentinel addresses
- `--redis-sentinel-master`: Name of Redis Sentinel master

### Example Usage

```bash
# Using local file configuration
go run main.go --route-config my-routes.yaml

# Using local Redis instance
go run main.go --redis-local

# Using Redis cluster
go run main.go --redis-addrs "redis1:6379,redis2:6379" --redis-password "mypass"

# Using Redis with Sentinel
go run main.go \
  --redis-addrs "redis1:6379,redis2:6379" \
  --redis-sentinel-addrs "sentinel1:26379,sentinel2:26379" \
  --redis-sentinel-master "mymaster"
```
