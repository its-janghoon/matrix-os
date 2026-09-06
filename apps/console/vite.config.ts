import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Vite config for the Matrix Console.
//
// The dev server is pinned to port 5173 (strictPort) so tooling and docs can
// rely on a stable URL. When running inside `tauri dev`, Tauri points its
// WebView at this same dev server; for production it loads the built `dist/`.
//
// `TAURI_*` env vars set by the Tauri CLI are honoured when present, but the
// frontend builds and runs identically in a plain browser without Tauri.
export default defineConfig(() => ({
  plugins: [react()],
  clearScreen: false,
  server: {
    host: "0.0.0.0",
    port: 5173,
    strictPort: true,
  },
  build: {
    // Emit a modern bundle; Tauri's WebView and evergreen browsers both support
    // ES2020. `dist/` is the artifact consumed by both `vite preview` and the
    // Tauri production build.
    target: "es2020",
    outDir: "dist",
    sourcemap: false,
  },
}));
