package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gemini-browser/server/internal/auth"
	"gemini-browser/server/internal/hub"
	mcpserver "gemini-browser/server/internal/mcp"
)

func getEnv(key, defaultVal string) string {
	val := os.Getenv(key)
	if val == "" {
		return defaultVal
	}
	return val
}

func main() {
	log.Println("==================================================")
	log.Println(" Gemini Live Chrome Browser MCP Server starting...")
	log.Println("==================================================")

	port := getEnv("PORT", "8080")
	host := getEnv("HOST", "0.0.0.0")
	dataDir := getEnv("DATA_DIR", "/data")
	baseURL := getEnv("BASE_URL", "")
	adminPassword := getEnv("ADMIN_PASSWORD", "admin123")
	extensionToken := getEnv("EXTENSION_SECRET_TOKEN", "supersecret-gemini-token")

	if os.Getenv("ADMIN_PASSWORD") == "" {
		log.Println("[SECURITY WARNING] ADMIN_PASSWORD not set in environment. Defaulting to 'admin123'.")
	}
	if os.Getenv("EXTENSION_SECRET_TOKEN") == "" {
		log.Println("[SECURITY WARNING] EXTENSION_SECRET_TOKEN not set in environment. Defaulting to 'supersecret-gemini-token'.")
	}

	// 1. Initialize persistent OAuth store
	store, err := auth.NewStore(dataDir)
	if err != nil {
		log.Fatalf("Failed to initialize storage: %v", err)
	}

	// 2. Initialize OAuth Server
	oauthServer := auth.NewOAuthServer(store, adminPassword, baseURL)

	// 3. Initialize Chrome Extension WebSocket Hub
	extHub := hub.NewHub(extensionToken)

	// 4. Initialize MCP Server with Streamable HTTP handler
	mcpWrapper := mcpserver.NewServer(extHub)
	mcpHandler := mcpWrapper.Handler()

	// 5. Setup HTTP Router
	mux := http.NewServeMux()

	// Health check (Coolify)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":              "ok",
			"extension_connected": extHub.IsConnected(),
			"version":             "1.0.0",
			"uptime":              time.Now().Unix(),
		})
	})

	// OAuth 2.0 endpoints for Google Gemini Custom Connected Apps
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", oauthServer.HandleMetadata)
	mux.HandleFunc("POST /register", oauthServer.HandleRegister)
	mux.HandleFunc("GET /authorize", oauthServer.HandleAuthorize)
	mux.HandleFunc("POST /authorize", oauthServer.HandleAuthorize)
	mux.HandleFunc("POST /token", oauthServer.HandleToken)

	// WebSocket endpoint for Chrome Extension
	mux.HandleFunc("GET /ws", extHub.HandleWebSocket)

	// Streamable HTTP MCP endpoint (protected by OAuth Bearer Token)
	// Supports POST, GET, DELETE /mcp
	mcpWithAuth := oauthServer.ValidateBearerToken(mcpHandler)
	mux.Handle("/mcp", mcpWithAuth)
	mux.Handle("/mcp/", mcpWithAuth)

	addr := fmt.Sprintf("%s:%s", host, port)
	server := &http.Server{
		Addr:         addr,
		Handler:      loggingMiddleware(corsMiddleware(mux)),
		ReadTimeout:  120 * time.Second,
		WriteTimeout: 120 * time.Second,
	}

	// Graceful shutdown listener
	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("Server listening on http://%s", addr)
		log.Printf("MCP Streamable HTTP Endpoint: http://%s/mcp", addr)
		log.Printf("Chrome Extension WebSocket:   ws://%s/ws", addr)
		log.Printf("OAuth Metadata Endpoint:       http://%s/.well-known/oauth-authorization-server", addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server ListenAndServe error: %v", err)
		}
	}()

	<-stopChan
	log.Println("Shutting down server gracefully...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Printf("Server shutdown error: %v", err)
	}
	log.Println("Server exiting.")
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Mcp-Session-Id")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		// Omit logging for periodic pings on /health to keep logs clean
		if r.URL.Path != "/health" {
			log.Printf("[%s] %s from %s took %v", r.Method, r.URL.Path, r.RemoteAddr, time.Since(start))
		}
	})
}
