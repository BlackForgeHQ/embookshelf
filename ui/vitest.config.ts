import { defineConfig } from "vitest/config"
import react from "@vitejs/plugin-react"

// Vite is only the test runner now; the app itself builds with Next.
export default defineConfig({
  plugins: [react()],
  resolve: {
    tsconfigPaths: true,
  },
})
