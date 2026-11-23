import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => vi.fn(),
  Link: ({ children }: { children?: React.ReactNode }) => <a>{children}</a>,
}));
const { GamePage, validateGameSearch } = await import('./game');

const answers: Record<string, unknown> = {
  '/api/catalog/games/2': {
    id: 2, name: 'Metroid Fusion', year: 2002, summary: 'Samus fights the X.', rating: 91, inLibrary: false,
    genres: ['Shooter', 'Platform'], developers: ['Nintendo R&D1'], screenshots: ['sc1abc', 'sc2def'],
    videos: [{ name: 'Trailer', youtubeId: 'dQw4w9WgXcQ' }, { name: 'Bad', youtubeId: 'javascript:x' }],
  },
  '/api/catalog/requests': { items: [] },
};

afterEach(() => { vi.unstubAllGlobals(); });

describe('GamePage', () => {
  it('shows the description, screenshots and videos, and can request the game', async () => {
    vi.stubGlobal('fetch', vi.fn().mockImplementation((req: Request) => {
      const body = answers[new URL(req.url).pathname];
      return Promise.resolve(new Response(JSON.stringify(body), { status: body ? 200 : 404, headers: { 'Content-Type': 'application/json' } }));
    }));
    render(
      <QueryClientProvider client={new QueryClient()}>
        <GamePage gameId="2" platform="gba" />
      </QueryClientProvider>,
    );

    expect(await screen.findByRole('heading', { name: 'Metroid Fusion' })).toBeInTheDocument();
    expect(screen.getByText('Samus fights the X.')).toBeInTheDocument();
    expect(screen.getByText('gba · 2002 · Nintendo R&D1')).toBeInTheDocument();
    expect(screen.getByText('Rated 91/100')).toBeInTheDocument();
    expect(screen.getByRole('img', { name: 'Metroid Fusion screenshot 2' })).toHaveAttribute('src', '/api/catalog/screenshots/sc2def');
    // Only a well-formed YouTube id becomes a link.
    const videos = screen.getByRole('list', { name: 'Videos' }).querySelectorAll('a');
    expect(videos).toHaveLength(1);
    expect(videos[0]).toHaveAttribute('href', 'https://www.youtube.com/watch?v=dQw4w9WgXcQ');
    expect(await screen.findByRole('button', { name: 'Request Metroid Fusion' })).toBeInTheDocument();
  });

  it('needs a platform', () => {
    expect(validateGameSearch({ platform: 'gba' })).toEqual({ platform: 'gba' });
    expect(validateGameSearch({})).toEqual({ platform: '' });
  });
});
