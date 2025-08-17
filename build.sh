#!/bin/bash

# Prerequisite: go install golang.org/x/tools/cmd/goimports@latest
# Prerequisite: npm install -g esbuild

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

if [ $? -ne 0 ]; then
    echo "!!> JavaScript bundling failed. Aborting."
    exit 1
fi

echo "    -> Bundled to $JS_OUTPUT_FILE"


# --- Go Compilation ---
echo "==> Compiling Go binary with FTS5 support..."
go build -tags "sqlite_fts5" -o yib .


if [ $? -ne 0 ]; then
    echo "!!> Go compilation failed. Aborting."
    exit 1
fi

echo ""
echo "Build complete! Run with:"
echo "./yib"
echo ""
echo "Or with custom config:"
echo "YIB_PORT=8888 YIB_DB_PATH=./prod.db ./yib"
echo ""