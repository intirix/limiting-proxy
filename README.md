# Limiting Proxy

A Go-based HTTP proxy server that implements rate limiting functionality. This proxy can be used to control and manage traffic to backend services by implementing various rate limiting strategies.

## Features

- HTTP proxy functionality with TLS support
- Multiple rate limiting strategies:
  - Fixed window rate limiting
  - Sliding window rate limiting
  - No limit (round-robin)
- Redis-based distributed rate limiting
- Configurable slow start for gradual traffic ramp-up
- Health checking of backend targets:
  - Customizable health check endpoints
  - Configurable check intervals and timeouts
  - Required successful checks before enabling targets
  - Automatic removal of unhealthy targets
- Dynamic target management:
  - Multiple pools and subpools
  - Weighted load distribution
  - Automatic failover to healthy targets
- Flexible routing based on:
  - Host headers
  - Path prefixes
  - HTTP methods

## Getting Started

1. Clone the repository
2. Run `go mod tidy` to install dependencies
3. Run `go run main.go` to start the proxy server

## Configuration

The proxy server is configured using YAML files. Configuration includes:

### Rate Limiting Configuration
- `limit`: Number of requests allowed per window
- `window`: Duration of the rate limiting window (in seconds)
- `rateLimitType`: Strategy to use (fixed-window, sliding-window, no-limit)
- `checkInterval`: How often to sync with Redis
- `slowStartDuration`: Duration for gradual traffic ramp-up

### Health Check Configuration
- `deepHealthCheckPath`: Path for deep health check requests (e.g., /deep-health)
- `shallowHealthCheckPath`: Path for shallow health check requests (e.g., /shallow-health)
- `healthCheckPath`: Legacy path for health check requests (deprecated, use the above instead)
- `healthCheckInterval`: Interval between health checks (in seconds)
- `healthCheckTimeout`: Timeout for health check requests
- `requiredSuccessfulChecks`: Number of successful checks before enabling a target
- `allowedFailedChecks`: Number of consecutive failures before disabling a target

Deep and shallow health checks serve different purposes:
- **Deep Health Checks**: Targets must pass deep health checks to be initially put into service. These are more thorough checks that verify the target is fully operational.
- **Shallow Health Checks**: Once a target has passed a deep health check, only shallow health checks are used to determine if it should be taken out of service. These are typically lighter-weight checks that verify basic responsiveness.

### TLS Configuration
- `insecureSkipVerify`: Option to skip SSL certificate validation

### Route Configuration

Route configuration can be stored in a YAML file (default: `route-config.yaml`) or in Redis. Use the `--route-config` flag to specify a different YAML file path.

### Redis Configuration

Redis is used for both configuration storage and distributed rate limiting. Configure Redis in `limitproxy-config.yaml`:

```yaml
redis:
  # Use local Redis instance
  local: true
  
  # Or specify Redis cluster addresses
  # addresses:
  #   - "redis1:6379"
  #   - "redis2:6379"
  
  # Authentication
  password: "mypassword"
  
  # Database selection
  db: 0
  
  # Key prefix for config storage
  key: "limiting_proxy_config"
  
  # Connection pool size
  poolSize: 10
  
  # Optional Redis Sentinel configuration
  sentinel:
    addresses:
      - "sentinel1:26379"
      - "sentinel2:26379"
    masterName: "mymaster"
```

The proxy supports:
- Single Redis instance (local or remote)
- Redis cluster configuration
- Redis Sentinel for high availability
- Connection pooling
- Authentication and database selection

