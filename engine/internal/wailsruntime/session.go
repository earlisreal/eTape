package wailsruntime

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"
)

var (
	ErrInvalidSession = errors.New("invalid stream session")
	ErrSessionOwner   = errors.New("stream session owner mismatch")
)

type SessionOwner struct {
	WorkspaceID string
	WindowID    uint64
}

type SessionRegistry struct {
	mu       sync.RWMutex
	sessions map[string]SessionOwner
}

func NewSessionRegistry() *SessionRegistry {
	return &SessionRegistry{sessions: make(map[string]SessionOwner)}
}

func (r *SessionRegistry) Issue(owner SessionOwner) (string, error) {
	if owner.WorkspaceID == "" {
		return "", ErrInvalidSession
	}

	for {
		bytes := make([]byte, 24)
		if _, err := rand.Read(bytes); err != nil {
			return "", err
		}
		token := base64.RawURLEncoding.EncodeToString(bytes)

		r.mu.Lock()
		if _, exists := r.sessions[token]; !exists {
			r.sessions[token] = owner
			r.mu.Unlock()
			return token, nil
		}
		r.mu.Unlock()
	}
}

func (r *SessionRegistry) Validate(token, workspaceID string, windowID uint64) error {
	if token == "" || workspaceID == "" {
		return ErrInvalidSession
	}

	r.mu.RLock()
	owner, ok := r.sessions[token]
	r.mu.RUnlock()
	if !ok || owner.WorkspaceID != workspaceID || owner.WindowID != windowID {
		return ErrSessionOwner
	}
	return nil
}

func (r *SessionRegistry) Revoke(token string) {
	r.mu.Lock()
	delete(r.sessions, token)
	r.mu.Unlock()
}
