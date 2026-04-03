// gateway.go - Smart Gateway Router
package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/panjf2000/gnet/v2"
)

// ============================================
// Configuration
// ============================================

const (
	GATEWAY_HTTP_PORT   = ":5000"      // Gateway listens here
	GATEWAY_BINARY_PORT = ":9090"      // Gateway binary protocol port
	GNET_HTTP_BACKEND   = "http://file_server:8081"  // gnet HTTP APIs
	GNET_BINARY_BACKEND = "file_server:8081"         // gnet binary protocol

	// Binary protocol commands (must match gnet server)
	CMD_UPLOAD_CHUNK = 0x01
	CMD_STREAM_RANGE = 0x02
	CMD_PING         = 0x03
)

// ============================================
// HTTP Gateway (Routes to gnet HTTP)
// ============================================

type HTTPGateway struct {
	gnetProxy  *httputil.ReverseProxy
}

func NewHTTPGateway() *HTTPGateway {
	gnetURL, _ := url.Parse(GNET_HTTP_BACKEND)

	return &HTTPGateway{
		gnetProxy:  httputil.NewSingleHostReverseProxy(gnetURL),
	}
}

// ============================================
// WebSocket Upgrader
// ============================================

var wsUpgrader = websocket.Upgrader{
	// NOTE: CheckOrigin allows all origins here because this gateway is an
	// internal service only reachable from the Docker network / localhost.
	// Restrict this to trusted origins if the gateway is ever exposed publicly.
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func (gw *HTTPGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Log request
	log.Printf("📥 HTTP %s %s from %s", r.Method, r.URL.Path, r.RemoteAddr)

	// Route WebSocket upload bridge before regular proxy routing
	if r.URL.Path == "/ws/upload" {
		gw.handleWSUpload(w, r)
		return
	}

	// Route based on path
	if isGnetHTTPRoute(r.URL.Path) {
		// Route to gnet HTTP server (streaming, internal APIs)
		log.Printf("→ Routing to gnet HTTP: %s", r.URL.Path)
		gw.gnetProxy.ServeHTTP(w, r)
	} else {
		http.NotFound(w, r)
	}
}

// handleWSUpload upgrades an HTTP connection to WebSocket and bridges it to the gnet TCP backend.
func (gw *HTTPGateway) handleWSUpload(w http.ResponseWriter, r *http.Request) {
	wsConn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("❌ WebSocket upgrade failed: %v", err)
		return
	}

	tcpConn, err := net.DialTimeout("tcp", GNET_BINARY_BACKEND, 5*time.Second)
	if err != nil {
		log.Printf("❌ Failed to connect to gnet backend: %v", err)
		wsConn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "backend unavailable"))
		wsConn.Close()
		return
	}

	log.Printf("🔗 WS bridge: %s → %s", wsConn.RemoteAddr(), GNET_BINARY_BACKEND)

	// Use sync.Once so each connection is closed exactly once regardless of
	// which goroutine encounters an error first.
	var (
		onceWS  sync.Once
		onceTCP sync.Once
	)
	closeWS  := func() { onceWS.Do(func() { wsConn.Close() }) }
	closeTCP := func() { onceTCP.Do(func() { tcpConn.Close() }) }

	// done is closed when the WS→TCP goroutine exits (browser closed or error).
	done := make(chan struct{})

	// WS → TCP: forward binary messages from the browser to the gnet server.
	go func() {
		defer close(done)
		defer closeTCP()
		for {
			msgType, data, err := wsConn.ReadMessage()
			if err != nil {
				if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
					log.Printf("❌ WS read error: %v", err)
				}
				return
			}
			if msgType == websocket.BinaryMessage {
				if _, err := tcpConn.Write(data); err != nil {
					log.Printf("❌ TCP write error: %v", err)
					return
				}
			}
		}
	}()

	// TCP → WS: forward raw bytes from the gnet server back to the browser.
	go func() {
		defer closeWS()
		buf := make([]byte, 64*1024)
		for {
			n, err := tcpConn.Read(buf)
			if err != nil {
				if err != io.EOF {
					log.Printf("❌ TCP read error: %v", err)
				}
				return
			}
			if n > 0 {
				if err := wsConn.WriteMessage(websocket.BinaryMessage, buf[:n]); err != nil {
					log.Printf("❌ WS write error: %v", err)
					return
				}
			}
		}
	}()

	<-done
	closeWS()
}

func isGnetHTTPRoute(path string) bool {
	// Routes that go to gnet HTTP server
	gnetRoutes := []string{
		"/stream/",           // Streaming endpoint
		"/internal/",         // Internal gnet APIs
		"/health",            // Health check (gnet)
	}

	for _, route := range gnetRoutes {
		if len(path) >= len(route) && path[:len(route)] == route {
			return true
		}
	}

	return false
}

// ============================================
// Binary Gateway (Routes binary traffic to gnet)
// ============================================

type BinaryGateway struct {
	gnet.BuiltinEventEngine

	gnetBackend  string
	connPool     map[gnet.Conn]net.Conn // Client conn -> Backend conn
	connPoolMu   sync.RWMutex
}

type ClientContext struct {
	backendConn net.Conn
	buffer      []byte
	mu          sync.Mutex
}

func (bg *BinaryGateway) OnBoot(eng gnet.Engine) (action gnet.Action) {
	log.Printf("🚀 Binary gateway started on port 9090")
	log.Printf("🔗 Backend: %s", bg.gnetBackend)
	return gnet.None
}

func (bg *BinaryGateway) OnOpen(c gnet.Conn) (out []byte, action gnet.Action) {
	log.Printf("✅ Binary client connected: %s", c.RemoteAddr())

	// Establish connection to gnet backend
	backendConn, err := net.DialTimeout("tcp", bg.gnetBackend, 5*time.Second)
	if err != nil {
		log.Printf("❌ Failed to connect to gnet backend: %v", err)
		return nil, gnet.Close
	}

	ctx := &ClientContext{
		backendConn: backendConn,
		buffer:      make([]byte, 0, 4096),
	}
	c.SetContext(ctx)

	// Start reading responses from backend
	go bg.readFromBackend(c, backendConn)

	return nil, gnet.None
}

func (bg *BinaryGateway) OnClose(c gnet.Conn, err error) (action gnet.Action) {
	ctx := c.Context().(*ClientContext)

	if ctx.backendConn != nil {
		ctx.backendConn.Close()
		log.Printf("👋 Closed backend connection for %s", c.RemoteAddr())
	}

	if err != nil {
		log.Printf("❌ Client disconnected with error: %v", err)
	} else {
		log.Printf("👋 Client disconnected: %s", c.RemoteAddr())
	}

	return gnet.None
}

func (bg *BinaryGateway) OnTraffic(c gnet.Conn) (action gnet.Action) {
	ctx := c.Context().(*ClientContext)

	// Read data from client
	data, err := c.Next(-1)
	if err != nil {
		log.Printf("❌ Error reading from client: %v", err)
		return gnet.Close
	}

	// Peek at command to log
	if len(data) > 0 {
		cmd := data[0]
		log.Printf("⚡ Forwarding command 0x%02x (%d bytes) to gnet backend", cmd, len(data))
	}

	// Forward to gnet backend
	ctx.mu.Lock()
	_, err = ctx.backendConn.Write(data)
	ctx.mu.Unlock()

	if err != nil {
		log.Printf("❌ Error writing to backend: %v", err)
		return gnet.Close
	}

	return gnet.None
}

func (bg *BinaryGateway) readFromBackend(clientConn gnet.Conn, backendConn net.Conn) {
	buffer := make([]byte, 64*1024) // 64KB buffer

	for {
		n, err := backendConn.Read(buffer)
		if err != nil {
			if err != io.EOF {
				log.Printf("❌ Error reading from backend: %v", err)
			}
			clientConn.Close()
			return
		}

		if n > 0 {
			// Forward response to client
			err = clientConn.AsyncWrite(buffer[:n], nil)
			if err != nil {
				log.Printf("❌ Error writing to client: %v", err)
				return
			}

			log.Printf("⬅️  Forwarded %d bytes from backend to client", n)
		}
	}
}

// ============================================
// Enhanced Binary Gateway with Protocol Detection
// ============================================

type SmartBinaryGateway struct {
	gnet.BuiltinEventEngine

	gnetBackend string
}

func (sbg *SmartBinaryGateway) OnBoot(eng gnet.Engine) (action gnet.Action) {
	log.Printf("🚀 Smart binary gateway started")
	return gnet.None
}

func (sbg *SmartBinaryGateway) OnOpen(c gnet.Conn) (out []byte, action gnet.Action) {
	log.Printf("✅ Client connected: %s", c.RemoteAddr())

	ctx := &ClientContext{
		buffer: make([]byte, 0, 4096),
	}
	c.SetContext(ctx)

	return nil, gnet.None
}

func (sbg *SmartBinaryGateway) OnTraffic(c gnet.Conn) (action gnet.Action) {
	ctx := c.Context().(*ClientContext)

	// Read data
	data, err := c.Next(-1)
	if err != nil {
		return gnet.Close
	}

	ctx.buffer = append(ctx.buffer, data...)

	// Lazy connection to backend
	if ctx.backendConn == nil {
		backendConn, err := net.DialTimeout("tcp", sbg.gnetBackend, 5*time.Second)
		if err != nil {
			log.Printf("❌ Failed to connect to backend: %v", err)
			return gnet.Close
		}

		ctx.backendConn = backendConn

		// Start reading from backend
		go sbg.readFromBackend(c, backendConn)
	}

	// Forward buffered data to backend
	if len(ctx.buffer) > 0 {
		ctx.mu.Lock()
		_, err = ctx.backendConn.Write(ctx.buffer)
		ctx.mu.Unlock()

		if err != nil {
			log.Printf("❌ Error forwarding to backend: %v", err)
			return gnet.Close
		}

		// Log command
		if ctx.buffer[0] == CMD_UPLOAD_CHUNK {
			log.Printf("⚡ Upload chunk forwarded (%d bytes)", len(ctx.buffer))
		}

		ctx.buffer = ctx.buffer[:0]
	}

	return gnet.None
}

func (sbg *SmartBinaryGateway) OnClose(c gnet.Conn, err error) (action gnet.Action) {
	ctx := c.Context().(*ClientContext)

	if ctx.backendConn != nil {
		ctx.backendConn.Close()
	}

	return gnet.None
}

func (sbg *SmartBinaryGateway) readFromBackend(clientConn gnet.Conn, backendConn net.Conn) {
	buffer := make([]byte, 64*1024)

	for {
		n, err := backendConn.Read(buffer)
		if err != nil {
			if err != io.EOF {
				log.Printf("❌ Backend read error: %v", err)
			}
			clientConn.Close()
			return
		}

		if n > 0 {
			clientConn.AsyncWrite(buffer[:n], nil)
		}
	}
}

// ============================================
// Protocol Auto-Detection Gateway (ADVANCED)
// ============================================

type UnifiedGateway struct {
	gnet.BuiltinEventEngine

	gnetBackend  string
}

func (ug *UnifiedGateway) OnBoot(eng gnet.Engine) (action gnet.Action) {
	log.Printf("🚀 Unified gateway started (auto-detect protocol)")
	return gnet.None
}

func (ug *UnifiedGateway) OnOpen(c gnet.Conn) (out []byte, action gnet.Action) {
	ctx := &ClientContext{
		buffer: make([]byte, 0, 4096),
	}
	c.SetContext(ctx)
	return nil, gnet.None
}

func (ug *UnifiedGateway) OnTraffic(c gnet.Conn) (action gnet.Action) {
	ctx := c.Context().(*ClientContext)

	// Read data
	data, err := c.Next(-1)
	if err != nil {
		return gnet.Close
	}

	ctx.buffer = append(ctx.buffer, data...)

	// Detect protocol on first packet
	if ctx.backendConn == nil && len(ctx.buffer) >= 4 {
		backend := ug.gnetBackend
		log.Printf("🔍 Routing to gnet backend")

		// Connect to gnet backend
		backendConn, err := net.DialTimeout("tcp", backend, 5*time.Second)
		if err != nil {
			log.Printf("❌ Backend connection failed: %v", err)
			return gnet.Close
		}

		ctx.backendConn = backendConn

		// Start reading from backend
		go ug.readFromBackend(c, backendConn)
	}

	// Forward data to backend
	if ctx.backendConn != nil && len(ctx.buffer) > 0 {
		ctx.mu.Lock()
		_, err = ctx.backendConn.Write(ctx.buffer)
		ctx.mu.Unlock()

		if err != nil {
			log.Printf("❌ Forward error: %v", err)
			return gnet.Close
		}

		ctx.buffer = ctx.buffer[:0]
	}

	return gnet.None
}

func (ug *UnifiedGateway) OnClose(c gnet.Conn, err error) (action gnet.Action) {
	ctx := c.Context().(*ClientContext)

	if ctx.backendConn != nil {
		ctx.backendConn.Close()
	}

	return gnet.None
}

func (ug *UnifiedGateway) readFromBackend(clientConn gnet.Conn, backendConn net.Conn) {
	buffer := make([]byte, 64*1024)

	for {
		n, err := backendConn.Read(buffer)
		if err != nil {
			clientConn.Close()
			return
		}

		if n > 0 {
			clientConn.AsyncWrite(buffer[:n], nil)
		}
	}
}

// ============================================
// Main - Choose Your Gateway Mode
// ============================================

func main() {
	// Mode 1: Separate HTTP and Binary gateways
	mode := "separate" // Options: "separate", "unified"

	switch mode {
	case "separate":
		runSeparateGateways()
	case "unified":
		runUnifiedGateway()
	default:
		log.Fatal("Invalid mode")
	}
}

// ============================================
// Mode 1: Separate HTTP and Binary Gateways
// ============================================

func runSeparateGateways() {
	log.Printf("🚀 Starting SEPARATE gateways mode")
	log.Printf("📡 HTTP Gateway: %s → gnet(%s)",
		GATEWAY_HTTP_PORT, GNET_HTTP_BACKEND)
	log.Printf("⚡ Binary Gateway: %s → gnet(%s)",
		GATEWAY_BINARY_PORT, GNET_BINARY_BACKEND)

	// Start HTTP gateway
	go func() {
		httpGateway := NewHTTPGateway()
		log.Printf("🌐 HTTP Gateway listening on %s", GATEWAY_HTTP_PORT)
		log.Fatal(http.ListenAndServe(GATEWAY_HTTP_PORT, httpGateway))
	}()

	// Start Binary gateway
	binaryGateway := &BinaryGateway{
		gnetBackend: GNET_BINARY_BACKEND,
		connPool:    make(map[gnet.Conn]net.Conn),
	}

	log.Fatal(gnet.Run(binaryGateway, fmt.Sprintf("tcp://%s", GATEWAY_BINARY_PORT),
		gnet.WithMulticore(true),
		gnet.WithEdgeTriggeredIO(true),
		gnet.WithReusePort(true)))
}

// ============================================
// Mode 2: Unified Gateway (Auto-detect)
// ============================================

func runUnifiedGateway() {
	log.Printf("🚀 Starting UNIFIED gateway mode (auto-detect)")
	log.Printf("📡 Listening on %s", GATEWAY_HTTP_PORT)

	// This gateway auto-detects HTTP vs Binary protocol
	unifiedGateway := &UnifiedGateway{
		gnetBackend:  GNET_BINARY_BACKEND,
	}

	log.Fatal(gnet.Run(unifiedGateway, fmt.Sprintf("tcp://%s", GATEWAY_HTTP_PORT),
		gnet.WithMulticore(true),
		gnet.WithEdgeTriggeredIO(true),
		gnet.WithReusePort(true)))
}
