import { defineConfig, devices } from '@playwright/test';

export default defineConfig({
  testDir: '{{TEST_DIR}}',
  timeout: 10000,
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: 0,
  workers: process.env.CI ? 1 : undefined,
  reporter: [['html', { open: 'never' }], ['list']],
  use: {
    baseURL: process.env.BASE_URL || '{{BASE_URL}}',
    trace: 'on-first-retry',
  },
  projects: [
    {
      name: 'chromium',
      use: {
        ...devices['Desktop Chrome'],
        launchOptions: {
          args: [
            '--disable-features=HttpsUpgrades,HttpsFirstModeV2,AutomaticHttpsDefault,AutomaticHttpsDefaultEnabled,HttpsOnlyMode',
            '--ignore-certificate-errors',
          ],
        },
      },
    },
  ],
});
