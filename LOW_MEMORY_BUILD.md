# Low Memory Build Guide

## For Systems with 1GB RAM or Less

### Quick Start
```bash
# The build script automatically detects low memory and optimizes
./build-bouquet.sh
```

### Manual Low-Memory Build
```bash
cd clients/bouquet
pnpm run build:low-memory
```

### Additional Optimizations

#### 1. Enable Swap (if not already enabled)
```bash
# Check current swap
free -h

# Create 1GB swap file if needed
sudo fallocate -l 1G /swapfile
sudo chmod 600 /swapfile
sudo mkswap /swapfile
sudo swapon /swapfile
```

#### 2. Close Other Applications
- Close browser tabs
- Stop other Node.js processes
- Temporarily stop services like Docker

#### 3. Build Environment Variables
```bash
# Set memory limits (gc-interval not supported in NODE_OPTIONS on some systems)
export NODE_OPTIONS="--max-old-space-size=384"

# Reduce npm/pnpm cache
export npm_config_cache=/tmp/npm-cache

# Alternative: Use node directly with gc flags (if needed)
# node --max-old-space-size=384 --gc-interval=100 ./node_modules/.bin/tsc
```

#### 4. Alternative: Use GitHub Actions
If local builds still fail, consider using GitHub Actions for building:

```yaml
# .github/workflows/build-bouquet.yml
name: Build Bouquet
on: [push]
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - uses: actions/setup-node@v3
        with:
          node-version: '18'
      - run: cd clients/bouquet && npm install
      - run: cd clients/bouquet && npm run build:low-memory
      - uses: actions/upload-artifact@v3
        with:
          name: bouquet-dist
          path: bouquet-dist/
```

### Memory Usage Breakdown
- **TypeScript compilation**: ~384MB
- **Vite bundling**: ~512MB  
- **Peak usage**: ~800MB total
- **Safety margin**: ~200MB for system

### Troubleshooting
If build still fails:
1. Try `build:low-memory` directly
2. Restart terminal to clear memory
3. Check `free -m` for available memory
4. Consider using a build server or CI/CD
