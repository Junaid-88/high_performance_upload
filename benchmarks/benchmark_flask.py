#!/usr/bin/env python3
"""
benchmark_flask.py – benchmark the Flask HTTP multipart upload path.

Usage (from the project root):
    python3 benchmarks/benchmark_flask.py [options]

Options:
    --addr         Base URL of the flask_bench_server  (default: http://localhost:5002)
    --sizes        Comma-separated file sizes in MB     (default: 1,10,50,100)
    --concurrency  Comma-separated concurrency values   (default: 1,5,10,25)
"""

import argparse
import io
import os
import time
from concurrent.futures import ThreadPoolExecutor, as_completed

import requests
from tabulate import tabulate


# ---------------------------------------------------------------------------
# single upload
# ---------------------------------------------------------------------------

def run_upload(base_url: str, file_size_bytes: int, upload_idx: int):
    """
    POST file_size_bytes of random data to POST /upload.
    Returns (total_time_s, error_str_or_None).
    """
    data = os.urandom(file_size_bytes)
    filename = f"bench_{upload_idx}_{int(time.time() * 1000)}.mp4"

    start = time.perf_counter()
    try:
        resp = requests.post(
            f"{base_url}/upload",
            files={"file": (filename, io.BytesIO(data), "application/octet-stream")},
            timeout=300,
        )
        elapsed = time.perf_counter() - start
        if resp.status_code != 200:
            return elapsed, f"HTTP {resp.status_code}: {resp.text[:200]}"
        return elapsed, None
    except Exception as exc:  # noqa: BLE001
        elapsed = time.perf_counter() - start
        return elapsed, str(exc)


# ---------------------------------------------------------------------------
# benchmark harness
# ---------------------------------------------------------------------------

def run_benchmark(base_url: str, file_size_mb: int, concurrency: int, warmup: int = 2):
    file_size_bytes = file_size_mb * 1024 * 1024

    # warm-up
    for i in range(warmup):
        run_upload(base_url, file_size_bytes, -(i + 1))

    times = []
    errors = 0

    with ThreadPoolExecutor(max_workers=concurrency) as pool:
        futures = [
            pool.submit(run_upload, base_url, file_size_bytes, idx)
            for idx in range(concurrency)
        ]
        for fut in as_completed(futures):
            elapsed, err = fut.result()
            if err:
                errors += 1
            else:
                times.append(elapsed)

    if not times:
        return {
            "size_mb": file_size_mb,
            "concurrency": concurrency,
            "throughput_mbps": 0.0,
            "avg_time_s": 0.0,
            "errors": errors,
        }

    avg_time = sum(times) / len(times)
    throughput = file_size_mb / avg_time if avg_time > 0 else 0.0

    return {
        "size_mb": file_size_mb,
        "concurrency": concurrency,
        "throughput_mbps": throughput,
        "avg_time_s": avg_time,
        "errors": errors,
    }


# ---------------------------------------------------------------------------
# main
# ---------------------------------------------------------------------------

def main():
    parser = argparse.ArgumentParser(description="Flask HTTP upload benchmark")
    parser.add_argument("--addr", default="http://localhost:5002",
                        help="Base URL of the flask_bench_server")
    parser.add_argument("--sizes", default="1,10,50,100",
                        help="Comma-separated file sizes in MB")
    parser.add_argument("--concurrency", default="1,5,10,25",
                        help="Comma-separated concurrency levels")
    args = parser.parse_args()

    sizes = [int(s.strip()) for s in args.sizes.split(",") if s.strip()]
    concurrencies = [int(c.strip()) for c in args.concurrency.split(",") if c.strip()]

    print("\n=== Flask HTTP Multipart Upload Benchmark ===")
    print(f"Server : {args.addr}")
    print(f"Sizes  : {sizes} MB")
    print(f"Conc.  : {concurrencies}\n")

    results = []
    for size in sizes:
        for conc in concurrencies:
            print(f"  running: {size} MB × concurrency {conc} …", end="", flush=True)
            r = run_benchmark(args.addr, size, conc)
            results.append(r)
            print(f" {r['throughput_mbps']:.2f} MB/s")

    # Pretty table
    headers = ["Size(MB)", "Concurrency", "Throughput(MB/s)", "Avg Time(s)", "Errors"]
    rows = [
        [
            r["size_mb"],
            r["concurrency"],
            f"{r['throughput_mbps']:.2f}",
            f"{r['avg_time_s']:.3f}",
            r["errors"],
        ]
        for r in results
    ]
    print()
    print(tabulate(rows, headers=headers, tablefmt="grid"))

    # Machine-readable CSV for benchmark_compare.sh
    print("\n--- flask_csv_start ---")
    print("size_mb,concurrency,throughput_mbps,avg_time_s,errors")
    for r in results:
        print(f"{r['size_mb']},{r['concurrency']},{r['throughput_mbps']:.4f},{r['avg_time_s']:.4f},{r['errors']}")
    print("--- flask_csv_end ---")


if __name__ == "__main__":
    main()
