import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react-swc'

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: '../../bouquet-dist', // Build to root directory for Go to serve
    emptyOutDir: true,
    // Optimize for low memory systems
    chunkSizeWarningLimit: 500,
    minify: 'esbuild', // Use esbuild (default, no extra dependency needed)
    target: 'es2020', // Optimize for modern browsers to reduce bundle size
    rollupOptions: {
      // Reduce memory usage during build
      maxParallelFileOps: 2, // Limit concurrent operations
      output: {
        // More aggressive chunking for memory efficiency
        manualChunks: (id) => {
          // Split node_modules into smaller chunks
          if (id.includes('node_modules')) {
            if (id.includes('react') || id.includes('react-dom')) {
              return 'react-vendor';
            }
            if (id.includes('@nostr-dev-kit') || id.includes('nostr-tools')) {
              return 'nostr-vendor';
            }
            if (id.includes('@heroicons') || id.includes('daisyui')) {
              return 'ui-vendor';
            }
            if (id.includes('@tanstack')) {
              return 'query-vendor';
            }
            // Split other large libraries
            if (id.includes('lodash') || id.includes('dayjs')) {
              return 'utils-vendor';
            }
            return 'vendor'; // Everything else
          }
        }
      }
    }
  },
  base: '/bouquet/', // Serve from /bouquet/ path
})
