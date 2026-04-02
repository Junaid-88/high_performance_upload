package main

import (
	"log"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// ============================================
// Upload Session
// ============================================

type ChunkInfo struct {
	Index      uint32
	Size       uint32
	Hash       string
	UploadedAt time.Time
	PartNumber int32
	ETag       string
}

type UploadSession struct {
	SessionID      string
	UserID         string
	Username       string
	FileName       string
	S3Key          string // user_id/timestamp/filename
	FileExtension  string
	ContentType    string
	TotalChunks    uint32
	ChunkSize      uint32
	TotalSize      uint64
	State          string
	ReceivedChunks map[uint32]*ChunkInfo
	UploadID       string
	CompletedParts []types.CompletedPart
	CreatedAt      time.Time
	UpdatedAt      time.Time
	PausedAt       *time.Time
	mu             sync.Mutex
}

func (us *UploadSession) AddChunk(index uint32, size uint32, hash string, partNumber int32, etag string) bool {
	us.mu.Lock()
	defer us.mu.Unlock()

	// Check if chunk already exists (duplicate)
	if existing, exists := us.ReceivedChunks[index]; exists {
		log.Printf("⚠️  Duplicate chunk detected: session=%s, chunk=%d (hash: %s)", us.SessionID, index, hash)
		// Verify hash matches
		if existing.Hash == hash {
			return true // Same chunk, skip (idempotent)
		}
		log.Printf("❌ Hash mismatch for chunk %d: expected %s, got %s", index, existing.Hash, hash)
		return false
	}

	// Add new chunk
	us.ReceivedChunks[index] = &ChunkInfo{
		Index:      index,
		Size:       size,
		Hash:       hash,
		UploadedAt: time.Now(),
		PartNumber: partNumber,
		ETag:       etag,
	}

	us.CompletedParts = append(us.CompletedParts, types.CompletedPart{
		PartNumber: aws.Int32(partNumber),
		ETag:       aws.String(etag),
	})

	us.State = STATE_UPLOADING
	us.UpdatedAt = time.Now()
	return false // Not duplicate
}

func (us *UploadSession) GetProgress() (received, total uint32) {
	us.mu.Lock()
	defer us.mu.Unlock()
	return uint32(len(us.ReceivedChunks)), us.TotalChunks
}

func (us *UploadSession) IsComplete() bool {
	us.mu.Lock()
	defer us.mu.Unlock()
	return len(us.ReceivedChunks) == int(us.TotalChunks)
}

func (us *UploadSession) GetMissingChunks() []uint32 {
	us.mu.Lock()
	defer us.mu.Unlock()

	missing := make([]uint32, 0)
	for i := uint32(0); i < us.TotalChunks; i++ {
		if _, exists := us.ReceivedChunks[i]; !exists {
			missing = append(missing, i)
		}
	}
	return missing
}

func (us *UploadSession) Pause() {
	us.mu.Lock()
	defer us.mu.Unlock()
	now := time.Now()
	us.State = STATE_PAUSED
	us.PausedAt = &now
	us.UpdatedAt = now
}

func (us *UploadSession) Resume() {
	us.mu.Lock()
	defer us.mu.Unlock()
	us.State = STATE_UPLOADING
	us.PausedAt = nil
	us.UpdatedAt = time.Now()
}

func (us *UploadSession) Cancel() {
	us.mu.Lock()
	defer us.mu.Unlock()
	us.State = STATE_CANCELLED
	us.UpdatedAt = time.Now()
}
