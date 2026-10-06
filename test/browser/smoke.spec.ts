import { expect, test } from '@playwright/test';
import { getPublicKey } from 'nostr-tools';

test('front page renders relay information', async ({ page }) => {
  await page.goto('/');
  await expect(page).toHaveTitle(/Swarm|Relay/i);
  await expect(page.locator('body')).toContainText(/relay/i);
});

test('dashboard renders', async ({ page }) => {
  const response = await page.goto('/dashboard');
  expect(response?.ok()).toBeTruthy();
  await expect(page.locator('body')).toBeVisible();
});

test('converter renders', async ({ page }) => {
  const response = await page.goto('/convert');
  expect(response?.ok()).toBeTruthy();
  await expect(page.locator('body')).toBeVisible();
});

test('admin signs in and loads the protected dashboard', async ({ page }) => {
  const secretKey = process.env.E2E_ADMIN_SECRET_KEY;
  test.skip(!secretKey, 'E2E_ADMIN_SECRET_KEY is not set');
  const publicKey = getPublicKey(Buffer.from(secretKey!, 'hex'));
  await page.addInitScript(({ secretKey, publicKey }) => {
    window.nostr = {
      getPublicKey: async () => publicKey,
      signEvent: async (event: Record<string, unknown>) => {
        const bytes = new Uint8Array(secretKey.match(/.{2}/g)!.map((value) => Number.parseInt(value, 16)));
        return window.NostrTools.finalizeEvent(event, bytes);
      },
    };
  }, { secretKey, publicKey });
  await page.goto('/dashboard');
  await page.getByRole('button', { name: 'Login with Nostr Extension' }).click();
  await page.locator('#nl-ext-btn').click();
  await expect(page.locator('#dashboard')).toContainText('Users Management');
  await expect(page.locator('#loginScreen')).toBeHidden();
});
