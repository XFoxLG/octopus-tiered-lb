// Package geminicli 提供 Google Gemini CLI OAuth 流程辅助工具。
//
// 移植自 sub2api backend/internal/pkg/geminicli，去除 sub2api 特有依赖。
// 内置 Gemini CLI 公开 OAuth 客户端凭据（client_secret 需通过环境变量提供，
// 与 sub2api 一致的安全策略）。
package geminicli

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	AuthorizeURL = "https://accounts.google.com/o/oauth2/v2/auth"
	TokenURL     = "https://oauth2.googleapis.com/token"

	// 默认 redirect URI（copy/paste 回调流程）。
	DefaultRedirectURI = "http://localhost:1455/auth/callback"

	// Code Assist 默认 scopes
	DefaultCodeAssistScopes = "https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/userinfo.email https://www.googleapis.com/auth/userinfo.profile"

	// 内置 Gemini CLI 公开 OAuth 客户端凭据。
	GeminiCLIOAuthClientID = "681255809395-oo8ft2oprdrnp9e3aqf6av3hmdib135j.apps.googleusercontent.com"

	GeminiCLIOAuthClientSecretEnv = "GEMINI_CLI_OAUTH_CLIENT_SECRET"

	SessionTTL = 30 * time.Minute
)

// OAuthConfig Gemini OAuth 客户端配置。
type OAuthConfig struct {
	ClientID     string
	ClientSecret string
	Scopes       string
}

// OAuthSession 存储 Gemini OAuth 流程状态。
type OAuthSession struct {
	State        string    `json:"state"`
	CodeVerifier string    `json:"code_verifier"`
	RedirectURI  string    `json:"redirect_uri"`
	CreatedAt    time.Time `json:"created_at"`
}

// SessionStore 内存管理 OAuth 会话。
type SessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*OAuthSession
	stopOnce sync.Once
	stopCh   chan struct{}
}

func NewSessionStore() *SessionStore {
	store := &SessionStore{
		sessions: make(map[string]*OAuthSession),
		stopCh:   make(chan struct{}),
	}
	go store.cleanup()
	return store
}

func (s *SessionStore) Stop() {
	s.stopOnce.Do(func() { close(s.stopCh) })
}

func (s *SessionStore) Set(sessionID string, session *OAuthSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[sessionID] = session
}

func (s *SessionStore) Get(sessionID string) (*OAuthSession, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.sessions[sessionID]
	if !ok {
		return nil, false
	}
	if time.Since(session.CreatedAt) > SessionTTL {
		return nil, false
	}
	return session, true
}

func (s *SessionStore) Delete(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, sessionID)
}

func (s *SessionStore) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.mu.Lock()
			for id, session := range s.sessions {
				if time.Since(session.CreatedAt) > SessionTTL {
					delete(s.sessions, id)
				}
			}
			s.mu.Unlock()
		}
	}
}

func GenerateRandomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}

func GenerateState() (string, error) {
	b, err := GenerateRandomBytes(32)
	if err != nil {
		return "", err
	}
	return base64URLEncode(b), nil
}

func GenerateSessionID() (string, error) {
	b, err := GenerateRandomBytes(16)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func GenerateCodeVerifier() (string, error) {
	b, err := GenerateRandomBytes(32)
	if err != nil {
		return "", err
	}
	return base64URLEncode(b), nil
}

func GenerateCodeChallenge(verifier string) string {
	hash := sha256.Sum256([]byte(verifier))
	return base64URLEncode(hash[:])
}

func base64URLEncode(data []byte) string {
	return strings.TrimRight(base64.URLEncoding.EncodeToString(data), "=")
}

// EffectiveOAuthConfig 返回生效的 OAuth 配置。
// 未提供 client_id/secret 时回退内置 Gemini CLI 客户端（secret 需环境变量）。
func EffectiveOAuthConfig(cfg OAuthConfig) (OAuthConfig, error) {
	effective := OAuthConfig{
		ClientID:     strings.TrimSpace(cfg.ClientID),
		ClientSecret: strings.TrimSpace(cfg.ClientSecret),
		Scopes:       strings.TrimSpace(cfg.Scopes),
	}
	if effective.ClientID == "" && effective.ClientSecret == "" {
		secret := strings.TrimSpace(os.Getenv(GeminiCLIOAuthClientSecretEnv))
		if secret == "" {
			return OAuthConfig{}, fmt.Errorf("built-in Gemini CLI OAuth client_secret is not configured; set %s", GeminiCLIOAuthClientSecretEnv)
		}
		effective.ClientID = GeminiCLIOAuthClientID
		effective.ClientSecret = secret
	} else if effective.ClientID == "" || effective.ClientSecret == "" {
		return OAuthConfig{}, fmt.Errorf("OAuth client not configured: set both client_id and client_secret (or leave both empty for built-in client)")
	}
	if effective.Scopes == "" {
		effective.Scopes = DefaultCodeAssistScopes
	}
	return effective, nil
}

// BuildAuthorizationURL 构造 Gemini OAuth 授权 URL。
func BuildAuthorizationURL(cfg OAuthConfig, state, codeChallenge, redirectURI string) (string, error) {
	effectiveCfg, err := EffectiveOAuthConfig(cfg)
	if err != nil {
		return "", err
	}
	redirectURI = strings.TrimSpace(redirectURI)
	if redirectURI == "" {
		redirectURI = DefaultRedirectURI
	}
	params := url.Values{}
	params.Set("response_type", "code")
	params.Set("client_id", effectiveCfg.ClientID)
	params.Set("redirect_uri", redirectURI)
	params.Set("scope", effectiveCfg.Scopes)
	params.Set("state", state)
	params.Set("code_challenge", codeChallenge)
	params.Set("code_challenge_method", "S256")
	params.Set("access_type", "offline")
	params.Set("prompt", "consent")
	params.Set("include_granted_scopes", "true")
	return fmt.Sprintf("%s?%s", AuthorizeURL, params.Encode()), nil
}

// TokenResponse Google OAuth token 端点响应。
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	IDToken      string `json:"id_token,omitempty"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope,omitempty"`
}
