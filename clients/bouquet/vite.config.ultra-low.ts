import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react-swc'

// Ultra-low memory configuration for systems with <1GB RAM
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: '../../bouquet-dist',
    emptyOutDir: true,
    // Ultra-aggressive memory optimizations
    chunkSizeWarningLimit: 200, // Very small chunks
    minify: false, // Disable minification to save memory
    target: 'es2020',
    sourcemap: false, // Disable sourcemaps to save memory
    reportCompressedSize: false, // Skip gzip size reporting to save memory
    rollupOptions: {
      // Minimize memory usage
      maxParallelFileOps: 1, // No parallel processing
      cache: false, // Disable caching to save memory
      output: {
        // Extremely aggressive chunking - split everything
        manualChunks: (id) => {
          if (id.includes('node_modules')) {
            // Create very small chunks for each package
            const match = id.match(/node_modules\/([^\/]+)/);
            if (match) {
              const packageName = match[1].replace(/[^a-zA-Z0-9]/g, '');
              // Group some tiny packages together
              if (packageName.length < 4) return 'tiny-vendor';
              return `pkg-${packageName}`;
            }
            return 'vendor-misc';
          }
          // Split app code into very small chunks
          if (id.includes('/components/')) {
            const componentMatch = id.match(/\/components\/([^\/]+)/);
            if (componentMatch) {
              return `comp-${componentMatch[1].replace(/[^a-zA-Z0-9]/g, '')}`;
            }
            return 'components';
          }
          if (id.includes('/utils/')) return 'utils';
          if (id.includes('/pages/')) return 'pages';
        }
      }
    }
  },
  base: '/bouquet/',
})
