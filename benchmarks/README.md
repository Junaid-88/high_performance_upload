# Upload Benchmark Suite

This benchmark suite compares two upload approaches provided by this repository:

| Approach | Protocol | Server | Port |
|---|---|---|---|
| **gnet binary** | Custom binary TCP | `gnet-backend` | 8081 |
| **Flask HTTP** | HTTP multipart/form-data | `flask_bench_server` | 5002 |

Both servers write uploaded files to the same MinIO/S3 backend so the storage
overhead is identical and the results reflect only the upload protocol itself.

---

## What the benchmarks measure

- **Throughput** – megabytes per second from the client's perspective
- **Total upload time** – wall-clock time from first byte sent to completion
- **Per-chunk latency** – average time per individual chunk (gnet only)
- **Connection setup time** – time to open a TCP connection or HTTP session

Each benchmark runs a **warm-up phase** (2 uploads) before collecting results.
Results are printed as a formatted table and, when the comparison script is
used, shown side-by-side with a percentage-improvement column.

---

## Prerequisites

| Tool | Minimum version | Why |
|---|---|---|
| Docker & docker-compose | 20.x / v2 | Run MinIO + servers |
| Go | 1.21+ | Run gnet benchmark |
| Python 3 | 3.9+ | Run Flask benchmark |

Install Python dependencies:

```bash
pip install -r benchmarks/requirements.txt
```

---

## Quick start (all-in-one)

Run from the **project root**:

```bash
bash benchmarks/benchmark_compare.sh
```

The script will:

1. Check that Docker, Go, and Python 3 are available
2. Start `docker-compose` services
3. Wait for MinIO, gnet-backend, and flask_bench_server to become healthy
4. Run `benchmark_gnet.go`
5. Run `benchmark_flask.py`
6. Print a side-by-side comparison table
7. Optionally stop the containers

---

## Running benchmarks individually

### gnet binary protocol benchmark

```bash
# From the project root
go run benchmarks/benchmark_gnet.go
```

Optional flags:

| Flag | Default | Description |
|---|---|---|
| `-addr` | `localhost:8081` | gnet-backend TCP address |
| `-sizes` | `1,10,50,100` | Comma-separated file sizes in MB |
| `-concurrency` | `1,5,10,25` | Comma-separated concurrency levels |
| `-chunk` | `5` | Chunk size in MB |

Example – quick run with small files only:

```bash
go run benchmarks/benchmark_gnet.go -sizes 1,10 -concurrency 1,5
```

### Flask HTTP benchmark

```bash
# From the project root
python3 benchmarks/benchmark_flask.py
```

Optional flags:

| Flag | Default | Description |
|---|---|---|
| `--addr` | `http://localhost:5002` | flask_bench_server base URL |
| `--sizes` | `1,10,50,100` | Comma-separated file sizes in MB |
| `--concurrency` | `1,5,10,25` | Comma-separated concurrency levels |

Example:

```bash
python3 benchmarks/benchmark_flask.py --sizes 1,10 --concurrency 1,5
```

---

## How to interpret results

### Throughput (MB/s)

Higher is better.  The gnet binary protocol avoids HTTP framing overhead and
keeps a persistent TCP connection, so it should outperform HTTP multipart
uploads, especially at high concurrency.

### Total upload time (s)

Lower is better.

### Per-chunk latency (ms) – gnet only

Lower is better.  Each chunk is acknowledged individually; a lower latency
means the server and S3 are handling chunks quickly.

### Percentage improvement

Shown in the comparison table as `gnet improvement %`.  A positive value means
gnet was faster.  A negative value (unlikely for throughput) would indicate
Flask was faster for that particular combination.

---

## Standalone flask upload server

`flask_upload_server.py` is a self-contained Flask application that:

- Accepts `POST /upload` with a `file` field (multipart/form-data)
- Streams the upload directly to MinIO using boto3
- Exposes `GET /health` for readiness checks

You can start it manually:

```bash
python3 benchmarks/flask_upload_server.py
```

It reads the following environment variables (with the defaults shown):

| Variable | Default |
|---|---|
| `S3_ENDPOINT` | `http://localhost:9000` |
| `S3_ACCESS_KEY` | `admin` |
| `S3_SECRET_KEY` | `strongpassword` |
| `S3_BUCKET` | `uploads` |
| `S3_REGION` | `us-east-1` |
| `FLASK_PORT` | `5002` |
