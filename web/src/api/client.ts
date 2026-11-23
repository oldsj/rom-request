// The only way the app talks to the server: a typed client generated from api/openapi.yaml.
// Run `make gen` after changing the contract; type errors then show every call site to fix.
import createFetchClient from 'openapi-fetch';
import createQueryClient from 'openapi-react-query';
import { queryOptions } from '@tanstack/react-query';

import type { components, paths } from './schema.gen';

export type Release = components['schemas']['Release'];
export type CatalogGame = components['schemas']['CatalogGame'];
export type CatalogGameDetails = components['schemas']['CatalogGameDetails'];
export type RomRequest = components['schemas']['Request'];
export type RequestStatus = components['schemas']['RequestStatus'];
export type User = components['schemas']['User'];
export type ApiError = components['schemas']['Error'];

// Same origin as the page. An absolute URL, because Node's Request (under Vitest) rejects relative
// ones. fetch is looked up per call, not captured once, so tests can stub it.
export const client = createFetchClient<paths>({
  baseUrl: new URL('/api', window.location.origin).href,
  fetch: (req) => globalThis.fetch(req),
});
export const $api = createQueryClient(client);

/** The user's message from an API error body, or a generic one. */
export function errorMessage(error: unknown): string {
  const e = error as Partial<ApiError> | undefined;
  return e?.error?.message ?? 'Something went wrong. Try again.';
}

/** The signed-in user, or null when signed out. Shared by the router guard and the UI. */
export const meQuery = queryOptions({
  queryKey: ['get', '/auth/me'],
  queryFn: async (): Promise<User | null> => {
    const { data, response } = await client.GET('/auth/me');
    if (response.status === 401) return null;
    if (!data) throw new Error(`GET /auth/me: ${String(response.status)}`);
    return data;
  },
  staleTime: 60_000,
});
