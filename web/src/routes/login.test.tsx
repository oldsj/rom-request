import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { errorMessage } from '../api/client';

// The router is mocked: these tests cover the form, not navigation.
vi.mock('@tanstack/react-router', () => ({ useNavigate: () => vi.fn() }));
const { LoginPage } = await import('./login');

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <LoginPage />
    </QueryClientProvider>,
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('LoginPage', () => {
  it('shows the server error message on failed sign-in', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ error: { code: 'invalid_credentials', message: 'Wrong email or password.' } }), {
        status: 401,
        headers: { 'Content-Type': 'application/json' },
      }),
    );
    vi.stubGlobal('fetch', fetchMock);
    renderPage();

    await userEvent.type(screen.getByLabelText('Email'), 'ada@example.com');
    await userEvent.type(screen.getByLabelText('Password'), 'a long enough password');
    await userEvent.click(screen.getByRole('button', { name: 'Sign in' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('Wrong email or password.');
    const request = fetchMock.mock.calls[0]?.[0] as Request;
    expect(new URL(request.url).pathname).toBe('/api/auth/login');
    expect(request.method).toBe('POST');
  });

  it('switches to sign-up', async () => {
    renderPage();
    await userEvent.click(screen.getByRole('button', { name: /sign up/i }));
    expect(screen.getByRole('heading')).toHaveTextContent('Create an account');
  });
});

describe('errorMessage', () => {
  it('falls back to a generic message', () => {
    expect(errorMessage(undefined)).toMatch(/something went wrong/i);
    expect(errorMessage({ error: { code: 'x', message: 'Nope.' } })).toBe('Nope.');
  });
});
