import { readFileSync } from 'node:fs';
import { join } from 'node:path';

import { expect, test, type Page } from '@playwright/test';

// Any CSP violation or script error fails a test. Chrome also logs every 4xx response, and 401s
// are expected here (signed out, wrong password).
function watchForProblems(page: Page): string[] {
  const problems: string[] = [];
  page.on('console', (m) => {
    if (m.type() === 'error' && !m.text().includes('status of 401')) problems.push(m.text());
  });
  page.on('pageerror', (e) => problems.push(e.message));
  return problems;
}

async function signUp(page: Page) {
  const email = `e2e-${String(Date.now())}-${String(Math.random()).slice(2, 8)}@example.com`;
  const password = 'a long enough password';
  await page.goto('/');
  await expect(page).toHaveURL(/\/login$/);
  await page.getByRole('button', { name: /sign up/i }).click();
  await page.getByLabel('Email').fill(email);
  await page.getByLabel('Password').fill(password);
  await page.getByRole('button', { name: 'Sign up' }).click();
  await expect(page.getByRole('heading', { name: 'Browse games' })).toBeVisible();
  return { email, password };
}

test('sign up, sign out and back in', async ({ page }) => {
  const problems = watchForProblems(page);
  const response = await page.goto('/');
  expect(response?.headers()['content-security-policy']).toContain("script-src 'self'");

  const { email, password } = await signUp(page);

  await page.getByRole('link', { name: 'Requests' }).click();
  await expect(page.getByText('No requests yet.')).toBeVisible();

  await page.getByRole('button', { name: 'Sign out' }).click();
  await expect(page).toHaveURL(/\/login$/);
  await page.goto('/requests');
  await expect(page).toHaveURL(/\/login$/);

  await page.getByLabel('Email').fill(email);
  await page.getByLabel('Password').fill('the wrong password');
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByRole('alert')).toHaveText('Wrong email or password.');

  await page.getByLabel('Password').fill(password);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByRole('heading', { name: 'Browse games' })).toBeVisible();

  expect(problems).toEqual([]);
});

test('search, request, and follow a release into the library', async ({ page }) => {
  const problems = watchForProblems(page);
  await signUp(page);

  await page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Search' }).click();
  await page.getByLabel('Game or release').fill('homebrew');
  await page.getByRole('button', { name: 'Search' }).click();
  const result = page.getByRole('list', { name: 'Results' }).getByRole('listitem').filter({ hasText: 'Homebrew Quest' });
  await expect(result).toContainText('Homebrew Hub');

  await result.getByRole('button', { name: 'Request' }).click();
  const form = page.getByRole('form', { name: 'Request Homebrew Quest' });
  await form.getByLabel('Platform').selectOption('GBA');
  await form.getByLabel('License').fill('CC BY 4.0');
  await form.getByLabel('Release page').fill('https://example.com/homebrew-quest');
  await form.getByRole('button', { name: 'Request' }).click();

  await expect(page.getByRole('heading', { name: 'Requests' })).toBeVisible();
  const item = page.getByRole('list', { name: 'Requests' }).getByRole('listitem').first();
  await expect(item.getByText('In library')).toBeVisible({ timeout: 15_000 });
  await expect(item).toContainText('GBA/Homebrew Quest.gba');
  await expect(item).toContainText('CC BY 4.0');

  const library = process.env.E2E_LIBRARY_DIR ?? '';
  expect(readFileSync(join(library, 'GBA', 'Homebrew Quest.gba'), 'utf8')).toBe('e2e homebrew rom');

  // Removing the request leaves the library alone.
  await item.getByRole('button', { name: 'Remove' }).click();
  await item.getByRole('button', { name: 'Remove request' }).click();
  await expect(page.getByText('No requests yet.')).toBeVisible();
  expect(readFileSync(join(library, 'GBA', 'Homebrew Quest.gba'), 'utf8')).toBe('e2e homebrew rom');

  expect(problems).toEqual([]);
});
