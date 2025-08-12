#!/bin/bash

echo "==> Tidying Go modules..."
go mod tidy

# --- JavaScript Bundling ---
echo "==> Bundling JavaScript assets..."
JS_SOURCE_DIR="frontend"
JS_DIST_DIR="static/dist"
JS_OUTPUT_FILE="$JS_DIST_DIR/board.min.js"

# Create dist directory if it doesn't exist
mkdir -p "$JS_DIST_DIR"

# Bundling
esbuild "$JS_SOURCE_DIR/main.js" --bundle --minify --outfile="$JS_OUTPUT_FILE"

echo "    -> Bundled to $JS_OUTPUT_FILE"

echo "==> Building..."

go build -tags "sqlite_fts5" -o yib-server .

echo "=================="
echo "Build complete! Run with:"
echo "./yib-server"
echo 
echo "Or with custom config:"
echo "YIB_PORT=8888 YIB_DB_PATH=./prod.db ./yib"
echo "=================="