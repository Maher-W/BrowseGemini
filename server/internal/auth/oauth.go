package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strings"
)

type OAuthServer struct {
	store         *Store
	adminPassword string
	baseURL       string
}

func sanitizeURL(u string) string {
	u = strings.TrimSpace(u)
	for strings.Contains(u, "..") {
		u = strings.ReplaceAll(u, "..", ".")
	}
	return strings.TrimSuffix(u, "/")
}

func NewOAuthServer(store *Store, adminPassword, baseURL string) *OAuthServer {
	return &OAuthServer{
		store:         store,
		adminPassword: adminPassword,
		baseURL:       sanitizeURL(baseURL),
	}
}

func (s *OAuthServer) resolveBaseURL(r *http.Request) string {
	if s.baseURL != "" {
		return sanitizeURL(s.baseURL)
	}

	scheme := "http"
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	} else if r.TLS != nil {
		scheme = "https"
	}

	host := r.Host
	if fHost := r.Header.Get("X-Forwarded-Host"); fHost != "" {
		host = fHost
	}

	return sanitizeURL(fmt.Sprintf("%s://%s", scheme, host))
}

// 1. RFC 9728 OAuth 2.0 Protected Resource Metadata
func (s *OAuthServer) HandleProtectedResource(w http.ResponseWriter, r *http.Request) {
	base := s.resolveBaseURL(r)

	metadata := map[string]any{
		"resource":                 base + "/mcp",
		"authorization_servers":    []string{base},
		"scopes_supported":         []string{"browser"},
		"bearer_methods_supported": []string{"header"},
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(metadata)
}

// 2. RFC 8414 OAuth 2.0 Authorization Server Metadata
func (s *OAuthServer) HandleMetadata(w http.ResponseWriter, r *http.Request) {
	base := s.resolveBaseURL(r)

	metadata := map[string]any{
		"issuer":                                base,
		"authorization_endpoint":                base + "/authorize",
		"token_endpoint":                        base + "/token",
		"registration_endpoint":                 base + "/register",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_post", "client_secret_basic", "none"},
		"code_challenge_methods_supported":      []string{"S256", "plain"},
		"scopes_supported":                      []string{"browser"},
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(metadata)
}

// 3. Dynamic Client Registration (RFC 7591)
func (s *OAuthServer) HandleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req map[string]any
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid_request","error_description":"Malformed JSON body"}`, http.StatusBadRequest)
		return
	}

	var redirectURIs []string
	if rawURIs, ok := req["redirect_uris"].([]any); ok {
		for _, u := range rawURIs {
			if str, ok := u.(string); ok && str != "" {
				redirectURIs = append(redirectURIs, str)
			}
		}
	} else if singleURI, ok := req["redirect_uri"].(string); ok && singleURI != "" {
		redirectURIs = append(redirectURIs, singleURI)
	}

	if len(redirectURIs) == 0 {
		redirectURIs = []string{
			"https://vertexaisearch.cloud.google.com/oauth-redirect",
			"https://gemini.google.com/oauth/callback",
		}
	}

	name := "Google Gemini"
	if n, ok := req["client_name"].(string); ok && n != "" {
		name = n
	}

	client, err := s.store.RegisterClient(name, redirectURIs)
	if err != nil {
		http.Error(w, `{"error":"server_error"}`, http.StatusInternalServerError)
		return
	}

	resp := map[string]any{
		"client_id":                  client.ClientID,
		"client_secret":              client.ClientSecret,
		"client_name":                client.ClientName,
		"redirect_uris":              client.RedirectURIs,
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "client_secret_post",
		"client_id_issued_at":        client.CreatedAt,
		"client_secret_expires_at":   0,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(resp)
}

// 4. Authorization Endpoint (GET /authorize and POST /authorize)
var loginTemplate = template.Must(template.New("login").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Authorize Gemini Browser Access</title>
  <style>
    :root {
      --bg: #0f172a;
      --card-bg: #1e293b;
      --border: #334155;
      --primary: #3b82f6;
      --primary-hover: #2563eb;
      --text: #f8fafc;
      --text-muted: #94a3b8;
      --error: #ef4444;
      --error-bg: rgba(239, 68, 68, 0.15);
    }
    * { box-sizing: border-box; margin: 0; padding: 0; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; }
    body { background: var(--bg); color: var(--text); min-height: 100vh; display: flex; align-items: center; justify-content: center; padding: 20px; }
    .card { background: var(--card-bg); border: 1px solid var(--border); border-radius: 12px; max-width: 420px; width: 100%; padding: 32px 28px; box-shadow: 0 20px 25px -5px rgba(0,0,0,0.5); }
    .logo { width: 44px; height: 44px; border-radius: 10px; background: linear-gradient(135deg, #3b82f6, #8b5cf6); display: flex; align-items: center; justify-content: center; margin-bottom: 20px; }
    .logo svg { stroke: white; }
    h1 { font-size: 20px; font-weight: 700; margin-bottom: 8px; }
    p { font-size: 13px; color: var(--text-muted); line-height: 1.5; margin-bottom: 24px; }
    .app-badge { display: inline-flex; align-items: center; gap: 6px; background: #0b1120; border: 1px solid var(--border); padding: 6px 12px; border-radius: 6px; font-size: 12px; font-weight: 500; margin-bottom: 20px; width: 100%; }
    .error-banner { background: var(--error-bg); border: 1px solid rgba(239, 68, 68, 0.4); color: #fca5a5; padding: 10px 14px; border-radius: 6px; font-size: 13px; margin-bottom: 18px; }
    .form-group { margin-bottom: 20px; }
    label { display: block; font-size: 12px; font-weight: 600; color: #cbd5e1; margin-bottom: 6px; }
    input[type="password"] { width: 100%; padding: 10px 14px; background: #0b1120; border: 1px solid var(--border); border-radius: 6px; color: var(--text); font-size: 14px; outline: none; transition: border-color 0.2s; }
    input[type="password"]:focus { border-color: var(--primary); }
    .btn { width: 100%; padding: 11px; background: var(--primary); color: white; border: none; border-radius: 6px; font-size: 14px; font-weight: 600; cursor: pointer; transition: background 0.2s; }
    .btn:hover { background: var(--primary-hover); }
    .footer { margin-top: 24px; text-align: center; font-size: 11px; color: #64748b; }
  </style>
</head>
<body>
  <div class="card">
    <div class="logo">
      <svg viewBox="0 0 24 24" width="24" height="24" fill="none" stroke="currentColor" stroke-width="2">
        <circle cx="12" cy="12" r="10"/>
        <path d="M12 2a14.5 14.5 0 0 0 0 20 14.5 14.5 0 0 0 0-20"/>
        <path d="M2 12h20"/>
      </svg>
    </div>
    <h1>Connect Google Gemini</h1>
    <p>Google Gemini is requesting access to control your live Chrome browser via BrowseGemini.</p>

    <div class="app-badge">
      <span>Requesting Client:</span>
      <strong style="color: #60a5fa;">{{.ClientName}}</strong>
    </div>

    {{if .Error}}
    <div class="error-banner">{{.Error}}</div>
    {{end}}

    <form method="POST" action="/authorize">
      <input type="hidden" name="client_id" value="{{.ClientID}}">
      <input type="hidden" name="redirect_uri" value="{{.RedirectURI}}">
      <input type="hidden" name="state" value="{{.State}}">
      <input type="hidden" name="code_challenge" value="{{.CodeChallenge}}">
      <input type="hidden" name="code_challenge_method" value="{{.CodeChallengeMethod}}">

      <div class="form-group">
        <label for="password">Admin Password (from server env)</label>
        <input type="password" id="password" name="password" placeholder="Enter ADMIN_PASSWORD" required autofocus>
      </div>

      <button type="submit" class="btn">Authorize Gemini Connection</button>
    </form>

    <div class="footer">
      Protected by BrowseGemini &bull; Port 8080
    </div>
  </div>
</body>
</html>`))

type AuthPageData struct {
	ClientID            string
	ClientName          string
	RedirectURI         string
	State               string
	CodeChallenge       string
	CodeChallengeMethod string
	Error               string
}

func (s *OAuthServer) HandleAuthorize(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		q := r.URL.Query()
		clientID := q.Get("client_id")
		redirectURI := q.Get("redirect_uri")
		state := q.Get("state")
		codeChallenge := q.Get("code_challenge")
		codeChallengeMethod := q.Get("code_challenge_method")

		if clientID == "" || redirectURI == "" {
			http.Error(w, "Missing client_id or redirect_uri", http.StatusBadRequest)
			return
		}

		client, exists := s.store.GetClient(clientID)
		clientName := "Google Gemini"
		if exists && client.ClientName != "" {
			clientName = client.ClientName
		}

		data := AuthPageData{
			ClientID:            clientID,
			ClientName:          clientName,
			RedirectURI:         redirectURI,
			State:               state,
			CodeChallenge:       codeChallenge,
			CodeChallengeMethod: codeChallengeMethod,
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = loginTemplate.Execute(w, data)
		return
	}

	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Invalid form data", http.StatusBadRequest)
			return
		}

		clientID := r.FormValue("client_id")
		redirectURI := r.FormValue("redirect_uri")
		state := r.FormValue("state")
		codeChallenge := r.FormValue("code_challenge")
		codeChallengeMethod := r.FormValue("code_challenge_method")
		password := r.FormValue("password")

		// Validate password
		if subtle.ConstantTimeCompare([]byte(password), []byte(s.adminPassword)) != 1 {
			data := AuthPageData{
				ClientID:            clientID,
				ClientName:          "Google Gemini",
				RedirectURI:         redirectURI,
				State:               state,
				CodeChallenge:       codeChallenge,
				CodeChallengeMethod: codeChallengeMethod,
				Error:               "Invalid Admin Password. Please try again.",
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			_ = loginTemplate.Execute(w, data)
			return
		}

		// Generate auth code
		code := s.store.CreateAuthCode(clientID, redirectURI, codeChallenge, codeChallengeMethod)

		targetURL, err := url.Parse(redirectURI)
		if err != nil {
			http.Error(w, "Invalid redirect_uri", http.StatusBadRequest)
			return
		}

		query := targetURL.Query()
		query.Set("code", code)
		if state != "" {
			query.Set("state", state)
		}
		targetURL.RawQuery = query.Encode()

		http.Redirect(w, r, targetURL.String(), http.StatusFound)
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

// 5. Token Endpoint (POST /token)
func (s *OAuthServer) HandleToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, `{"error":"invalid_request"}`, http.StatusBadRequest)
		return
	}

	grantType := r.FormValue("grant_type")

	if grantType == "authorization_code" {
		code := r.FormValue("code")
		redirectURI := r.FormValue("redirect_uri")
		clientID := r.FormValue("client_id")
		clientSecret := r.FormValue("client_secret")
		codeVerifier := r.FormValue("code_verifier")

		// Also check Basic Auth for client_id if not in form
		if clientID == "" {
			if u, p, ok := r.BasicAuth(); ok {
				clientID = u
				if clientSecret == "" {
					clientSecret = p
				}
			}
		}

		authCode, valid := s.store.ConsumeAuthCode(code)
		if !valid {
			s.jsonError(w, http.StatusBadRequest, "invalid_grant", "Authorization code is invalid or expired")
			return
		}

		if redirectURI != "" && authCode.RedirectURI != "" && redirectURI != authCode.RedirectURI {
			s.jsonError(w, http.StatusBadRequest, "invalid_grant", "redirect_uri mismatch")
			return
		}

		// PKCE validation
		if authCode.CodeChallenge != "" {
			if !validatePKCE(codeVerifier, authCode.CodeChallenge, authCode.CodeChallengeMethod) {
				s.jsonError(w, http.StatusBadRequest, "invalid_grant", "PKCE verification failed")
				return
			}
		}

		tokenRecord, err := s.store.IssueToken(clientID, "browser")
		if err != nil {
			s.jsonError(w, http.StatusInternalServerError, "server_error", "Failed to generate tokens")
			return
		}

		resp := map[string]any{
			"access_token":  tokenRecord.AccessToken,
			"token_type":    "Bearer",
			"expires_in":    tokenRecord.ExpiresAt - tokenRecord.CreatedAt(),
			"refresh_token": tokenRecord.RefreshToken,
			"scope":         "browser",
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
		return
	}

	if grantType == "refresh_token" {
		refreshToken := r.FormValue("refresh_token")
		if refreshToken == "" {
			s.jsonError(w, http.StatusBadRequest, "invalid_request", "Missing refresh_token")
			return
		}

		tokenRecord, err := s.store.RefreshToken(refreshToken)
		if err != nil {
			s.jsonError(w, http.StatusBadRequest, "invalid_grant", "Invalid or expired refresh_token")
			return
		}

		resp := map[string]any{
			"access_token":  tokenRecord.AccessToken,
			"token_type":    "Bearer",
			"expires_in":    tokenRecord.ExpiresAt - tokenRecord.CreatedAt(),
			"refresh_token": tokenRecord.RefreshToken,
			"scope":         tokenRecord.Scope,
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
		return
	}

	s.jsonError(w, http.StatusBadRequest, "unsupported_grant_type", "Grant type not supported")
}

func (t *TokenRecord) CreatedAt() int64 {
	return t.ExpiresAt - 30*24*3600
}

func (s *OAuthServer) jsonError(w http.ResponseWriter, status int, errCode, description string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":             errCode,
		"error_description": description,
	})
}

func validatePKCE(verifier, challenge, method string) bool {
	if challenge == "" {
		return true
	}
	if method == "plain" {
		return subtle.ConstantTimeCompare([]byte(verifier), []byte(challenge)) == 1
	}

	// S256: BASE64URL-ENCODE(SHA256(verifier)) without padding
	h := sha256.Sum256([]byte(verifier))
	computed := base64.RawURLEncoding.EncodeToString(h[:])
	return subtle.ConstantTimeCompare([]byte(computed), []byte(challenge)) == 1
}

// 6. Middleware to validate Bearer token on /mcp with RFC 9728 resource_metadata challenge
func (s *OAuthServer) ValidateBearerToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := s.resolveBaseURL(r)
		authHeader := r.Header.Get("Authorization")

		if !strings.HasPrefix(authHeader, "Bearer ") {
			log.Printf("[Auth] Unauthenticated request on %s - returning RFC 9728 resource_metadata challenge", r.URL.Path)
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s/.well-known/oauth-protected-resource"`, base))
			http.Error(w, `{"error":"invalid_token","error_description":"Bearer token required"}`, http.StatusUnauthorized)
			return
		}

		token := strings.TrimPrefix(authHeader, "Bearer ")
		if !s.store.IsValidToken(token) {
			log.Printf("[Auth] Invalid/expired token on %s", r.URL.Path)
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s/.well-known/oauth-protected-resource", error="invalid_token"`, base))
			http.Error(w, `{"error":"invalid_token","error_description":"Token is expired or invalid"}`, http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}
