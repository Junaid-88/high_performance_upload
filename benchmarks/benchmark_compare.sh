#!/usr/bin/env bash
# benchmark_compare.sh – run both benchmarks and show a side-by-side comparison.
#
# Usage (from the project root):
#   bash benchmarks/benchmark_compare.sh [--no-cleanup] [--sizes 1,10,50,100]
#                                         [--concurrency 1,5,10,25] [--chunk 5]
#
# Options:
#   --no-cleanup        Keep docker-compose services running after the benchmark
#   --sizes             Comma-separated file sizes in MB  (default: 1,10,50,100)
#   --concurrency       Comma-separated concurrency values (default: 1,5,10,25)
#   --chunk             Chunk size in MB for gnet         (default: 5)
#   --gnet-addr         gnet-backend TCP address          (default: localhost:8081)
#   --flask-addr        flask_bench_server base URL       (default: http://localhost:5002)

set -euo pipefail

# ── defaults ─────────────────────────────────────────────────────────────────
CLEANUP=true
SIZES="1,10,50,100"
CONCURRENCY="1,5,10,25"
CHUNK=5
GNET_ADDR="localhost:8081"
FLASK_ADDR="http://localhost:5002"

# ── argument parsing ─────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case "$1" in
    --no-cleanup)   CLEANUP=false ;;
    --sizes)        SIZES="$2";       shift ;;
    --concurrency)  CONCURRENCY="$2"; shift ;;
    --chunk)        CHUNK="$2";       shift ;;
    --gnet-addr)    GNET_ADDR="$2";   shift ;;
    --flask-addr)   FLASK_ADDR="$2";  shift ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
  shift
done

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

GNET_OUT="$(mktemp /tmp/bench_gnet_XXXXXX.txt)"
FLASK_OUT="$(mktemp /tmp/bench_flask_XXXXXX.txt)"

# ── cleanup trap ──────────────────────────────────────────────────────────────
cleanup() {
  rm -f "$GNET_OUT" "$FLASK_OUT"
  if [[ "$CLEANUP" == true ]]; then
    echo ""
    echo "⟳  Stopping docker-compose services …"
    (cd "$PROJECT_ROOT" && docker compose down 2>/dev/null || docker-compose down 2>/dev/null) || true
  fi
}
trap cleanup EXIT

# ── helpers ───────────────────────────────────────────────────────────────────
check_cmd() {
  if ! command -v "$1" &>/dev/null; then
    echo "❌  '$1' not found. Please install it and re-run."
    exit 1
  fi
}

wait_http() {
  local url="$1" label="$2" retries=30
  echo -n "  Waiting for $label ($url) "
  for i in $(seq 1 $retries); do
    if curl -sf "$url" &>/dev/null; then
      echo " ✓"
      return 0
    fi
    echo -n "."
    sleep 3
  done
  echo " ✗"
  echo "❌  $label did not become healthy after $((retries * 3))s."
  exit 1
}

wait_tcp() {
  local host="$1" port="$2" label="$3" retries=30
  echo -n "  Waiting for $label ($host:$port) "
  for i in $(seq 1 $retries); do
    if bash -c "echo >/dev/tcp/$host/$port" &>/dev/null 2>&1; then
      echo " ✓"
      return 0
    fi
    echo -n "."
    sleep 3
  done
  echo " ✗"
  echo "❌  $label did not become available after $((retries * 3))s."
  exit 1
}

# ── prerequisites ─────────────────────────────────────────────────────────────
echo "┌─────────────────────────────────────────────────────┐"
echo "│          Upload Benchmark Suite – Comparison         │"
echo "└─────────────────────────────────────────────────────┘"
echo ""
echo "▸ Checking prerequisites …"
check_cmd docker
check_cmd go
check_cmd python3

COMPOSE_CMD="docker compose"
if ! docker compose version &>/dev/null 2>&1; then
  COMPOSE_CMD="docker-compose"
  check_cmd docker-compose
fi

# ── start services ────────────────────────────────────────────────────────────
echo ""
echo "▸ Starting docker-compose services …"
(cd "$PROJECT_ROOT" && $COMPOSE_CMD up -d --build)

echo ""
echo "▸ Waiting for services to be healthy …"
wait_http "http://localhost:9000/minio/health/live" "MinIO"
wait_tcp  "localhost" "8081" "gnet-backend"
wait_http "http://localhost:5002/health" "flask_bench_server"
echo ""

# ── gnet benchmark ────────────────────────────────────────────────────────────
echo "▸ Running gnet binary protocol benchmark …"
(
  cd "$PROJECT_ROOT"
  go run benchmarks/benchmark_gnet.go \
    -addr "$GNET_ADDR" \
    -sizes "$SIZES" \
    -concurrency "$CONCURRENCY" \
    -chunk "$CHUNK"
) | tee "$GNET_OUT"
echo ""

# ── Flask benchmark ───────────────────────────────────────────────────────────
echo "▸ Running Flask HTTP multipart benchmark …"
(
  cd "$PROJECT_ROOT"
  python3 benchmarks/benchmark_flask.py \
    --addr "$FLASK_ADDR" \
    --sizes "$SIZES" \
    --concurrency "$CONCURRENCY"
) | tee "$FLASK_OUT"
echo ""

# ── comparison table ──────────────────────────────────────────────────────────
echo "▸ Generating comparison …"
python3 - "$GNET_OUT" "$FLASK_OUT" <<'PYEOF'
import sys, re

def parse_csv(path, start_marker, end_marker):
    rows = []
    inside = False
    with open(path) as fh:
        for line in fh:
            line = line.rstrip()
            if start_marker in line:
                inside = True
                continue
            if end_marker in line:
                inside = False
                continue
            if inside and line and not line.startswith("size_mb"):
                parts = line.split(",")
                if len(parts) >= 4:
                    rows.append(parts)
    return rows

gnet_rows  = parse_csv(sys.argv[1], "gnet_csv_start",  "gnet_csv_end")
flask_rows = parse_csv(sys.argv[2], "flask_csv_start", "flask_csv_end")

# Index flask rows by (size_mb, concurrency)
flask_map = {}
for r in flask_rows:
    flask_map[(r[0].strip(), r[1].strip())] = r

header = (
    f"{'Size':>8} {'Conc':>5}  "
    f"{'gnet MB/s':>10} {'Flask MB/s':>11}  "
    f"{'gnet s':>7} {'Flask s':>8}  "
    f"{'Improv%':>8}"
)
sep = "-" * len(header)
print()
print("=" * len(header))
print("              Side-by-side Comparison")
print("=" * len(header))
print(header)
print(sep)

for gr in gnet_rows:
    key = (gr[0].strip(), gr[1].strip())
    fr  = flask_map.get(key)
    g_thr = float(gr[2])
    g_t   = float(gr[3])
    f_thr = float(fr[2]) if fr else None
    f_t   = float(fr[3]) if fr else None

    if f_thr and f_thr > 0:
        improv = (g_thr - f_thr) / f_thr * 100.0
        improv_str = f"{improv:+.1f}%"
    else:
        improv_str = "  N/A"

    f_thr_str = f"{f_thr:>11.2f}" if f_thr is not None else "        N/A"
    f_t_str   = f"{f_t:>8.3f}"   if f_t   is not None else "     N/A"

    print(
        f"{gr[0].strip():>8} {gr[1].strip():>5}  "
        f"{g_thr:>10.2f}{f_thr_str}  "
        f"{g_t:>7.3f}{f_t_str}  "
        f"{improv_str:>8}"
    )

print(sep)
print()
print("  Improv% = (gnet_throughput - flask_throughput) / flask_throughput × 100")
print("  Positive = gnet is faster")
print()
PYEOF
