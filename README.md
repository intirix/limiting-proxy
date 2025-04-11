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

By default, the proxy server runs on port 8080 and implements a basic rate limiting strategy.
