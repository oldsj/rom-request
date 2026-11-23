import { useQueryClient } from '@tanstack/react-query';
import { useNavigate } from '@tanstack/react-router';
import { useState, type SubmitEvent } from 'react';

import { Button, Input, Notice } from '@app/design';

import { $api, errorMessage, meQuery } from '../api/client';
import { field } from '../api/form';

// Why an OIDC sign-in came back to /login (the callback's ?error=).
const oidcErrors: Record<string, string> = {
  oidc_denied: 'Sign-in was cancelled.',
  oidc_forbidden: 'Your account isn’t allowed to use this app. Ask the admin to add you to the right group.',
  oidc_conflict: 'That email already belongs to another account here.',
  oidc_no_email: 'Your identity provider didn’t share an email address.',
  oidc_state: 'That sign-in expired or was started in another browser. Try again.',
  oidc_invalid: 'The identity provider’s answer couldn’t be verified. Try again.',
  oidc_unavailable: 'The identity provider can’t be reached right now. Try again shortly.',
  oidc_off: 'Single sign-on isn’t set up on this server.',
};

export function oidcErrorMessage(search: string): string | undefined {
  const code = new URLSearchParams(search).get('error');
  if (!code) return undefined;
  return oidcErrors[code] ?? 'Sign-in didn’t work. Try again.';
}

export function LoginPage() {
  const [mode, setMode] = useState<'login' | 'signup'>('login');
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const { data: methods } = $api.useQuery('get', '/auth/methods');
  const oidcError = oidcErrorMessage(window.location.search);
  // Until the server says otherwise, offer the password form.
  const password = methods?.password ?? true;
  const signupAllowed = methods?.signup ?? true;
  const login = $api.useMutation('post', '/auth/login');
  const signup = $api.useMutation('post', '/auth/signup');
  const mutation = mode === 'login' ? login : signup;

  function onSubmit(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const form = new FormData(e.currentTarget);
    const body = { email: field(form, 'email'), password: field(form, 'password') };
    mutation.mutate(
      { body },
      {
        onSuccess: (user) => {
          queryClient.setQueryData(meQuery.queryKey, user);
          void navigate({ to: '/' });
        },
      },
    );
  }

  return (
    <section className="mx-auto max-w-sm">
      <h1 className="mb-6 text-2xl font-semibold">{mode === 'login' ? 'Sign in' : 'Create an account'}</h1>
      {oidcError && (
        <div className="mb-4">
          <Notice tone="error">{oidcError}</Notice>
        </div>
      )}
      {methods?.oidc && (
        <Button
          variant={password ? 'secondary' : 'primary'}
          fullWidth
          onClick={() => {
            // A full-page navigation: the server redirects on to the identity provider.
            window.location.assign('/api/auth/oidc/login');
          }}
        >
          Sign in with {methods.oidcName ?? 'single sign-on'}
        </Button>
      )}
      {methods?.oidc && password && <p className="text-muted-foreground my-4 text-center text-sm">or</p>}
      {password && (
      <form onSubmit={onSubmit} className="space-y-4">
        <Input label="Email" name="email" type="email" required autoComplete="email" />
        <Input
          label="Password"
          name="password"
          type="password"
          required
          minLength={12}
          maxLength={128}
          autoComplete={mode === 'login' ? 'current-password' : 'new-password'}
        />
        {mutation.isError && <Notice tone="error">{errorMessage(mutation.error)}</Notice>}
        <Button type="submit" variant="primary" fullWidth disabled={mutation.isPending}>
          {mode === 'login' ? 'Sign in' : 'Sign up'}
        </Button>
      </form>
      )}
      {password && (signupAllowed || mode === 'signup') && (
      <button
        type="button"
        className="focus-visible:outline-ring mt-4 min-h-11 text-sm underline focus-visible:outline-2"
        onClick={() => {
          login.reset();
          signup.reset();
          setMode(mode === 'login' ? 'signup' : 'login');
        }}
      >
        {mode === 'login' ? 'Need an account? Sign up' : 'Have an account? Sign in'}
      </button>
      )}
    </section>
  );
}
