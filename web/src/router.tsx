import type { QueryClient } from '@tanstack/react-query';
import {
  Outlet,
  createRootRouteWithContext,
  createRoute,
  createRouter,
  redirect,
} from '@tanstack/react-router';

import { meQuery } from './api/client';
import { AppLayout } from './routes/layout';
import { LoginPage } from './routes/login';
import { RequestsPage } from './routes/requests';
import { SearchPage } from './routes/search';

interface RouterContext {
  queryClient: QueryClient;
}

const rootRoute = createRootRouteWithContext<RouterContext>()({
  component: () => (
    <main className="mx-auto max-w-3xl px-4 py-10">
      <Outlet />
    </main>
  ),
});

// Signed-in pages: redirect to /login when there is no session. The server enforces this
// independently; this only spares the user a page of errors.
const signedInRoute = createRoute({
  getParentRoute: () => rootRoute,
  id: 'signed-in',
  beforeLoad: async ({ context }) => {
    const me = await context.queryClient.query({ ...meQuery, staleTime: 'static' });
    // eslint-disable-next-line @typescript-eslint/only-throw-error -- TanStack Router's redirect is thrown by design
    if (!me) throw redirect({ to: '/login' });
    return { me };
  },
  component: AppLayout,
});

const searchRoute = createRoute({
  getParentRoute: () => signedInRoute,
  path: '/',
  component: SearchPage,
});

const requestsRoute = createRoute({
  getParentRoute: () => signedInRoute,
  path: '/requests',
  component: RequestsPage,
});

const loginRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/login',
  component: LoginPage,
});

const routeTree = rootRoute.addChildren([signedInRoute.addChildren([searchRoute, requestsRoute]), loginRoute]);

export function createAppRouter(queryClient: QueryClient) {
  return createRouter({ routeTree, context: { queryClient }, defaultPreload: 'intent' });
}

declare module '@tanstack/react-router' {
  interface Register {
    router: ReturnType<typeof createAppRouter>;
  }
}
