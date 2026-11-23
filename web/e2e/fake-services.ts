// Stand-ins for Prowlarr and qBittorrent, so the e2e test can follow a request from search to the
// library against the real binary. One port serves both APIs (their paths don't overlap). A
// torrent "downloads" the moment it is added: its file is written into the save path, which the
// e2e config makes the same folder as the app's DOWNLOAD_DIR.
import { mkdirSync, writeFileSync } from 'node:fs';
import { createServer } from 'node:http';
import { join } from 'node:path';

const port = Number(process.env.FAKE_PORT ?? '18090');
const apiKey = process.env.FAKE_PROWLARR_KEY ?? '';
const hash = '0123456789abcdef0123456789abcdef01234567';
const romBytes = 'e2e homebrew rom';

const torrents = new Map<string, { savePath: string }>();

const releases = [
  {
    guid: 'e2e-1',
    title: 'Homebrew Quest',
    indexer: 'Homebrew Hub',
    size: romBytes.length,
    seeders: 12,
    protocol: 'torrent',
    infoUrl: 'https://example.com/homebrew-quest',
    magnetUrl: `magnet:?xt=urn:btih:${hash}&dn=Homebrew+Quest`,
    categories: [{ id: 1000, name: 'Console' }],
  },
];

function form(body: string, contentType: string): Map<string, string> {
  const out = new Map<string, string>();
  if (contentType.startsWith('multipart/form-data')) {
    const re = /name="([^"]+)"\r\n\r\n([^\r]*)\r\n/g;
    for (const m of body.matchAll(re)) out.set(m[1], m[2]);
  } else {
    for (const [k, v] of new URLSearchParams(body)) out.set(k, v);
  }
  return out;
}

const server = createServer((req, res) => {
  let body = '';
  req.on('data', (c: Buffer) => (body += c.toString()));
  req.on('end', () => {
    const url = new URL(req.url ?? '/', `http://localhost:${String(port)}`);
    const json = (v: unknown) => {
      res.setHeader('Content-Type', 'application/json');
      res.end(JSON.stringify(v));
    };
    const f = form(body, req.headers['content-type'] ?? '');

    if (url.pathname === '/api/v1/search') {
      if (req.headers['x-api-key'] !== apiKey) {
        res.statusCode = 401;
        res.end();
        return;
      }
      json(releases);
      return;
    }
    if (url.pathname === '/api/v2/auth/login') {
      res.end('Ok.'); // qBittorrent with authentication bypassed for this client
      return;
    }
    if (url.pathname === '/api/v2/torrents/add') {
      const savePath = f.get('savepath') ?? '';
      if (!f.get('urls')?.includes(hash) || torrents.has(hash)) {
        res.end('Fails.');
        return;
      }
      mkdirSync(savePath, { recursive: true });
      writeFileSync(join(savePath, 'Homebrew Quest.gba'), romBytes);
      torrents.set(hash, { savePath });
      res.end('Ok.');
      return;
    }
    if (url.pathname === '/api/v2/torrents/info') {
      const t = torrents.get(url.searchParams.get('hashes') ?? '');
      json(t ? [{ hash, name: 'Homebrew Quest.gba', state: 'stalledUP', progress: 1, save_path: t.savePath, size: romBytes.length }] : []);
      return;
    }
    if (url.pathname === '/api/v2/torrents/files') {
      json([{ name: 'Homebrew Quest.gba', size: romBytes.length, progress: 1, priority: 1 }]);
      return;
    }
    if (url.pathname === '/api/v2/torrents/delete') {
      torrents.delete(f.get('hashes') ?? '');
      res.end();
      return;
    }
    if (url.pathname === '/healthz') {
      res.end('ok');
      return;
    }
    res.statusCode = 404;
    res.end();
  });
});

server.listen(port, '127.0.0.1');
