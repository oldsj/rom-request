import { useNavigate } from '@tanstack/react-router';
import { useState, type SubmitEvent } from 'react';

import { Button, Input, Notice, Panel, Select } from '@app/design';

import { $api, errorMessage, type Release } from '../api/client';
import { field } from '../api/form';
import { formatBytes, formatDate } from '../format';

export function SearchPage() {
  const [query, setQuery] = useState('');
  const status = $api.useQuery('get', '/status');
  const results = $api.useQuery(
    'get',
    '/search',
    { params: { query: { q: query } } },
    { enabled: query.length >= 2 && status.data?.prowlarr === true, staleTime: 5 * 60_000 },
  );

  function onSearch(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const q = field(new FormData(e.currentTarget), 'q').trim();
    // The same query again (say, after an error or an expired result) searches again.
    if (q === query) void results.refetch();
    else setQuery(q);
  }

  const canRequest = status.data?.qbittorrent === true;

  return (
    <section>
      <h1 className="mb-2 text-2xl font-semibold">Search releases</h1>
      <p className="text-muted-foreground mb-6 text-sm">
        Results come from your Prowlarr indexers. A listing isn't a license: check the release page
        before you request a game.
      </p>

      {status.isError && <Notice tone="error">{errorMessage(status.error)}</Notice>}
      {status.data && !status.data.prowlarr && (
        <Notice>Search isn't set up on this server yet: it needs Prowlarr.</Notice>
      )}

      {status.data?.prowlarr && (
        <form onSubmit={onSearch} role="search" aria-label="Releases" className="mb-8 flex items-end gap-2">
          <Input label="Game or release" name="q" required minLength={2} maxLength={200} className="flex-1" />
          <Button type="submit" variant="primary">Search</Button>
        </form>
      )}
      {status.data?.prowlarr && !canRequest && (
        <Notice className="mb-4">Requesting isn't set up on this server yet: it needs qBittorrent and the ROM library.</Notice>
      )}

      {results.isFetching && <Notice>Searching…</Notice>}
      {results.isError && <Notice tone="error">{errorMessage(results.error)}</Notice>}
      {results.data?.items.length === 0 && <Notice>No torrent releases found for “{query}”.</Notice>}
      <ul className="space-y-3" aria-label="Results">
        {results.data?.items.map((r) => <ReleaseItem key={r.id} release={r} canRequest={canRequest} />)}
      </ul>
    </section>
  );
}

function ReleaseItem({ release, canRequest }: { release: Release; canRequest: boolean }) {
  const [open, setOpen] = useState(false);
  const meta = [
    release.indexer,
    formatBytes(release.sizeBytes),
    release.seeders === undefined ? undefined : `${String(release.seeders)} seeders`,
    release.publishedAt ? formatDate(release.publishedAt) : undefined,
  ].filter(Boolean);

  return (
    <Panel as="li">
      <h2 className="font-medium break-words">{release.title}</h2>
      <p className="text-muted-foreground mt-1 text-sm">{meta.join(' · ')}</p>
      {release.infoUrl && (
        <p className="mt-1 text-sm">
          <a href={release.infoUrl} target="_blank" rel="noopener noreferrer" className="underline">
            Indexer page
          </a>
        </p>
      )}
      {canRequest && !open && (
        <Button className="mt-3" onClick={() => { setOpen(true); }}>Request</Button>
      )}
      {open && <RequestForm release={release} onCancel={() => { setOpen(false); }} />}
    </Panel>
  );
}

function RequestForm({ release, onCancel }: { release: Release; onCancel: () => void }) {
  const navigate = useNavigate();
  const platforms = $api.useQuery('get', '/platforms');
  const create = $api.useMutation('post', '/requests', {
    onSuccess: () => { void navigate({ to: '/requests' }); },
  });

  function onSubmit(e: SubmitEvent<HTMLFormElement>) {
    e.preventDefault();
    const d = new FormData(e.currentTarget);
    create.mutate({
      body: {
        releaseId: release.id,
        platform: field(d, 'platform'),
        license: field(d, 'license').trim(),
        sourceUrl: field(d, 'sourceUrl').trim(),
      },
    });
  }

  return (
    <form onSubmit={onSubmit} aria-label={`Request ${release.title}`} className="mt-4 space-y-3">
      {platforms.isError && <Notice tone="error">{errorMessage(platforms.error)}</Notice>}
      <Select label="Platform" name="platform" required defaultValue="" hint="The library folder it goes into.">
        <option value="" disabled>{platforms.isPending ? 'Loading…' : 'Choose a platform'}</option>
        {platforms.data?.items.map((p) => <option key={p.name} value={p.name}>{p.name}</option>)}
      </Select>
      <Input
        label="License"
        name="license"
        maxLength={200}
        hint="As the release states it, such as CC BY 4.0. Leave empty if you don't know."
      />
      <Input
        label="Release page"
        name="sourceUrl"
        type="url"
        maxLength={2000}
        pattern="https?://\S+"
        hint="The creator's or publisher's page for this release."
      />
      {create.isError && <Notice tone="error">{errorMessage(create.error)}</Notice>}
      <div className="flex gap-2">
        <Button type="submit" variant="primary" disabled={create.isPending}>Request</Button>
        <Button onClick={onCancel}>Cancel</Button>
      </div>
    </form>
  );
}
