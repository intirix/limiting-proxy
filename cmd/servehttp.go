package cmd

import (
	"log"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/spf13/cobra"
)

var (
	port      string
	directory string

	servehttpCmd = &cobra.Command{
		Use:   "servehttp",
		Short: "Start a simple HTTP server",
		Long:  `Start an HTTP server that serves static files from a directory and provides a health check endpoint.`,
		Run:   serveHTTP,
	}
)

func init() {
	servehttpCmd.Flags().StringVarP(&port, "port", "p", "8080", "port to listen on")
	servehttpCmd.Flags().StringVarP(&directory, "directory", "d", ".", "directory to serve files from")
	servehttpCmd.Flags().Bool("log-rate", false, "log request rate every second")
	rootCmd.AddCommand(servehttpCmd)
}

func serveHTTP(cmd *cobra.Command, args []string) {
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

	// Convert directory to absolute path
	absDir, err := filepath.Abs(directory)
	if err != nil {
		log.Fatal("Failed to get absolute path:", err)
	}

	// Create file server handler
	fileServer := http.FileServer(http.Dir(absDir))

	// Create mux for routing
	mux := http.NewServeMux()

	// Add health check endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if logRate {
			requestMutex.Lock()
			requestCount++
			requestMutex.Unlock()
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// Add file server handler for all other paths
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if logRate {
			requestMutex.Lock()
			requestCount++
			requestMutex.Unlock()
		}

		// Add CORS headers
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding")

		// Handle OPTIONS request
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		// Only allow GET requests for files
		if r.Method != "GET" {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		fileServer.ServeHTTP(w, r)
	}))

	// Start server
	addr := ":" + port
	log.Printf("Starting HTTP server on %s serving files from %s\n", addr, absDir)
	log.Printf("Health check endpoint available at http://localhost%s/health\n", addr)
	
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal("Server error:", err)
	}
}
