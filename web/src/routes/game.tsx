import { Link } from '@tanstack/react-router';

import { Badge, Notice, Panel } from '@app/design';

import { $api, errorMessage, type CatalogGameDetails } from '../api/client';
import { RequestState, useCatalogRequests } from './catalog';

export interface GameSearch {
  platform: string;
}

/** Reads the page's URL search params; a game page needs its platform folder. */
export function validateGameSearch(s: Record<string, unknown>): GameSearch {
  return { platform: typeof s.platform === 'string' && s.platform.length <= 200 ? s.platform : '' };
}

/** YouTube ids from IGDB, checked again before they go into a link. */
const youtubeId = /^[A-Za-z0-9_-]{11}$/;

export function GamePage({ gameId, platform }: { gameId: string; platform: string }) {
  const id = Number(gameId);
  const valid = Number.isSafeInteger(id) && id > 0 && platform !== '';
  const game = $api.useQuery('get', '/catalog/games/{gameId}', { params: { path: { gameId: id }, query: { platform } } }, {
    enabled: valid,
    staleTime: 60 * 60_000,
  });
  const requests = useCatalogRequests(platform);

  return (
    <section>
      <Link to="/" search={{ platform }} className="text-muted-foreground mb-4 inline-block text-sm hover:underline">
        ← Back to {platform || 'Browse'}
      </Link>
      {!valid && <Notice tone="error">This link is missing its game or platform.</Notice>}
      {game.isError && <Notice tone="error">{errorMessage(game.error)}</Notice>}
      {game.isPending && valid && <p className="text-muted-foreground text-sm">Loading…</p>}
      {game.data && <Details game={game.data} platform={platform} request={requests.get(game.data.id)} />}
    </section>
  );
}

function Details({ game, platform, request }: { game: CatalogGameDetails; platform: string; request: Parameters<typeof RequestState>[0]['request'] }) {
  const videos = game.videos.filter((v) => youtubeId.test(v.youtubeId));
  return (
    <article className="flex flex-col gap-8">
      <div className="flex flex-col gap-6 sm:flex-row">
        {game.coverId ? (
          <img
            src={`/api/catalog/covers/${encodeURIComponent(game.coverId)}`}
            alt={`${game.name} cover`}
            width={264}
            height={374}
            className="bg-muted rounded-panel aspect-[264/374] w-44 shrink-0 self-start object-cover"
          />
        ) : (
          <div aria-hidden="true" className="bg-muted rounded-panel aspect-[264/374] w-44 shrink-0 self-start" />
        )}
        <div className="flex min-w-0 flex-1 flex-col gap-3">
          <h1 className="text-2xl font-semibold break-words">{game.name}</h1>
          <p className="text-muted-foreground text-sm">
            {[platform, game.year, game.developers.join(', ')].filter(Boolean).join(' · ')}
          </p>
          {(game.genres.length > 0 || game.rating !== undefined) && (
            <ul className="flex flex-wrap gap-2" aria-label="Genres">
              {game.genres.map((g) => <li key={g}><Badge>{g}</Badge></li>)}
              {game.rating !== undefined && <li><Badge tone="active">Rated {game.rating}/100</Badge></li>}
            </ul>
          )}
          <div className="max-w-56">
            <RequestState game={game} platform={platform} request={request} />
          </div>
          {game.summary && <p className="max-w-prose text-sm leading-relaxed whitespace-pre-line">{game.summary}</p>}
          {game.storyline && (
            <details className="max-w-prose text-sm">
              <summary className="cursor-pointer font-medium">Story</summary>
              <p className="text-muted-foreground mt-2 leading-relaxed whitespace-pre-line">{game.storyline}</p>
            </details>
          )}
        </div>
      </div>

      {game.screenshots.length > 0 && (
        <Panel heading="Screenshots">
          <ul className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3" aria-label="Screenshots">
            {game.screenshots.map((s, i) => (
              <li key={s}>
                <a href={`/api/catalog/screenshots/${encodeURIComponent(s)}`} target="_blank" rel="noopener">
                  <img
                    src={`/api/catalog/screenshots/${encodeURIComponent(s)}`}
                    alt={`${game.name} screenshot ${String(i + 1)}`}
                    loading="lazy"
                    width={889}
                    height={500}
                    className="bg-muted aspect-video w-full rounded object-cover"
                  />
                </a>
              </li>
            ))}
          </ul>
        </Panel>
      )}

      {videos.length > 0 && (
        <Panel heading="Videos" description="Trailers and gameplay, on YouTube.">
          <ul className="flex flex-col gap-2" aria-label="Videos">
            {videos.map((v) => (
              <li key={v.youtubeId}>
                <a
                  href={`https://www.youtube.com/watch?v=${v.youtubeId}`}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="text-sm underline"
                >
                  ▶ {v.name || 'Video'}
                </a>
              </li>
            ))}
          </ul>
        </Panel>
      )}
    </article>
  );
}
