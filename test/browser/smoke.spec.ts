import { expect, test } from '@playwright/test';

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
