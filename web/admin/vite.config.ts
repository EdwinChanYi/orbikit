import vue from "@vitejs/plugin-vue";
import { defineConfig } from "vitest/config";

// outDir is the directory //go:embed reads in internal/admin/static.
// Go cannot embed files outside that package.
export default defineConfig({
  plugins: [vue()],
  server: {
    proxy: {
      "/api": "http://127.0.0.1:8091",
    },
  },
  build: {
    outDir: "../../internal/admin/static/dist",
    emptyOutDir: true,
  },
  test: {
    environment: "jsdom",
  },
});
