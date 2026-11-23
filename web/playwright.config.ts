import { mkdirSync, mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

import { defineConfig, devices } from '@playwright/test';

// Runs against the production binary (`make build`, SPA embedded, strict CSP), not the Vite dev
// server, so the test sees what users get. DATABASE_URL must point at a disposable database.
// e2e/fake-services.ts stands in for Prowlarr and qBittorrent; the library and download folders
// are fresh temporary folders.
const port = process.env.E2E_PORT ?? '18080';
const fakePort = process.env.E2E_FAKE_PORT ?? '18090';
const fakeKey = 'e2e-prowlarr-key';
// Workers load this file again; they inherit the folder made by the first load.
const work = process.env.E2E_WORK_DIR ?? mkdtempSync(join(tmpdir(), 'rom-request-e2e-'));
process.env.E2E_WORK_DIR = work;
const downloads = join(work, 'downloads');
const library = join(work, 'library');
for (const dir of [downloads, join(library, 'GBA'), join(library, 'PS1'), join(library, 'bios')]) {
  mkdirSync(dir, { recursive: true });
}
process.env.E2E_LIBRARY_DIR = library;

export default defineConfig({
  testDir: 'e2e',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [['list'], ['html', { open: 'never' }]] : 'list',
  use: {
    baseURL: `http://localhost:${port}`,
    trace: 'retain-on-failure',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
  webServer: [
    {
      command: 'node e2e/fake-services.ts',
      url: `http://127.0.0.1:${fakePort}/healthz`,
      reuseExistingServer: false,
      env: { FAKE_PORT: fakePort, FAKE_PROWLARR_KEY: fakeKey },
    },
    {
    command: '../bin/app serve',
    url: `http://localhost:${port}/readyz`,
    reuseExistingServer: false,
    timeout: 30_000,
    env: {
      LISTEN_ADDR: `127.0.0.1:${port}`,
      ALLOW_SIGNUP: 'true',
      LOG_LEVEL: 'warn',
      DATABASE_URL: process.env.DATABASE_URL ?? '',
      PROWLARR_URL: `http://127.0.0.1:${fakePort}`,
      PROWLARR_API_KEY: fakeKey,
      QBITTORRENT_URL: `http://127.0.0.1:${fakePort}`,
      QBITTORRENT_SAVE_PATH: downloads,
      DOWNLOAD_DIR: downloads,
      LIBRARY_DIR: library,
      WORKER_INTERVAL: '1s',
    },
    },
  ],
});
