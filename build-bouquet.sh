#!/bin/bash

# Build script for integrating Bouquet client with Go backend

set -e

echo "🌺 Building Bouquet client for integration with Go backend..."

# Navigate to bouquet directory
cd clients/bouquet

# Install dependencies if node_modules doesn't exist
if [ ! -d "node_modules" ]; then
    echo "📦 Installing dependencies..."
    pnpm install
fi

# Build the client
echo "🔨 Building Bouquet client..."
pnpm run build:integration

# Check if build was successful
if [ -d "../../bouquet-dist" ]; then
    echo "✅ Bouquet client built successfully!"
    echo "📁 Static files are now available in ./bouquet-dist/"
    echo "🚀 Start your Go server and visit http://localhost:3334/bouquet/"
else
    echo "❌ Build failed - bouquet-dist directory not found"
    exit 1
fi

# Navigate back to root
cd ../..

echo "🎉 Integration complete! The Bouquet client is now served by your Go backend."
