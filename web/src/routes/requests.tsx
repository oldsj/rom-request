import { useQueryClient } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { useState } from 'react';

import { Badge, Button, Notice, Panel, Progress } from '@app/design';

import { $api, errorMessage, type RomRequest } from '../api/client';
import { formatBytes, formatDate, isActive, statusLabel, statusTone } from '../format';

const listKey = ['get', '/requests'] as const;

export function RequestsPage() {
  const queryClient = useQueryClient();
  const requests = $api.useQuery('get', '/requests', {}, {
    // Follow downloads while any request is still moving.
    refetchInterval: (q) => (q.state.data?.items.some((r) => isActive(r.status)) ? 5000 : false),
  });
  const invalidate = () => queryClient.invalidateQueries({ queryKey: listKey });

  return (
    <section>
      <h1 className="mb-6 text-2xl font-semibold">Requests</h1>
      {requests.isPending && <Notice>Loading…</Notice>}
      {requests.isError && <Notice tone="error">{errorMessage(requests.error)}</Notice>}
      {requests.data?.items.length === 0 && (
        <Notice>
          No requests yet. <Link to="/" className="underline">Search for a release</Link>.
        </Notice>
      )}
      <ul className="space-y-3" aria-label="Requests">
        {requests.data?.items.map((r) => <RequestItem key={r.id} request={r} onChanged={invalidate} />)}
      </ul>
    </section>
  );
}

function RequestItem({ request: r, onChanged }: { request: RomRequest; onChanged: () => Promise<void> }) {
  const [confirming, setConfirming] = useState(false);
  const params = { path: { requestId: r.id } };
  const retry = $api.useMutation('post', '/requests/{requestId}/retry', { onSuccess: onChanged });
  const remove = $api.useMutation('delete', '/requests/{requestId}', { onSuccess: onChanged });
  const active = isActive(r.status);
  const removeLabel = r.status === 'imported' || r.status === 'failed' ? 'Remove' : 'Cancel';

  return (
    <Panel as="li">
      <div className="flex items-start justify-between gap-3">
        <h2 className="font-medium break-words">{r.title}</h2>
        <Badge tone={statusTone[r.status]}>{statusLabel[r.status]}</Badge>
      </div>
      <p className="text-muted-foreground mt-1 text-sm">
        {[r.platform, formatBytes(r.sizeBytes), r.indexer, `requested ${formatDate(r.createdAt)}`].join(' · ')}
      </p>

      {r.status === 'downloading' && <Progress label="Downloading" value={r.progress} className="mt-3" />}
      {r.status === 'queued' && <p className="text-muted-foreground mt-3 text-sm">Waiting for qBittorrent.</p>}
      {r.status === 'importing' && <p className="text-muted-foreground mt-3 text-sm">Copying into the library…</p>}
      {r.status === 'imported' && r.importedPath && (
        <p className="mt-3 text-sm">
          In the library as <code className="break-all">{r.importedPath}</code>. RomM lists it after its next scan.
        </p>
      )}
      {r.status === 'failed' && r.error && <Notice tone="error" className="mt-3">{r.error.message}</Notice>}

      <dl className="mt-3 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-sm">
        <dt className="text-muted-foreground">License</dt>
        <dd>{r.license || 'Unknown'}</dd>
        <dt className="text-muted-foreground">Release page</dt>
        <dd className="break-all">
          {r.sourceUrl ? (
            <a href={r.sourceUrl} target="_blank" rel="noopener noreferrer" className="underline">{r.sourceUrl}</a>
          ) : (
            'Not recorded'
          )}
        </dd>
        {r.importedSha256 && (
          <>
            <dt className="text-muted-foreground">SHA-256</dt>
            <dd className="font-mono text-xs break-all">{r.importedSha256}</dd>
          </>
        )}
      </dl>

      {(retry.isError || remove.isError) && (
        <Notice tone="error" className="mt-3">{errorMessage(retry.error ?? remove.error)}</Notice>
      )}
      {r.status !== 'importing' && (
        <div className="mt-3 flex flex-wrap gap-2">
          {r.status === 'failed' && (
            <Button variant="primary" disabled={retry.isPending} onClick={() => { remove.reset(); retry.mutate({ params }); }}>
              Retry
            </Button>
          )}
          {confirming ? (
            <>
              <Button variant="danger" disabled={remove.isPending} onClick={() => { retry.reset(); remove.mutate({ params }); }}>
                {active ? 'Cancel download' : 'Remove request'}
              </Button>
              <Button onClick={() => { setConfirming(false); }}>Keep</Button>
            </>
          ) : (
            <Button onClick={() => { setConfirming(true); }}>{removeLabel}</Button>
          )}
        </div>
      )}
      {confirming && (
        <p className="text-muted-foreground mt-2 text-sm">
          {r.status === 'imported'
            ? 'This removes the request only. The game stays in the library, and the torrent keeps seeding.'
            : 'This removes the request, and its torrent and downloaded files from qBittorrent.'}
        </p>
      )}
    </Panel>
  );
}
