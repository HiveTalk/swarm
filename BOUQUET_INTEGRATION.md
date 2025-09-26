# Bouquet Client Integration

This document explains how the Bouquet client is integrated with the Go backend to provide a seamless single-server experience.

## 🏗️ Architecture

The integration uses a **static file serving approach** where:

1. **Bouquet client** is built as static assets (HTML, CSS, JS)
2. **Go backend** serves these static files at `/bouquet/`
3. **Single server** runs on port 3334 serving both the API and web interface

## 🚀 Quick Setup

### 1. Build the Bouquet Client

```bash
# Option A: Use the build script (recommended)
./build-bouquet.sh

# Option B: Manual build
cd clients/bouquet
pnpm install
pnpm run build:integration
cd ../..
```

### 2. Start the Go Server

```bash
# Build and run the Go server
go build -o swarm
./swarm
```

### 3. Access the Interface

- **Main landing page**: http://localhost:3334/
- **Bouquet client**: http://localhost:3334/bouquet/
- **API endpoints**: http://localhost:3334/upload, /list/, etc.

## 🔧 How It Works

### Vite Configuration

The Bouquet client is configured to:
- Build to `../../bouquet-dist/` (relative to Go root)
- Use `/bouquet/` as the base path
- Generate static assets optimized for production

### React Router Configuration

The React Router is configured with:
- `basename: "/bouquet"` to handle the base path correctly
- SPA routing support for client-side navigation

### Go Backend Integration

The Go server includes a `setupBouquetHandler()` function that:
- Serves static files from `./bouquet-dist/`
- Handles `/bouquet/` routes with SPA support
- Falls back to `index.html` for client-side routes (404s)
- Redirects `/bouquet` to `/bouquet/`

### Frontend Landing Page

The main landing page (`frontend.go`) includes:
- A dedicated Bouquet Client section
- Launch button linking to `/bouquet/`
- Only shown when Blossom is enabled

## 🎯 Benefits

✅ **Single server** - No need to run separate development servers  
✅ **Production ready** - Optimized static assets  
✅ **Seamless integration** - Direct links from landing page  
✅ **Same domain** - No CORS issues  
✅ **Easy deployment** - Single binary + static files  

## 🔄 Development Workflow

### For Go Backend Changes
```bash
go build -o swarm && ./swarm
```

### For Bouquet Client Changes
```bash
cd clients/bouquet
pnpm run dev  # Development server on :5173
# OR
pnpm run build:integration && cd ../.. && ./swarm  # Test integration
```

### For Production Deployment
```bash
./build-bouquet.sh  # Build client
go build -o swarm   # Build server
./swarm            # Run integrated server
```

## 📁 File Structure

```
swarm/
├── main.go                    # Go server with Bouquet integration
├── frontend.go               # Landing page with Bouquet link
├── bouquet-dist/            # Built Bouquet client (generated)
├── build-bouquet.sh         # Build script
├── clients/bouquet/         # Bouquet source code
│   ├── vite.config.ts       # Configured for integration
│   └── package.json         # With build:integration script
└── BOUQUET_INTEGRATION.md   # This file
```

## 🔍 Troubleshooting

**404 on /bouquet/**: Ensure you've run `./build-bouquet.sh` to build the client

**CORS errors**: The integration eliminates CORS issues by serving from the same domain

**Build errors**: Check that pnpm is installed and run `pnpm install` in `clients/bouquet/`

**Static files not updating**: Rebuild with `./build-bouquet.sh` after client changes
