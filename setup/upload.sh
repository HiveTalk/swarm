#!/bin/bash

# Simple wrapper for blossom upload
# Usage: ./upload.sh <file-path> [--prompt-sec]

SERVER_URL="https://swarm.hivetalk.org"

if [ $# -lt 1 ]; then
    echo "Usage: ./upload.sh <file-path> [--prompt-sec]"
    echo "Example: ./upload.sh ~/Desktop/video.mp4 --prompt-sec"
    echo "Example: ./upload.sh ~/Desktop/video.mp4"
    exit 1
fi

FILE_PATH="$1"
PROMPT_SEC="$2"

if [ ! -f "$FILE_PATH" ]; then
    echo "Error: File '$FILE_PATH' not found"
    exit 1
fi

echo "🚀 Direct Blossom Upload Tool"
echo "📡 Server: $SERVER_URL"
echo ""

if [ "$PROMPT_SEC" = "--prompt-sec" ]; then
    go run blossom-upload.go --prompt-sec "$SERVER_URL" "$FILE_PATH"
else
    go run blossom-upload.go "$SERVER_URL" "$FILE_PATH"
fi
