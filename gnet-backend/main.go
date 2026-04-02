// gnet_file_server.go - Advanced file upload server with auth, retry, pause/resume
package main

import (
	"fmt"
	"log"

	"github.com/panjf2000/gnet/v2"
)

// ============================================
// Main
// ============================================

func main() {
	log.Printf("🚀 Starting advanced file upload server")
	log.Printf("📁 S3 path format: user_id/timestamp/filename")
	log.Printf("📄 Supported: MP4, PDF, Images (up to 10GB)")

	// Initialize S3 client
	s3Client, err := NewS3Client()
	if err != nil {
		log.Fatalf("❌ Failed to initialize S3: %v", err)
	}
	log.Printf("✅ S3 client initialized")

	// Initialize auth manager
	authMgr := NewAuthManager()

	// Create session manager
	sessionMgr := NewSessionManager(s3Client, authMgr)

	// Start gnet server
	fileServer := &FileUploadServer{
		sessionMgr: sessionMgr,
		s3Client:   s3Client,
		authMgr:    authMgr,
	}

	// FIX: Remove WithEdgeTriggeredIO as it might not be available in your gnet version
	log.Fatal(gnet.Run(fileServer, fmt.Sprintf("tcp://%s", GNET_PORT),
		gnet.WithMulticore(true),
		gnet.WithReusePort(true),
		gnet.WithReadBufferCap(64*1024*1024), // 64MB read buffer for large chunks
		gnet.WithWriteBufferCap(4*1024*1024), // 4MB write buffer
	))
}
