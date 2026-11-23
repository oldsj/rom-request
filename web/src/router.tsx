import type { QueryClient } from '@tanstack/react-query';
import {
  Outlet,
  createRootRouteWithContext,
  createRoute,
  createRouter,
  redirect,
} from '@tanstack/react-router';

import { meQuery } from './api/client';
import { CatalogPage, validateCatalogSearch } from './routes/catalog';
import { GamePage, validateGameSearch } from './routes/game';
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
  path: '/search',
  beforeLoad: ({ context }) => {
    // A child's account can't search releases (the server answers 403); Browse instead.
    // eslint-disable-next-line @typescript-eslint/only-throw-error -- TanStack Router's redirect is thrown by design
    if (context.me.limited) throw redirect({ to: '/' });
  },
  // A game's name and the platform folder to request it into, from a link.
  validateSearch: (s: Record<string, unknown>): { q?: string | undefined; platform?: string | undefined } => ({
    q: typeof s.q === 'string' && s.q.length <= 200 ? s.q : undefined,
    platform: typeof s.platform === 'string' && s.platform.length <= 200 ? s.platform : undefined,
  }),
  component: function Search() {
    const { q, platform } = searchRoute.useSearch();
    // Remount for a new search from a link, so the field and results follow it.
    return <SearchPage key={`${q ?? ''}|${platform ?? ''}`} initialQuery={q} platform={platform} />;
  },
});

// Browse is the home page.
const catalogRoute = createRoute({
  getParentRoute: () => signedInRoute,
  path: '/',
  validateSearch: validateCatalogSearch,
  component: function Catalog() {
    return <CatalogPage search={catalogRoute.useSearch()} />;
  },
});

const gameRoute = createRoute({
  getParentRoute: () => signedInRoute,
  path: '/games/$gameId',
  validateSearch: validateGameSearch,
  component: function Game() {
    const { gameId } = gameRoute.useParams();
    return <GamePage key={gameId} gameId={gameId} platform={gameRoute.useSearch().platform} />;
  },
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

const routeTree = rootRoute.addChildren([signedInRoute.addChildren([searchRoute, catalogRoute, gameRoute, requestsRoute]), loginRoute]);

export function createAppRouter(queryClient: QueryClient) {
  return createRouter({ routeTree, context: { queryClient }, defaultPreload: 'intent' });
}

declare module '@tanstack/react-router' {
  interface Register {
    router: ReturnType<typeof createAppRouter>;
  }
}
