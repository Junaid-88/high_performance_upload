package main

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// ============================================
// Session Manager
// ============================================

type SessionManager struct {
	sessions map[string]*UploadSession
	mu       sync.RWMutex
	s3Client *S3Client
	authMgr  *AuthManager
}

func NewSessionManager(s3Client *S3Client, authMgr *AuthManager) *SessionManager {
	sm := &SessionManager{
		sessions: make(map[string]*UploadSession),
		s3Client: s3Client,
		authMgr:  authMgr,
	}

	go sm.cleanupLoop()

	return sm
}

func (sm *SessionManager) CreateSession(userID, username, fileName string, totalChunks, chunkSize uint32) (*UploadSession, error) {
	// Validate file extension
	ext := strings.ToLower(filepath.Ext(fileName))
	contentType, supported := SUPPORTED_EXTENSIONS[ext]
	if !supported {
		return nil, fmt.Errorf("unsupported file type: %s (supported: mp4, pdf, jpg, png, gif, webp, mov, avi, mkv)", ext)
	}

	// Validate file size
	totalSize := uint64(totalChunks) * uint64(chunkSize)
	if totalSize > MAX_FILE_SIZE {
		return nil, fmt.Errorf("file size exceeds maximum: %d bytes (max: %d)", totalSize, MAX_FILE_SIZE)
	}

	// Validate chunk size
	if chunkSize < MIN_CHUNK_SIZE {
		return nil, fmt.Errorf("chunk size too small: %d bytes (min: %d)", chunkSize, MIN_CHUNK_SIZE)
	}
	if chunkSize > MAX_CHUNK_SIZE {
		return nil, fmt.Errorf("chunk size too large: %d bytes (max: %d)", chunkSize, MAX_CHUNK_SIZE)
	}

	// Generate S3 key: user_id/timestamp/filename
	timestamp := time.Now().Format("20060102_150405")
	s3Key := fmt.Sprintf("%s/%s/%s", userID, timestamp, fileName)

	// Generate session ID
	sessionID := fmt.Sprintf("%s_%d", userID, time.Now().UnixNano())

	sm.mu.Lock()
	defer sm.mu.Unlock()

	session := &UploadSession{
		SessionID:      sessionID,
		UserID:         userID,
		Username:       username,
		FileName:       fileName,
		S3Key:          s3Key,
		FileExtension:  ext,
		ContentType:    contentType,
		TotalChunks:    totalChunks,
		ChunkSize:      chunkSize,
		TotalSize:      totalSize,
		State:          STATE_INITIALIZED,
		ReceivedChunks: make(map[uint32]*ChunkInfo),
		CompletedParts: make([]types.CompletedPart, 0),
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	sm.sessions[sessionID] = session
	log.Printf("📦 Created session: %s (user: %s, file: %s, size: %.2f MB, chunks: %d, s3: %s)",
		sessionID, username, fileName, float64(totalSize)/(1024*1024), totalChunks, s3Key)

	return session, nil
}

func (sm *SessionManager) GetSession(sessionID string) *UploadSession {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.sessions[sessionID]
}

func (sm *SessionManager) DeleteSession(sessionID string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	delete(sm.sessions, sessionID)
}

func (sm *SessionManager) cleanupLoop() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		sm.mu.Lock()
		now := time.Now()
		for id, session := range sm.sessions {
			shouldCleanup := false

			switch session.State {
			case STATE_COMPLETED, STATE_CANCELLED:
				// Clean up finished sessions after 1 hour
				if now.Sub(session.UpdatedAt) > 1*time.Hour {
					shouldCleanup = true
				}
			case STATE_PAUSED:
				// Clean up paused sessions after SESSION_TIMEOUT
				if now.Sub(session.UpdatedAt) > SESSION_TIMEOUT {
					shouldCleanup = true
				}
			default:
				// Clean up stale active sessions
				if now.Sub(session.UpdatedAt) > SESSION_TIMEOUT {
					shouldCleanup = true
				}
			}

			if shouldCleanup {
				log.Printf("🧹 Cleaning up session: %s (state: %s, age: %v)", id, session.State, now.Sub(session.CreatedAt))

				// Abort S3 multipart upload if not completed
				if session.UploadID != "" && session.State != STATE_COMPLETED {
					_, err := sm.s3Client.client.AbortMultipartUpload(context.Background(), &s3.AbortMultipartUploadInput{
						Bucket:   aws.String(sm.s3Client.bucket),
						Key:      aws.String(session.S3Key),
						UploadId: aws.String(session.UploadID),
					})
					if err != nil {
						log.Printf("⚠️  Failed to abort multipart upload for session %s: %v", id, err)
					}
				}

				delete(sm.sessions, id)
			}
		}
		sm.mu.Unlock()
	}
}
