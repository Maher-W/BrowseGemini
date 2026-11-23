package auth

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Client struct {
	ClientID     string   `json:"client_id"`
	ClientSecret string   `json:"client_secret"`
	ClientName   string   `json:"client_name"`
	RedirectURIs []string `json:"redirect_uris"`
	CreatedAt    int64    `json:"created_at"`
}

type AuthCode struct {
	Code                string `json:"code"`
	ClientID            string `json:"client_id"`
	RedirectURI         string `json:"redirect_uri"`
	CodeChallenge       string `json:"code_challenge,omitempty"`
	CodeChallengeMethod string `json:"code_challenge_method,omitempty"`
	ExpiresAt           int64  `json:"expires_at"`
}

type TokenRecord struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ClientID     string `json:"client_id"`
	ExpiresAt    int64  `json:"expires_at"`
	Scope        string `json:"scope"`
}

type StoreData struct {
	Clients       map[string]Client      `json:"clients"`
	AuthCodes     map[string]AuthCode    `json:"auth_codes"`
	Tokens        map[string]TokenRecord `json:"tokens"`         // Key: AccessToken
	RefreshTokens map[string]string      `json:"refresh_tokens"` // Key: RefreshToken -> AccessToken
}

type Store struct {
	filePath string
	mu       sync.RWMutex
	data     StoreData
}

func NewStore(dataDir string) (*Store, error) {
	// Try creating dataDir if it doesn't exist
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		// Fallback to local ./data if dataDir is not writable (e.g. running on Windows without admin)
		dataDir = "./data"
		_ = os.MkdirAll(dataDir, 0755)
	}

	filePath := filepath.Join(dataDir, "oauth_store.json")
	store := &Store{
		filePath: filePath,
		data: StoreData{
			Clients:       make(map[string]Client),
			AuthCodes:     make(map[string]AuthCode),
			Tokens:        make(map[string]TokenRecord),
			RefreshTokens: make(map[string]string),
		},
	}

	if err := store.load(); err != nil {
		log.Printf("[OAuth Store] Initialized empty store (error reading %s: %v)", filePath, err)
	} else {
		log.Printf("[OAuth Store] Loaded %d clients and %d active tokens from %s",
			len(store.data.Clients), len(store.data.Tokens), filePath)
	}

	return store, nil
}

func (s *Store) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	content, err := os.ReadFile(s.filePath)
	if err != nil {
		return err
	}

	var data StoreData
	if err := json.Unmarshal(content, &data); err != nil {
		return err
	}

	if data.Clients == nil {
		data.Clients = make(map[string]Client)
	}
	if data.AuthCodes == nil {
		data.AuthCodes = make(map[string]AuthCode)
	}
	if data.Tokens == nil {
		data.Tokens = make(map[string]TokenRecord)
	}
	if data.RefreshTokens == nil {
		data.RefreshTokens = make(map[string]string)
	}

	s.data = data
	return nil
}

func (s *Store) saveLocked() error {
	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}

	tmpFile := s.filePath + ".tmp"
	if err := os.WriteFile(tmpFile, raw, 0644); err != nil {
		return err
	}

	return os.Rename(tmpFile, s.filePath)
}

func (s *Store) RegisterClient(name string, redirectURIs []string) (*Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	clientID := "client_" + randomHex(12)
	clientSecret := "secret_" + randomHex(24)

	client := Client{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		ClientName:   name,
		RedirectURIs: redirectURIs,
		CreatedAt:    time.Now().Unix(),
	}

	s.data.Clients[clientID] = client
	if err := s.saveLocked(); err != nil {
		log.Printf("[OAuth Store] Failed to save client: %v", err)
	}

	return &client, nil
}

func (s *Store) GetClient(clientID string) (Client, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	c, ok := s.data.Clients[clientID]
	return c, ok
}

func (s *Store) CreateAuthCode(clientID, redirectURI, challenge, challengeMethod string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	code := "code_" + randomHex(20)
	s.data.AuthCodes[code] = AuthCode{
		Code:                code,
		ClientID:            clientID,
		RedirectURI:         redirectURI,
		CodeChallenge:       challenge,
		CodeChallengeMethod: challengeMethod,
		ExpiresAt:           time.Now().Add(10 * time.Minute).Unix(),
	}

	_ = s.saveLocked()
	return code
}

func (s *Store) ConsumeAuthCode(code string) (AuthCode, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	authCode, ok := s.data.AuthCodes[code]
	if !ok {
		return AuthCode{}, false
	}

	delete(s.data.AuthCodes, code)
	_ = s.saveLocked()

	if time.Now().Unix() > authCode.ExpiresAt {
		return AuthCode{}, false
	}

	return authCode, true
}

func (s *Store) IssueToken(clientID, scope string) (*TokenRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	accessToken := "mcp_at_" + randomHex(32)
	refreshToken := "mcp_rt_" + randomHex(32)
	// 30 days token expiry
	expiresAt := time.Now().Add(30 * 24 * time.Hour).Unix()

	record := TokenRecord{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ClientID:     clientID,
		ExpiresAt:    expiresAt,
		Scope:        scope,
	}

	s.data.Tokens[accessToken] = record
	s.data.RefreshTokens[refreshToken] = accessToken

	if err := s.saveLocked(); err != nil {
		log.Printf("[OAuth Store] Error saving token: %v", err)
	}

	return &record, nil
}

func (s *Store) RefreshToken(refreshToken string) (*TokenRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	oldAccess, ok := s.data.RefreshTokens[refreshToken]
	if !ok {
		return nil, fmt.Errorf("invalid refresh token")
	}

	oldRecord, exists := s.data.Tokens[oldAccess]
	if !exists {
		return nil, fmt.Errorf("token record not found")
	}

	// Invalidate old access token
	delete(s.data.Tokens, oldAccess)

	newAccessToken := "mcp_at_" + randomHex(32)
	newRefreshToken := "mcp_rt_" + randomHex(32)
	expiresAt := time.Now().Add(30 * 24 * time.Hour).Unix()

	newRecord := TokenRecord{
		AccessToken:  newAccessToken,
		RefreshToken: newRefreshToken,
		ClientID:     oldRecord.ClientID,
		ExpiresAt:    expiresAt,
		Scope:        oldRecord.Scope,
	}

	delete(s.data.RefreshTokens, refreshToken)
	s.data.Tokens[newAccessToken] = newRecord
	s.data.RefreshTokens[newRefreshToken] = newAccessToken

	_ = s.saveLocked()
	return &newRecord, nil
}

func (s *Store) IsValidToken(accessToken string) bool {
	if accessToken == "" {
		return false
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	record, ok := s.data.Tokens[accessToken]
	if !ok {
		return false
	}

	if time.Now().Unix() > record.ExpiresAt {
		return false
	}

	return true
}

func randomHex(bytesLen int) string {
	b := make([]byte, bytesLen)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
