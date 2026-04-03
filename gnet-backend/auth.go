package main

import (
	"log"
	"sync"
	"time"
)

// ============================================
// Authentication
// ============================================

type AuthManager struct {
	tokens map[string]*TokenInfo
	mu     sync.RWMutex
}

type TokenInfo struct {
	UserID    string
	Username  string
	ExpiresAt time.Time
}

func NewAuthManager() *AuthManager {
	am := &AuthManager{
		tokens: make(map[string]*TokenInfo),
	}

	// Add some demo tokens for testing
	am.AddToken("test_token_user123", "user_123", "testuser", 24*time.Hour)
	am.AddToken("test_token_user456", "user_456", "john_doe", 24*time.Hour)

	return am
}

func (am *AuthManager) ValidateToken(token string) (*TokenInfo, bool) {
	am.mu.RLock()
	defer am.mu.RUnlock()

	info, exists := am.tokens[token]
	if !exists {
		return nil, false
	}

	if time.Now().After(info.ExpiresAt) {
		return nil, false
	}

	return info, true
}

func (am *AuthManager) AddToken(token, userID, username string, duration time.Duration) {
	am.mu.Lock()
	defer am.mu.Unlock()

	am.tokens[token] = &TokenInfo{
		UserID:    userID,
		Username:  username,
		ExpiresAt: time.Now().Add(duration),
	}
	log.Printf("🔑 Added auth token for user: %s (expires in %v)", username, duration)
}
