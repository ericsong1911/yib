#!/bin/bash

# A simple build script for yib.
# Prerequisite: go install golang.org/x/tools/cmd/goimports@latest
# Prerequisite: npm install -g esbuild

# --- Script Configuration ---
# Exit immediately if a command exits with a non-zero status.
set -e

echo "==> Tidying Go modules..."
go mod tidy

# --- JavaScript Bundling ---
echo "==> Bundling JavaScript assets..."
JS_SOURCE_DIR="frontend"
JS_DIST_DIR="static/dist"
JS_OUTPUT_FILE="$JS_DIST_DIR/board.min.js"

# Create dist directory if it doesn't exist
mkdir -p "$JS_DIST_DIR"

# Use esbuild to bundle our ES Modules into a single minified file.
esbuild "$JS_SOURCE_DIR/main.js" --bundle --minify --outfile="$JS_OUTPUT_FILE"

echo "    -> Bundled to $JS_OUTPUT_FILE"

# --- Go Test Suite ---
echo "==> Running Go test suite with FTS5 support..."
go test -tags fts5 -v ./...

# --- Go Compilation ---
echo "==> Compiling Go binary with FTS5 support..."
# The 'sqlite_fts5' tag is required for the final binary build.
go build -tags "sqlite_fts5" -ldflags "-s -w" -o yib .

echo ""
echo "=============================="
echo "Build complete! Run with:"
echo "./yib"
echo ""
echo "Or with custom config:"
echo "YIB_PORT=8888 YIB_DB_PATH=./prod.db ./yib"
echo "=============================="
echo ""