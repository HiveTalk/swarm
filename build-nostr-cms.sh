#!/bin/bash
set -e

echo "Building nostr-cms for integration with Swarm..."

cd clients/nostr-cms

if [ ! -d "node_modules" ]; then
    echo "Installing dependencies..."
    npm install --silent
fi

echo "Building nostr-cms..."
VITE_MASTER_PUBKEY="${VITE_MASTER_PUBKEY:-}" \
  npx vite build --outDir ../../nostr-cms-dist

cd ../..

if [ -d "nostr-cms-dist" ]; then
    echo "nostr-cms built successfully -> ./nostr-cms-dist/"
else
    echo "Build failed"
    exit 1
fi
