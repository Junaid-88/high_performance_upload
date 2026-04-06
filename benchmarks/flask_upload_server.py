#!/usr/bin/env python3
"""
flask_upload_server.py – standalone Flask upload server for benchmarking.

Exposes:
    POST /upload  – multipart/form-data with a 'file' field
    GET  /health  – returns {"status":"ok"}

Writes every upload directly to MinIO/S3 using boto3.
Run:
    python3 benchmarks/flask_upload_server.py

Environment variables (all optional):
    S3_ENDPOINT    http://localhost:9000
    S3_ACCESS_KEY  admin
    S3_SECRET_KEY  strongpassword
    S3_BUCKET      uploads
    S3_REGION      us-east-1
    FLASK_PORT     5002
"""

import os
import time
import uuid

import boto3
from botocore.client import Config
from flask import Flask, jsonify, request
from werkzeug.serving import run_simple

# ---------------------------------------------------------------------------
# Configuration
# ---------------------------------------------------------------------------

S3_ENDPOINT = os.environ.get("S3_ENDPOINT", "http://localhost:9000")
S3_ACCESS_KEY = os.environ.get("S3_ACCESS_KEY", "admin")
S3_SECRET_KEY = os.environ.get("S3_SECRET_KEY", "strongpassword")
S3_BUCKET = os.environ.get("S3_BUCKET", "uploads")
S3_REGION = os.environ.get("S3_REGION", "us-east-1")
FLASK_PORT = int(os.environ.get("FLASK_PORT", "5002"))

# ---------------------------------------------------------------------------
# S3 client
# ---------------------------------------------------------------------------

s3_client = boto3.client(
    "s3",
    endpoint_url=S3_ENDPOINT,
    aws_access_key_id=S3_ACCESS_KEY,
    aws_secret_access_key=S3_SECRET_KEY,
    region_name=S3_REGION,
    config=Config(signature_version="s3v4"),
)


def ensure_bucket():
    """Create the bucket if it does not already exist."""
    try:
        s3_client.head_bucket(Bucket=S3_BUCKET)
    except Exception:  # noqa: BLE001
        try:
            s3_client.create_bucket(Bucket=S3_BUCKET)
            print(f"[flask_upload_server] Created bucket: {S3_BUCKET}")
        except Exception as exc:  # noqa: BLE001
            print(f"[flask_upload_server] Warning – could not create bucket: {exc}")


# ---------------------------------------------------------------------------
# Flask app
# ---------------------------------------------------------------------------

app = Flask(__name__)
# Disable the default maximum content length so large files are not rejected.
app.config["MAX_CONTENT_LENGTH"] = None


@app.route("/health", methods=["GET"])
def health():
    return jsonify({"status": "ok"})


@app.route("/upload", methods=["POST"])
def upload():
    if "file" not in request.files:
        return jsonify({"error": "No 'file' field in request"}), 400

    f = request.files["file"]
    filename = f.filename or f"upload_{uuid.uuid4()}"
    s3_key = f"bench/{int(time.time())}_{uuid.uuid4()}_{filename}"

    # Stream directly from the request file object to avoid buffering the
    # entire upload in memory – important for large benchmark payloads.
    try:
        s3_client.upload_fileobj(f.stream, S3_BUCKET, s3_key)
    except Exception:  # noqa: BLE001
        return jsonify({"error": "S3 upload failed"}), 500

    return jsonify({"status": "ok", "s3_key": s3_key})


# ---------------------------------------------------------------------------
# Entry point
# ---------------------------------------------------------------------------

if __name__ == "__main__":
    ensure_bucket()
    print(f"[flask_upload_server] Starting on port {FLASK_PORT}")
    print(f"[flask_upload_server] S3 endpoint : {S3_ENDPOINT}")
    print(f"[flask_upload_server] S3 bucket   : {S3_BUCKET}")
    run_simple(
        "0.0.0.0",
        FLASK_PORT,
        app,
        threaded=True,
        use_reloader=False,
    )
