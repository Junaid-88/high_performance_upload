// benchmark_gnet.go – benchmark the gnet binary-protocol upload path.
//
// Usage (from the project root):
//
//	go run benchmarks/benchmark_gnet.go [flags]
//
// Flags:
//
//	-addr        TCP address of the gnet-backend  (default: localhost:8081)
//	-sizes       Comma-separated file sizes in MB (default: 1,10,50,100)
//	-concurrency Comma-separated concurrency values (default: 1,5,10,25)
//	-chunk       Chunk size in MB                 (default: 5)
//
// Protocol frame format (BigEndian):
//
//	[auth_token_size: 4B][auth_token][payload_size: 4B][cmd: 1B][cmd_data]
//
// CMD_INIT_UPLOAD  (0x01) cmd_data: filename_size(2)|filename|total_chunks(4)|chunk_size(4)
// CMD_UPLOAD_CHUNK (0x02) cmd_data: session_id_size(2)|session_id|chunk_index(4)|chunk_size(4)|chunk_data
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ── protocol constants (mirror config.go) ────────────────────────────────────

const (
	cmdInitUpload  byte = 0x01
	cmdUploadChunk byte = 0x02

	respReady    byte = 0x12
	respChunkAck byte = 0x13
	respComplete byte = 0x14
	respError    byte = 0x11
	respDup      byte = 0x1A

	authToken = "test_token_user123"
)

// ── frame helpers ─────────────────────────────────────────────────────────────

// buildFrame wraps a payload with the standard wire frame.
func buildFrame(payload []byte) []byte {
	tok := []byte(authToken)
	frame := make([]byte, 4+len(tok)+4+len(payload))
	binary.BigEndian.PutUint32(frame[0:4], uint32(len(tok)))
	copy(frame[4:], tok)
	binary.BigEndian.PutUint32(frame[4+len(tok):], uint32(len(payload)))
	copy(frame[4+len(tok)+4:], payload)
	return frame
}

// buildInitPayload constructs the CMD_INIT_UPLOAD payload.
func buildInitPayload(filename string, totalChunks, chunkSize uint32) []byte {
	fn := []byte(filename)
	p := make([]byte, 1+2+len(fn)+4+4)
	p[0] = cmdInitUpload
	binary.BigEndian.PutUint16(p[1:3], uint16(len(fn)))
	copy(p[3:], fn)
	off := 3 + len(fn)
	binary.BigEndian.PutUint32(p[off:], totalChunks)
	binary.BigEndian.PutUint32(p[off+4:], chunkSize)
	return p
}

// buildChunkPayload constructs the CMD_UPLOAD_CHUNK payload.
func buildChunkPayload(sessionID string, chunkIndex uint32, data []byte) []byte {
	sid := []byte(sessionID)
	p := make([]byte, 1+2+len(sid)+4+4+len(data))
	p[0] = cmdUploadChunk
	binary.BigEndian.PutUint16(p[1:3], uint16(len(sid)))
	copy(p[3:], sid)
	off := 3 + len(sid)
	binary.BigEndian.PutUint32(p[off:], chunkIndex)
	binary.BigEndian.PutUint32(p[off+4:], uint32(len(data)))
	copy(p[off+8:], data)
	return p
}

// ── response reading ──────────────────────────────────────────────────────────

// readFull reads exactly n bytes from r.
func readFull(r io.Reader, n int) ([]byte, error) {
	buf := make([]byte, n)
	_, err := io.ReadFull(r, buf)
	return buf, err
}

// readSessionID parses a RESP_READY response and returns the session ID.
// Response layout: respCode(1) | session_id_size(2) | session_id | s3_key_size(2) | s3_key
func readSessionID(conn net.Conn) (string, error) {
	code, err := readFull(conn, 1)
	if err != nil {
		return "", fmt.Errorf("reading response code: %w", err)
	}
	switch code[0] {
	case respError:
		lenBuf, err := readFull(conn, 1)
		if err != nil {
			return "", fmt.Errorf("reading error length: %w", err)
		}
		msg, err := readFull(conn, int(lenBuf[0]))
		if err != nil {
			return "", fmt.Errorf("reading error message: %w", err)
		}
		return "", fmt.Errorf("server error: %s", string(msg))
	case respReady:
		// ok – continue
	default:
		return "", fmt.Errorf("unexpected response code: 0x%02x", code[0])
	}

	sizeBuf, err := readFull(conn, 2)
	if err != nil {
		return "", fmt.Errorf("reading session_id size: %w", err)
	}
	sidLen := int(binary.BigEndian.Uint16(sizeBuf))
	sidBuf, err := readFull(conn, sidLen)
	if err != nil {
		return "", fmt.Errorf("reading session_id: %w", err)
	}

	// Drain s3_key
	s3SizeBuf, err := readFull(conn, 2)
	if err != nil {
		return "", fmt.Errorf("reading s3_key size: %w", err)
	}
	s3Len := int(binary.BigEndian.Uint16(s3SizeBuf))
	if _, err = readFull(conn, s3Len); err != nil {
		return "", fmt.Errorf("reading s3_key: %w", err)
	}

	return string(sidBuf), nil
}

// readChunkAck reads a RESP_CHUNK_ACK / RESP_DUP / RESP_COMPLETE / RESP_ERROR.
// Returns (isComplete, err).
func readChunkAck(conn net.Conn) (bool, error) {
	code, err := readFull(conn, 1)
	if err != nil {
		return false, fmt.Errorf("reading ack code: %w", err)
	}
	switch code[0] {
	case respChunkAck:
		// received(4) | total(4) | progress(4)
		if _, err = readFull(conn, 12); err != nil {
			return false, fmt.Errorf("reading chunk_ack payload: %w", err)
		}
		return false, nil
	case respDup:
		// chunk_index(4) | progress(4)
		if _, err = readFull(conn, 8); err != nil {
			return false, fmt.Errorf("reading dup payload: %w", err)
		}
		return false, nil
	case respComplete:
		// s3_key_size(2) | s3_key | file_size(8)
		s3SizeBuf, err := readFull(conn, 2)
		if err != nil {
			return false, fmt.Errorf("reading complete s3_key size: %w", err)
		}
		s3Len := int(binary.BigEndian.Uint16(s3SizeBuf))
		if _, err = readFull(conn, s3Len+8); err != nil {
			return false, fmt.Errorf("reading complete s3_key + file_size: %w", err)
		}
		return true, nil
	case respError:
		lenBuf, err := readFull(conn, 1)
		if err != nil {
			return false, fmt.Errorf("reading error length: %w", err)
		}
		msg, err := readFull(conn, int(lenBuf[0]))
		if err != nil {
			return false, fmt.Errorf("reading error message: %w", err)
		}
		return false, fmt.Errorf("server error: %s", string(msg))
	default:
		return false, fmt.Errorf("unexpected ack code: 0x%02x", code[0])
	}
}

// ── single upload ─────────────────────────────────────────────────────────────

type uploadResult struct {
	totalTime   time.Duration
	connectTime time.Duration
	chunkTimes  []time.Duration
	bytesTotal  int64
	err         error
}

func runUpload(addr string, data []byte, chunkSizeBytes int, idx int) uploadResult {
	res := uploadResult{bytesTotal: int64(len(data))}
	fileSizeBytes := len(data)

	totalChunks := (fileSizeBytes + chunkSizeBytes - 1) / chunkSizeBytes
	filename := fmt.Sprintf("bench_%d_%d.mp4", idx, time.Now().UnixNano())

	// Connect
	connStart := time.Now()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		res.err = fmt.Errorf("connect: %w", err)
		return res
	}
	defer conn.Close()
	res.connectTime = time.Since(connStart)

	uploadStart := time.Now()

	// INIT_UPLOAD
	initPayload := buildInitPayload(filename, uint32(totalChunks), uint32(chunkSizeBytes))
	if _, err = conn.Write(buildFrame(initPayload)); err != nil {
		res.err = fmt.Errorf("write INIT: %w", err)
		return res
	}

	sessionID, err := readSessionID(conn)
	if err != nil {
		res.err = fmt.Errorf("read INIT resp: %w", err)
		return res
	}

	// UPLOAD_CHUNK loop
	for i := 0; i < totalChunks; i++ {
		start := i * chunkSizeBytes
		end := start + chunkSizeBytes
		if end > fileSizeBytes {
			end = fileSizeBytes
		}
		chunk := data[start:end]

		chunkStart := time.Now()
		chunkPayload := buildChunkPayload(sessionID, uint32(i), chunk)
		if _, err = conn.Write(buildFrame(chunkPayload)); err != nil {
			res.err = fmt.Errorf("write chunk %d: %w", i, err)
			return res
		}

		complete, err := readChunkAck(conn)
		if err != nil {
			res.err = fmt.Errorf("read chunk %d ack: %w", i, err)
			return res
		}
		res.chunkTimes = append(res.chunkTimes, time.Since(chunkStart))

		if complete {
			break
		}
	}

	res.totalTime = time.Since(uploadStart)
	return res
}

// ── benchmark harness ─────────────────────────────────────────────────────────

type benchResult struct {
	fileSizeMB  int
	concurrency int
	throughput  float64 // MB/s
	avgTime     float64 // seconds
	avgChunkMs  float64 // ms
	avgConnMs   float64 // ms
	errors      int
}

func runBenchmark(addr string, fileSizeMB, concurrency, chunkSizeMB, warmup int) benchResult {
	fileSizeBytes := fileSizeMB * 1024 * 1024
	chunkSizeBytes := chunkSizeMB * 1024 * 1024

	// Generate random data once and reuse it across all uploads in this run.
	data := make([]byte, fileSizeBytes)
	if _, err := rand.Read(data); err != nil { //nolint:gosec // benchmark only, not crypto use
		return benchResult{fileSizeMB: fileSizeMB, concurrency: concurrency, errors: concurrency}
	}

	// Warm-up
	for i := 0; i < warmup; i++ {
		_ = runUpload(addr, data, chunkSizeBytes, -(i + 1))
	}

	var (
		mu          sync.Mutex
		wg          sync.WaitGroup
		totalTime   time.Duration
		totalConn   time.Duration
		totalChunk  time.Duration
		chunkCount  int
		errCount    int
		uploadCount int
	)

	for c := 0; c < concurrency; c++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			r := runUpload(addr, data, chunkSizeBytes, id)
			mu.Lock()
			defer mu.Unlock()
			if r.err != nil {
				errCount++
				return
			}
			totalTime += r.totalTime
			totalConn += r.connectTime
			for _, ct := range r.chunkTimes {
				totalChunk += ct
				chunkCount++
			}
			uploadCount++
		}(c)
	}
	wg.Wait()

	if uploadCount == 0 {
		return benchResult{fileSizeMB: fileSizeMB, concurrency: concurrency, errors: errCount}
	}

	avgTimeSec := totalTime.Seconds() / float64(uploadCount)
	throughput := float64(fileSizeMB) / avgTimeSec
	avgChunkMs := 0.0
	if chunkCount > 0 {
		avgChunkMs = totalChunk.Seconds() * 1000.0 / float64(chunkCount)
	}
	avgConnMs := totalConn.Seconds() * 1000.0 / float64(uploadCount)

	return benchResult{
		fileSizeMB:  fileSizeMB,
		concurrency: concurrency,
		throughput:  throughput,
		avgTime:     avgTimeSec,
		avgChunkMs:  avgChunkMs,
		avgConnMs:   avgConnMs,
		errors:      errCount,
	}
}

// ── output ────────────────────────────────────────────────────────────────────

func printTable(results []benchResult) {
	sep := strings.Repeat("-", 80)
	fmt.Println(sep)
	fmt.Printf("%-10s %-12s %-14s %-14s %-14s %-8s\n",
		"Size(MB)", "Concurrency", "Throughput", "Avg Time", "Chunk Lat", "Errors")
	fmt.Printf("%-10s %-12s %-14s %-14s %-14s %-8s\n",
		"", "", "(MB/s)", "(s)", "(ms)", "")
	fmt.Println(sep)
	for _, r := range results {
		fmt.Printf("%-10d %-12d %-14.2f %-14.3f %-14.2f %-8d\n",
			r.fileSizeMB, r.concurrency, r.throughput, r.avgTime, r.avgChunkMs, r.errors)
	}
	fmt.Println(sep)
}

// ── main ──────────────────────────────────────────────────────────────────────

func main() {
	addr := flag.String("addr", "localhost:8081", "gnet-backend TCP address")
	sizesFlag := flag.String("sizes", "1,10,50,100", "comma-separated file sizes in MB")
	concurrencyFlag := flag.String("concurrency", "1,5,10,25", "comma-separated concurrency levels")
	chunkMB := flag.Int("chunk", 5, "chunk size in MB")
	flag.Parse()

	sizes := parseInts(*sizesFlag)
	concurrencies := parseInts(*concurrencyFlag)

	fmt.Printf("\n=== gnet Binary Protocol Benchmark ===\n")
	fmt.Printf("Server : %s\n", *addr)
	fmt.Printf("Chunk  : %d MB\n", *chunkMB)
	fmt.Printf("Sizes  : %v MB\n", sizes)
	fmt.Printf("Conc.  : %v\n\n", concurrencies)

	var results []benchResult
	for _, size := range sizes {
		for _, conc := range concurrencies {
			fmt.Printf("  running: %d MB × concurrency %d …", size, conc)
			r := runBenchmark(*addr, size, conc, *chunkMB, 2)
			results = append(results, r)
			fmt.Printf(" %.2f MB/s\n", r.throughput)
		}
	}

	fmt.Println()
	printTable(results)

	// Machine-readable summary for benchmark_compare.sh
	fmt.Println("\n--- gnet_csv_start ---")
	fmt.Println("size_mb,concurrency,throughput_mbps,avg_time_s,chunk_lat_ms,errors")
	for _, r := range results {
		fmt.Printf("%d,%d,%.4f,%.4f,%.4f,%d\n",
			r.fileSizeMB, r.concurrency, r.throughput, r.avgTime, r.avgChunkMs, r.errors)
	}
	fmt.Println("--- gnet_csv_end ---")
}

func parseInts(s string) []int {
	parts := strings.Split(s, ",")
	var out []int
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if n, err := strconv.Atoi(p); err == nil {
			out = append(out, n)
		}
	}
	return out
}
