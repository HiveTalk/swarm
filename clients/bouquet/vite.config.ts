import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react-swc'

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: '../../bouquet-dist', // Build to root directory for Go to serve
    emptyOutDir: true,
  },
  base: '/bouquet/', // Serve from /bouquet/ path
})
