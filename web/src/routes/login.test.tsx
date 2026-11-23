import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { errorMessage } from '../api/client';

// The router is mocked: these tests cover the form, not navigation.
vi.mock('@tanstack/react-router', () => ({ useNavigate: () => vi.fn() }));
const { LoginPage, oidcErrorMessage } = await import('./login');

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
    const fetchMock = vi.fn().mockImplementation(() =>
      Promise.resolve(
        new Response(JSON.stringify({ error: { code: 'invalid_credentials', message: 'Wrong email or password.' } }), {
          status: 401,
          headers: { 'Content-Type': 'application/json' },
        }),
      ),
    );
    vi.stubGlobal('fetch', fetchMock);
    renderPage();

    await userEvent.type(screen.getByLabelText('Email'), 'ada@example.com');
    await userEvent.type(screen.getByLabelText('Password'), 'a long enough password');
    await userEvent.click(screen.getByRole('button', { name: 'Sign in' }));

    expect(await screen.findByRole('alert')).toHaveTextContent('Wrong email or password.');
    const requests = fetchMock.mock.calls.map((c) => c[0] as Request);
    const login = requests.find((r) => new URL(r.url).pathname === '/api/auth/login');
    expect(login?.method).toBe('POST');
  });

  it('switches to sign-up', async () => {
    renderPage();
    await userEvent.click(screen.getByRole('button', { name: /sign up/i }));
    expect(screen.getByRole('heading')).toHaveTextContent('Create an account');
  });
});

function stubMethods(methods: object) {
  vi.stubGlobal(
    'fetch',
    vi.fn().mockImplementation(() =>
      Promise.resolve(new Response(JSON.stringify(methods), { status: 200, headers: { 'Content-Type': 'application/json' } })),
    ),
  );
}

describe('LoginPage with OIDC', () => {
  it('offers only single sign-on when password sign-in is off', async () => {
    stubMethods({ password: false, signup: false, oidc: true, oidcName: 'Pocket ID' });
    renderPage();
    expect(await screen.findByRole('button', { name: 'Sign in with Pocket ID' })).toBeInTheDocument();
    expect(screen.queryByLabelText('Email')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /sign up/i })).not.toBeInTheDocument();
  });

  it('offers both, without sign-up when it is off', async () => {
    stubMethods({ password: true, signup: false, oidc: true, oidcName: 'Pocket ID' });
    renderPage();
    expect(await screen.findByRole('button', { name: 'Sign in with Pocket ID' })).toBeInTheDocument();
    expect(screen.getByLabelText('Email')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /sign up/i })).not.toBeInTheDocument();
  });
});

describe('oidcErrorMessage', () => {
  it('explains the callback error codes', () => {
    expect(oidcErrorMessage('')).toBeUndefined();
    expect(oidcErrorMessage('?error=oidc_forbidden')).toMatch(/group/);
    expect(oidcErrorMessage('?error=something_new')).toMatch(/try again/i);
  });
});

describe('errorMessage', () => {
  it('falls back to a generic message', () => {
    expect(errorMessage(undefined)).toMatch(/something went wrong/i);
    expect(errorMessage({ error: { code: 'x', message: 'Nope.' } })).toBe('Nope.');
  });
});
