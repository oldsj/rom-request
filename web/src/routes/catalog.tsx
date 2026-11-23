import { useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate } from '@tanstack/react-router';
import type { SubmitEvent } from 'react';

import { Badge, Button, Input, Notice, Progress, Select } from '@app/design';

import { $api, errorMessage, type CatalogGame, type RomRequest } from '../api/client';
import { isActive } from '../format';
import { field } from '../api/form';

export interface CatalogSearch {
  platform?: string | undefined;
  q?: string | undefined;
  sort?: 'popular' | 'name' | undefined;
}

/** Reads the page's URL search params, dropping anything malformed. */
export function validateCatalogSearch(s: Record<string, unknown>): CatalogSearch {
  const str = (v: unknown, max: number) => (typeof v === 'string' && v.length > 0 && v.length <= max ? v : undefined);
  return {
    platform: str(s.platform, 200),
    q: str(s.q, 100),
    sort: s.sort === 'name' ? 'name' : undefined,
  };
}

export function CatalogPage({ search }: { search: CatalogSearch }) {
  const navigate = useNavigate();
  const status = $api.useQuery('get', '/status');
  const platforms = $api.useQuery('get', '/catalog/platforms', {}, { enabled: status.data?.catalog === true, staleTime: 10 * 60_000 });
  const browsable = platforms.data?.items.filter((p) => p.catalogName) ?? [];
  const platform = search.platform ?? browsable[0]?.name;

  function onSubmit(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const d = new FormData(e.currentTarget);
    const q = field(d, 'q').trim();
    void navigate({
      to: '/',
      search: { platform: field(d, 'platform'), ...(q ? { q } : {}), ...(field(d, 'sort') === 'name' ? { sort: 'name' as const } : {}) },
    });
  }

  return (
    <section>
      <h1 className="mb-2 text-2xl font-semibold">Browse games</h1>
      <p className="text-muted-foreground mb-6 text-sm">
        Every game IGDB lists for a platform in your library. Request one and ROM Request finds a
        release, downloads it and adds it to the library.
      </p>

      {status.data && !status.data.catalog && (
        <Notice>
          The catalog isn't set up on this server yet: it needs IGDB credentials and the ROM
          library. <Link to="/search" className="underline">Search for releases</Link> instead.
        </Notice>
      )}
      {platforms.isError && <Notice tone="error">{errorMessage(platforms.error)}</Notice>}
      {platforms.data && browsable.length === 0 && (
        <Notice>None of the library's platform folders match an IGDB platform. Map them with CATALOG_PLATFORMS.</Notice>
      )}

      {platform && (
        <form
          // Remount on navigation so the fields show the URL's values.
          key={`${platform}|${search.q ?? ''}|${search.sort ?? ''}`}
          onSubmit={onSubmit}
          role="search"
          aria-label="Catalog"
          className="mb-8 grid gap-2 sm:grid-cols-[1fr_1fr_auto_auto] sm:items-end"
        >
          <Select label="Platform" name="platform" defaultValue={platform}>
            {platforms.data?.items.map((p) => (
              <option key={p.name} value={p.name} disabled={!p.catalogName}>
                {p.catalogName ? `${p.catalogName} (${p.name})` : `${p.name}: not in IGDB`}
              </option>
            ))}
          </Select>
          <Input label="Name" name="q" maxLength={100} defaultValue={search.q} />
          <Select label="Order" name="sort" defaultValue={search.sort ?? 'popular'}>
            <option value="popular">Popular</option>
            <option value="name">A–Z</option>
          </Select>
          <Button type="submit" variant="primary">Browse</Button>
        </form>
      )}

      {platform && <Games platform={platform} q={search.q} sort={search.sort} />}
    </section>
  );
}

function Games({ platform, q, sort }: { platform: string; q?: string | undefined; sort?: 'popular' | 'name' | undefined }) {
  const games = $api.useInfiniteQuery(
    'get',
    '/catalog/games',
    { params: { query: { platform, ...(q ? { q } : {}), ...(sort ? { sort } : {}) } } },
    {
      pageParamName: 'offset',
      initialPageParam: 0,
      getNextPageParam: (last) => last.nextOffset,
      staleTime: 10 * 60_000,
    },
  );
  const items = games.data?.pages.flatMap((p) => p.items) ?? [];
  const requests = $api.useQuery('get', '/catalog/requests', { params: { query: { platform } } }, {
    // Follow requests while any is still moving.
    refetchInterval: (q) => (q.state.data?.items.some((r) => isActive(r.status)) ? 5000 : false),
  });
  const byGame = new Map((requests.data?.items ?? []).map((r) => [r.gameId, r]));

  if (games.isPending) return <Notice>Loading…</Notice>;
  // A failed first page; a failed later page shows under the list instead.
  if (!games.data) return <Notice tone="error">{errorMessage(games.error)}</Notice>;
  if (items.length === 0) return <Notice>No games found{q ? ` for “${q}”` : ''}.</Notice>;
  return (
    <>
      <ul aria-label="Games" className="grid grid-cols-2 gap-4 sm:grid-cols-3 md:grid-cols-4">
        {items.map((g) => <GameCard key={g.id} game={g} platform={platform} request={byGame.get(g.id)} />)}
      </ul>
      {games.hasNextPage && (
        <div className="mt-6 flex justify-center">
          <Button onClick={() => { void games.fetchNextPage(); }} disabled={games.isFetchingNextPage}>
            {games.isFetchingNextPage ? 'Loading…' : 'Load more'}
          </Button>
        </div>
      )}
      {games.isFetchNextPageError && <Notice tone="error" className="mt-4">{errorMessage(games.error)}</Notice>}
    </>
  );
}

function GameCard({ game, platform, request }: { game: CatalogGame; platform: string; request?: RomRequest | undefined }) {
  return (
    <li className="bg-surface border-border rounded-panel flex flex-col overflow-hidden border">
      {game.coverId ? (
        <img
          src={`/api/catalog/covers/${encodeURIComponent(game.coverId)}`}
          alt=""
          loading="lazy"
          width={264}
          height={374}
          className="bg-muted aspect-[264/374] w-full object-cover"
        />
      ) : (
        <div aria-hidden="true" className="bg-muted aspect-[264/374] w-full" />
      )}
      <div className="flex flex-1 flex-col gap-2 p-3">
        <h2 className="text-sm leading-snug font-medium break-words" title={game.summary}>{game.name}</h2>
        {game.year !== undefined && <p className="text-muted-foreground text-xs">{game.year}</p>}
        <div className="mt-auto">
          <RequestState game={game} platform={platform} request={request} />
        </div>
      </div>
    </li>
  );
}

/** A game's state: in the library, its request's progress, or a button to request it. */
function RequestState({ game, platform, request: r }: { game: CatalogGame; platform: string; request?: RomRequest | undefined }) {
  const queryClient = useQueryClient();
  const refresh = () => queryClient.invalidateQueries({ queryKey: ['get', '/catalog/requests'] });
  const create = $api.useMutation('post', '/catalog/requests', { onSettled: refresh });
  const retry = $api.useMutation('post', '/requests/{requestId}/retry', { onSettled: refresh });
  const error = create.error ?? retry.error;

  if (game.inLibrary || r?.status === 'imported') return <Badge tone="success">In library</Badge>;
  if (r && r.status !== 'failed') {
    if (r.status === 'downloading') return <Progress label="Downloading" value={r.progress} />;
    return <Badge tone="active">{r.status === 'searching' ? 'Finding a release…' : r.status === 'importing' ? 'Adding to library…' : 'Queued'}</Badge>;
  }
  return (
    <div className="flex flex-col gap-2">
      {r?.error && <p className="text-muted-foreground text-xs" title={r.error.message}>{r.error.code === 'no_release' ? 'No release found.' : 'The request failed.'}</p>}
      {error && <p role="alert" className="text-danger text-xs">{errorMessage(error)}</p>}
      <Button
        fullWidth
        variant={r ? 'secondary' : 'primary'}
        disabled={create.isPending || retry.isPending}
        aria-label={`${r ? 'Retry' : 'Request'} ${game.name}`}
        onClick={() => {
          if (r) retry.mutate({ params: { path: { requestId: r.id } } });
          else create.mutate({ body: { platform, gameId: game.id } });
        }}
      >
        {r ? 'Retry' : 'Request'}
      </Button>
    </div>
  );
}
