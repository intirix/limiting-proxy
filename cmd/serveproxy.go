package cmd

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"time"

	"github.com/spf13/cobra"
)

var (
	proxyPort string
	targetURL string
)

var serveproxyCmd = &cobra.Command{
	Use:   "serveproxy",
	Short: "Start a basic HTTP reverse proxy",
	Long:  `Start a basic HTTP reverse proxy server for benchmarking purposes`,
	Run:   serveProxy,
}

func init() {
	serveproxyCmd.Flags().StringVarP(&proxyPort, "port", "p", "8081", "port to listen on")
	serveproxyCmd.Flags().StringVarP(&targetURL, "target", "t", "", "target URL to proxy to")
	serveproxyCmd.Flags().Bool("log-rate", false, "log request rate every second")
	serveproxyCmd.MarkFlagRequired("target")
	rootCmd.AddCommand(serveproxyCmd)
}

func serveProxy(cmd *cobra.Command, args []string) {
	// Get log rate flag
	logRate, err := cmd.Flags().GetBool("log-rate")
	if err != nil {
		log.Fatal("Error getting log-rate flag:", err)
	}

	// Create request counter for rate logging
	var requestCount int64
	var requestMutex sync.Mutex

	// Start rate logging if enabled
	if logRate {
		go func() {
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()

			for range ticker.C {
				requestMutex.Lock()
				count := requestCount
				requestCount = 0
				requestMutex.Unlock()

				if count > 0 {
					log.Printf("Received %d requests in the last second\n", count)
				}
			}
		}()
	}

	// Parse target URL
	target, err := url.Parse(targetURL)
	if err != nil {
		log.Fatal("Invalid target URL:", err)
	}

	// Create reverse proxy
	proxy := httputil.NewSingleHostReverseProxy(target)

	// Create handler that wraps the proxy
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if logRate {
			requestMutex.Lock()
			requestCount++
			requestMutex.Unlock()
		}

		// Add CORS headers
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding, Authorization")

		// Handle OPTIONS request
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		proxy.ServeHTTP(w, r)
	})

	// Start server
	log.Printf("Starting proxy server on port %s, proxying to %s\n", proxyPort, targetURL)
	if err := http.ListenAndServe(":"+proxyPort, handler); err != nil {
		log.Fatal("Failed to start server:", err)
	}
}
