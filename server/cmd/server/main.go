package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Maher-W/BrowseGemini/server/internal/auth"
	"github.com/Maher-W/BrowseGemini/server/internal/hub"
	mcpserver "github.com/Maher-W/BrowseGemini/server/internal/mcp"
)

func getEnv(key, defaultVal string) string {
	val := os.Getenv(key)
	if val == "" {
		return defaultVal
	}
	return val
}

var homeTemplate = template.Must(template.New("home").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>BrowseGemini Server</title>
  <style>
    :root {
      --bg: #0f172a;
      --card: #1e293b;
      --border: #334155;
      --text: #f8fafc;
      --muted: #94a3b8;
      --primary: #3b82f6;
      --success: #10b981;
    }
    * { box-sizing: border-box; margin: 0; padding: 0; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; }
    body { background: var(--bg); color: var(--text); min-height: 100vh; display: flex; align-items: center; justify-content: center; padding: 20px; }
    .card { background: var(--card); border: 1px solid var(--border); border-radius: 14px; max-width: 540px; width: 100%; padding: 32px; box-shadow: 0 25px 35px -5px rgba(0,0,0,0.5); }
    .header { display: flex; align-items: center; gap: 14px; margin-bottom: 20px; }
    .logo { width: 44px; height: 44px; border-radius: 10px; background: linear-gradient(135deg, #3b82f6, #8b5cf6); display: flex; align-items: center; justify-content: center; color: white; flex-shrink: 0; }
    h1 { font-size: 22px; font-weight: 700; }
    .sub { font-size: 13px; color: var(--muted); }
    .status-badge { display: inline-flex; align-items: center; gap: 8px; padding: 6px 14px; border-radius: 9999px; font-size: 13px; font-weight: 600; margin-bottom: 20px; background: rgba(16, 185, 129, 0.15); color: #34d399; border: 1px solid rgba(16, 185, 129, 0.3); }
    .dot { width: 8px; height: 8px; border-radius: 50%; background: var(--success); box-shadow: 0 0 8px var(--success); }
    .info-box { background: #0b1120; border: 1px solid var(--border); border-radius: 8px; padding: 14px; margin-bottom: 20px; }
    .info-title { font-size: 11px; text-transform: uppercase; letter-spacing: 0.5px; color: var(--muted); margin-bottom: 8px; font-weight: 600; }
    .endpoint-item { display: flex; justify-content: space-between; align-items: center; margin-bottom: 8px; font-size: 13px; font-family: monospace; }
    .endpoint-item:last-child { margin-bottom: 0; }
    .endpoint-label { color: var(--muted); font-family: sans-serif; font-size: 12px; }
    .endpoint-val { color: #60a5fa; word-break: break-all; }
    .manual-box { background: rgba(59, 130, 246, 0.08); border: 1px dashed rgba(59, 130, 246, 0.4); border-radius: 8px; padding: 12px 14px; margin-bottom: 20px; }
    .manual-box strong { color: #93c5fd; }
    .steps { list-style: none; counter-reset: step-counter; }
    .steps li { counter-increment: step-counter; position: relative; padding-left: 28px; margin-bottom: 12px; font-size: 13px; color: #cbd5e1; line-height: 1.5; }
    .steps li::before { content: counter(step-counter); position: absolute; left: 0; top: 0; width: 18px; height: 18px; border-radius: 50%; background: var(--primary); color: white; font-size: 11px; font-weight: 700; display: flex; align-items: center; justify-content: center; }
    .footer { margin-top: 24px; text-align: center; font-size: 12px; color: #64748b; }
    a { color: var(--primary); text-decoration: none; }
    a:hover { text-decoration: underline; }
  </style>
</head>
<body>
  <div class="card">
    <div class="header">
      <div class="logo">
        <svg viewBox="0 0 24 24" width="24" height="24" fill="none" stroke="currentColor" stroke-width="2">
          <circle cx="12" cy="12" r="10"/>
          <path d="M12 2a14.5 14.5 0 0 0 0 20 14.5 14.5 0 0 0 0-20"/>
          <path d="M2 12h20"/>
        </svg>
      </div>
      <div>
        <h1>BrowseGemini Server</h1>
        <div class="sub">Remote Model Context Protocol (MCP) Bridge</div>
      </div>
    </div>

    <div class="status-badge">
      <span class="dot"></span>
      <span>Server Online &amp; Ready</span>
    </div>

    <div class="info-box">
      <div class="info-title">Server Endpoints</div>
      <div class="endpoint-item">
        <span class="endpoint-label">Gemini MCP URL:</span>
        <span class="endpoint-val">{{.BaseURL}}/mcp</span>
      </div>
      <div class="endpoint-item">
        <span class="endpoint-label">Extension WS URL:</span>
        <span class="endpoint-val">{{.WsURL}}</span>
      </div>
      <div class="endpoint-item">
        <span class="endpoint-label">Health Check:</span>
        <span class="endpoint-val"><a href="/health" target="_blank">/health</a></span>
      </div>
    </div>

    <div class="manual-box">
      <div class="info-title" style="color: #93c5fd; margin-bottom: 6px;">OAuth Credentials for Gemini</div>
      <div class="endpoint-item">
        <span class="endpoint-label">Client ID:</span>
        <strong class="endpoint-val" style="color: #ffffff;">gemini</strong>
      </div>
      <div class="endpoint-item">
        <span class="endpoint-label">Client Secret:</span>
        <strong class="endpoint-val" style="color: #ffffff;">gemini-secret</strong>
      </div>
    </div>

    <div class="info-title" style="margin-bottom: 10px;">Setup Instructions</div>
    <ol class="steps">
      <li><strong>Install Chrome Extension:</strong> Load <code>extension/</code> in Chrome (<code>chrome://extensions</code>).</li>
      <li><strong>Configure Extension:</strong> Set Server URL to <code>{{.WsURL}}</code>, enter <code>EXTENSION_SECRET_TOKEN</code>, and toggle <strong>ON</strong>.</li>
      <li><strong>Connect Gemini:</strong> Go to <a href="https://gemini.google.com" target="_blank">gemini.google.com</a> &rarr; Settings &rarr; Connected apps &rarr; Add custom connected app.</li>
      <li><strong>Enter MCP URL:</strong> Paste <code>{{.BaseURL}}/mcp</code>. (If prompted for Client ID/Secret, enter <code>gemini</code> and <code>gemini-secret</code>).</li>
      <li><strong>Authorize:</strong> Enter your <code>ADMIN_PASSWORD</code> when prompted to finalize.</li>
    </ol>

    <div class="footer">
      BrowseGemini &bull; Open Source on <a href="https://github.com/Maher-W/BrowseGemini" target="_blank">GitHub</a>
    </div>
  </div>
</body>
</html>`))

func main() {
	log.Println("==================================================")
	log.Println(" BrowseGemini MCP Server starting...")
	log.Println("==================================================")

	port := getEnv("PORT", "8080")
	host := getEnv("HOST", "0.0.0.0")
	dataDir := getEnv("DATA_DIR", "/data")
	baseURL := getEnv("BASE_URL", "")
	adminPassword := getEnv("ADMIN_PASSWORD", "admin123")
	extensionToken := getEnv("EXTENSION_SECRET_TOKEN", "supersecret-token")

	if os.Getenv("ADMIN_PASSWORD") == "" {
		log.Println("[SECURITY WARNING] ADMIN_PASSWORD not set in environment. Defaulting to 'admin123'.")
	}
	if os.Getenv("EXTENSION_SECRET_TOKEN") == "" {
		log.Println("[SECURITY WARNING] EXTENSION_SECRET_TOKEN not set in environment. Defaulting to 'supersecret-token'.")
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

	// Landing Page on GET /
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		currentBase := baseURL
		if currentBase == "" {
			scheme := "http"
			if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
				scheme = "https"
			}
			currentBase = fmt.Sprintf("%s://%s", scheme, r.Host)
		}
		wsScheme := "ws"
		if len(currentBase) >= 5 && currentBase[:5] == "https" {
			wsScheme = "wss"
		}
		hostOnly := r.Host
		if currentBase != "" {
			if len(currentBase) > 8 && currentBase[:8] == "https://" {
				hostOnly = currentBase[8:]
			} else if len(currentBase) > 7 && currentBase[:7] == "http://" {
				hostOnly = currentBase[7:]
			}
		}

		data := map[string]string{
			"BaseURL": currentBase,
			"WsURL":   fmt.Sprintf("%s://%s/ws", wsScheme, hostOnly),
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = homeTemplate.Execute(w, data)
	})

	// Health check (Coolify / Docker)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":              "ok",
			"extension_connected": extHub.IsConnected(),
			"version":             "1.0.0",
			"uptime":              time.Now().Unix(),
		})
	})

	// RFC 9728 OAuth 2.0 Protected Resource Metadata (both root and relative to /mcp)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", oauthServer.HandleProtectedResource)
	mux.HandleFunc("GET /mcp/.well-known/oauth-protected-resource", oauthServer.HandleProtectedResource)

	// RFC 8414 OAuth 2.0 Authorization Server Metadata (both root, relative, and OpenID fallback)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", oauthServer.HandleMetadata)
	mux.HandleFunc("GET /mcp/.well-known/oauth-authorization-server", oauthServer.HandleMetadata)
	mux.HandleFunc("GET /.well-known/openid-configuration", oauthServer.HandleMetadata)
	mux.HandleFunc("GET /mcp/.well-known/openid-configuration", oauthServer.HandleMetadata)

	// OAuth dynamic registration, authorize, and token
	mux.HandleFunc("POST /register", oauthServer.HandleRegister)
	mux.HandleFunc("GET /authorize", oauthServer.HandleAuthorize)
	mux.HandleFunc("POST /authorize", oauthServer.HandleAuthorize)
	mux.HandleFunc("POST /token", oauthServer.HandleToken)

	// WebSocket endpoint for Chrome Extension
	mux.HandleFunc("GET /ws", extHub.HandleWebSocket)

	// Streamable HTTP MCP endpoint (protected by OAuth Bearer Token)
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
		log.Printf("Protected Resource Metadata:   http://%s/.well-known/oauth-protected-resource", addr)
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
		if r.URL.Path != "/health" {
			log.Printf("[%s] %s from %s took %v", r.Method, r.URL.Path, r.RemoteAddr, time.Since(start))
		}
	})
}
