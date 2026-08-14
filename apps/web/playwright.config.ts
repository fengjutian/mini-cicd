import { defineConfig, devices } from '@playwright/test'

export default defineConfig({
  testDir: './e2e',
  timeout: 30_000,
  retries: process.env.CI ? 2 : 0,
  reporter: process.env.CI ? 'github' : 'list',
  use: { baseURL: 'http://127.0.0.1:18081', trace: 'retain-on-failure' },
  projects: [
    { name: 'desktop', use: { ...devices['Desktop Chrome'] } },
    { name: 'mobile', use: { ...devices['Pixel 7'] } },
  ],
  webServer: {
    command: 'cd ../.. && go run ./apps/server/cmd/minicicd',
    url: 'http://127.0.0.1:18081/healthz',
    reuseExistingServer: !process.env.CI,
    env: {
      MINICICD_LISTEN_ADDR: '127.0.0.1:18081',
      MINICICD_DATA_DIR: process.env.MINICICD_E2E_DATA_DIR || '/tmp/minicicd-e2e',
      MINICICD_MASTER_KEY: 'MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE',
      MINICICD_BACKUP_INTERVAL: '24h',
    },
  },
})
