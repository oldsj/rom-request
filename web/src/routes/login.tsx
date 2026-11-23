import { useQueryClient } from '@tanstack/react-query';
import { useNavigate } from '@tanstack/react-router';
import { useState, type SubmitEvent } from 'react';

import { Button, Input, Notice } from '@app/design';

import { $api, errorMessage, meQuery } from '../api/client';
import { field } from '../api/form';

export function LoginPage() {
  const [mode, setMode] = useState<'login' | 'signup'>('login');
  const queryClient = useQueryClient();
  const navigate = useNavigate();
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
    </section>
  );
}
