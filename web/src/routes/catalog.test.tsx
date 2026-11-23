import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';

const navigate = vi.fn();
vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => navigate,
  Link: ({ children, className }: { children?: React.ReactNode; className?: string }) => <a className={className}>{children}</a>,
}));
const { CatalogPage, validateCatalogSearch } = await import('./catalog');

const answers: Record<string, unknown> = {
  '/api/status': { prowlarr: true, qbittorrent: true, library: true, catalog: true },
  '/api/catalog/platforms': { items: [{ name: 'atari7800' }, { name: 'gba', catalogName: 'Game Boy Advance' }] },
  '/api/catalog/games': {
    items: [
      { id: 1, name: 'The Legend of Zelda: The Minish Cap', year: 2004, coverId: 'co1abc', inLibrary: true },
      { id: 2, name: 'Metroid Fusion', inLibrary: false },
      { id: 3, name: 'Pokémon FireRed Version', inLibrary: false },
    ],
  },
  '/api/catalog/requests': {
    items: [{
      id: 'r1', gameId: 3, gameName: 'Pokémon FireRed Version', title: 'Pokémon FireRed Version', indexer: '', sizeBytes: 0,
      infoUrl: '', platform: 'gba', license: '', sourceUrl: '', status: 'searching', progress: 0,
      createdAt: '2026-11-23T00:00:00Z', updatedAt: '2026-11-23T00:00:00Z',
    }],
  },
};

function stubApi() {
  const fetchMock = vi.fn().mockImplementation((req: Request) => {
    const body = answers[new URL(req.url).pathname];
    return Promise.resolve(new Response(JSON.stringify(body), { status: body ? 200 : 404, headers: { 'Content-Type': 'application/json' } }));
  });
  vi.stubGlobal('fetch', fetchMock);
  return fetchMock;
}

afterEach(() => {
  vi.unstubAllGlobals();
  navigate.mockReset();
});

describe('CatalogPage', () => {
  it('marks games in the library and requested, and requests the others', async () => {
    const fetchMock = stubApi();
    render(
      <QueryClientProvider client={new QueryClient()}>
        <CatalogPage search={{}} />
      </QueryClientProvider>,
    );

    expect(await screen.findByText('In library')).toBeInTheDocument();
    // The first platform IGDB knows is the default; one it doesn't is shown but can't be chosen.
    const games = fetchMock.mock.calls.map((c) => new URL((c[0] as Request).url)).find((u) => u.pathname === '/api/catalog/games');
    expect(games?.searchParams.get('platform')).toBe('gba');
    expect(games?.searchParams.get('offset')).toBe('0');
    expect(screen.getByRole('option', { name: 'atari7800: not in IGDB' })).toBeDisabled();
    expect(screen.getByRole('list', { name: 'Games' }).querySelector('img')?.getAttribute('src')).toBe('/api/catalog/covers/co1abc');

    expect(screen.getByText('Finding a release…')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Request Pokémon FireRed Version' })).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole('button', { name: 'Request Metroid Fusion' }));
    const post = fetchMock.mock.calls.map((c) => c[0] as Request).find((r) => r.method === 'POST');
    expect(new URL(post?.url ?? '').pathname).toBe('/api/catalog/requests');
    expect(await post?.json()).toEqual({ platform: 'gba', gameId: 2 });
  });
});

describe('validateCatalogSearch', () => {
  it('keeps only well-formed params', () => {
    expect(validateCatalogSearch({ platform: 'gba', q: 'zelda', sort: 'name' })).toEqual({ platform: 'gba', q: 'zelda', sort: 'name' });
    expect(validateCatalogSearch({ platform: 7, q: '', sort: 'random' })).toEqual({ platform: undefined, q: undefined, sort: undefined });
  });
});
