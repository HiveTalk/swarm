#!/bin/bash

# Build script for integrating Bouquet client with Go backend

set -e

echo "🌺 Building Bouquet client for integration with Go backend..."

# Check available memory
AVAILABLE_MEM=$(free -m 2>/dev/null | awk 'NR==2{printf "%.0f", $7}' || echo "unknown")
echo "💾 Available memory: ${AVAILABLE_MEM}MB"

# Navigate to bouquet directory
cd clients/bouquet

# Install dependencies if node_modules doesn't exist
if [ ! -d "node_modules" ]; then
    echo "📦 Installing dependencies..."
    pnpm install
fi

# Choose build strategy based on available memory
if [[ "$AVAILABLE_MEM" != "unknown" && "$AVAILABLE_MEM" -lt 400 ]]; then
    echo "🔨 Building with extreme memory constraints..."
    echo "⚠️  Only ${AVAILABLE_MEM}MB available - this will be very slow"
    echo "💡 Consider enabling swap: sudo fallocate -l 1G /swapfile && sudo mkswap /swapfile && sudo swapon /swapfile"
    pnpm run build:extreme
elif [[ "$AVAILABLE_MEM" != "unknown" && "$AVAILABLE_MEM" -lt 600 ]]; then
    echo "🔨 Building with ultra-low memory optimizations..."
    echo "⚠️  This may take longer but uses minimal memory"
    pnpm run build:ultra-low
elif [[ "$AVAILABLE_MEM" != "unknown" && "$AVAILABLE_MEM" -lt 1000 ]]; then
    echo "🔨 Building with low-memory optimizations..."
    pnpm run build:low-memory
else
    echo "🔨 Building Bouquet client..."
    pnpm run build:integration
fi

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
