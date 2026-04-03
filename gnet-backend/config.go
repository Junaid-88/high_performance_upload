package main

import "time"

// ============================================
// Configuration
// ============================================

const (
	GNET_PORT = ":8081"

	S3_ENDPOINT   = "http://minio:9000"
	S3_REGION     = "us-east-1"
	S3_ACCESS_KEY = "admin"
	S3_SECRET_KEY = "strongpassword"
	S3_BUCKET     = "uploads"

	// Protocol structure: size_of_auth_token|auth_token|size_of_payload|payload
	// Header: auth_token_size(4 bytes) | auth_token | payload_size(4 bytes) | command(1 byte) | payload

	// Protocol commands
	CMD_INIT_UPLOAD   = 0x01 // Initialize upload session
	CMD_UPLOAD_CHUNK  = 0x02 // Upload a chunk
	CMD_PAUSE_UPLOAD  = 0x03 // Pause upload
	CMD_RESUME_UPLOAD = 0x04 // Resume upload
	CMD_CANCEL_UPLOAD = 0x05 // Cancel upload
	CMD_GET_STATUS    = 0x06 // Get upload status

	// Response codes
	RESP_OK          = 0x10 // Success
	RESP_ERROR       = 0x11 // Error
	RESP_READY       = 0x12 // Session ready
	RESP_CHUNK_ACK   = 0x13 // Chunk acknowledged
	RESP_COMPLETE    = 0x14 // Upload complete
	RESP_STATUS      = 0x15 // Status response
	RESP_PAUSED      = 0x16 // Upload paused
	RESP_RESUMED     = 0x17 // Upload resumed
	RESP_CANCELLED   = 0x18 // Upload cancelled
	RESP_AUTH_FAILED = 0x19 // Authentication failed
	RESP_DUPLICATE   = 0x1A // Duplicate chunk (already received)

	// Session states
	STATE_INITIALIZED = "initialized"
	STATE_UPLOADING   = "uploading"
	STATE_PAUSED      = "paused"
	STATE_COMPLETED   = "completed"
	STATE_CANCELLED   = "cancelled"
	STATE_FAILED      = "failed"

	// File constraints
	MAX_FILE_SIZE  = 10 * 1024 * 1024 * 1024 // 10 GB
	MIN_CHUNK_SIZE = 5 * 1024 * 1024         // 5 MB (S3 minimum for multipart)
	MAX_CHUNK_SIZE = 100 * 1024 * 1024       // 100 MB

	// Timeouts
	SESSION_TIMEOUT = 2 * time.Hour
)

// Supported file types
var SUPPORTED_EXTENSIONS = map[string]string{
	".mp4":  "video/mp4",
	".pdf":  "application/pdf",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".gif":  "image/gif",
	".webp": "image/webp",
	".mov":  "video/quicktime",
	".avi":  "video/x-msvideo",
	".mkv":  "video/x-matroska",
}
