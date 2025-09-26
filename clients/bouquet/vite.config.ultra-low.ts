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
    rollupOptions: {
      // Minimize memory usage
      maxParallelFileOps: 1, // No parallel processing
      output: {
        // Extremely aggressive chunking
        manualChunks: (id) => {
          if (id.includes('node_modules')) {
            // Split every major dependency into its own chunk
            if (id.includes('react/')) return 'react';
            if (id.includes('react-dom/')) return 'react-dom';
            if (id.includes('@nostr-dev-kit/ndk')) return 'ndk';
            if (id.includes('nostr-tools')) return 'nostr-tools';
            if (id.includes('@heroicons')) return 'heroicons';
            if (id.includes('@tanstack/react-query')) return 'react-query';
            if (id.includes('blossom-client-sdk')) return 'blossom';
            if (id.includes('lodash')) return 'lodash';
            if (id.includes('dayjs')) return 'dayjs';
            if (id.includes('axios')) return 'axios';
            // Split remaining vendor code into smaller chunks
            const parts = id.split('node_modules/')[1]?.split('/');
            if (parts && parts[0]) {
              return `vendor-${parts[0].replace(/[^a-zA-Z0-9]/g, '')}`;
            }
            return 'vendor-misc';
          }
          // Split app code by directory
          if (id.includes('/components/')) return 'components';
          if (id.includes('/utils/')) return 'utils';
          if (id.includes('/pages/')) return 'pages';
        }
      }
    }
  },
  base: '/bouquet/',
})
