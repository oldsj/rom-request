# Security

## Reporting

Report vulnerabilities privately through GitHub's **Security → Report a vulnerability** on this
repository, not in a public issue.

## Defaults

| Area | Default | Where |
|------|---------|-------|
| Sessions | Random 256-bit token in `__Host-session` (HttpOnly, Secure, SameSite=Lax, Path=/); only its SHA-256 is stored, in Postgres; expires after `SESSION_TTL` (30 days); a new token on every sign-in; sign-out deletes it | `internal/server/session.go` |
| Passwords | argon2id (19 MiB, t=2), 12–128 characters; sign-in takes the same time whether or not the account exists | `internal/auth` |
| Sign-up | Off unless `ALLOW_SIGNUP=true` (and `PASSWORD_LOGIN` on) | `internal/config` |
| OIDC sign-in | Authorization code flow with PKCE (S256), state and nonce. The state is bound to the browser by a `__Host-oidc` cookie (HttpOnly, Secure, SameSite=Lax, 10 minutes) and to a server-side row holding a hash of it, deleted on use. The ID token's signature, issuer, audience, expiry and nonce are checked (go-oidc). Accounts are keyed by the provider's subject; an existing account is linked only by an email the provider marks verified. `OIDC_ALLOWED_GROUPS` admits only members. Issuer and redirect URL must be https (http only for localhost). Errors redirect to `/login?error=<code>` without detail | `internal/oidc`, `internal/server/oidc.go` |
| CSRF | Go's `http.CrossOriginProtection` refuses cross-origin unsafe requests (`Sec-Fetch-Site`/`Origin`), plus SameSite=Lax | `internal/server/server.go` |
| Brute force | Token bucket per client address on sign-in, sign-up and the OIDC endpoints (`AUTH_RATE_PER_MINUTE`, 10); `X-Forwarded-For` believed only from `TRUSTED_PROXIES` | `internal/httpx` |
| Browser | CSP `default-src 'self'`, no inline script or style, Trusted Types, `frame-ancestors 'none'`; HSTS, nosniff, `X-Frame-Options: DENY`, COOP/CORP same-origin, Referrer-Policy, Permissions-Policy | `internal/httpx/headers.go` |
| Input | Every request is validated against `api/openapi.yaml` (types, formats, lengths, no unknown fields) before a handler runs; 1 MiB body limit | `internal/server/server.go` |
| Server | Read-header 5 s, read 15 s, write 30 s, idle 60 s timeouts; 64 KiB headers; panics become a logged 500 | `internal/server/serve.go` |
| Errors and logs | Clients get a code and a safe message, never internals; logs never include headers, cookies or bodies | `internal/httpx` |
| SQL | sqlc only: parameterized queries, owner checked in every query | `internal/db/queries` |
| Database | TLS required; on the cluster, certificate and hostname verified (`verify-full` against the CNPG CA); `sslmode=disable` refused for anything but localhost | `internal/config`, chart |
| Secrets | Env only, no working defaults; Kubernetes Secrets from your secret store; `.env` is ignored by Git | `.env.example`, chart |
| Container | Distroless static, uid 65532, read-only root, all capabilities dropped, no privilege escalation, seccomp RuntimeDefault, no service account token | `Dockerfile`, chart |
| Network | NetworkPolicy: ingress only from the gateway's proxies, egress only to DNS, the database and the integrations you allow | chart |
| Integrations | Prowlarr's key and download links, and qBittorrent's credentials, stay on the server: search results reach the browser as opaque ids. Each Prowlarr result expires after an hour and is readable only by the user who searched | `internal/server/requests.go` |
| Files | Imports read only below `DOWNLOAD_DIR` and write only below `LIBRARY_DIR`, through `os.Root` (no `..`, no escaping symlinks); paths from torrents are checked; nothing in the library is ever replaced; copies are staged and moved into place last | `internal/library` |
| Requests | Every request query takes the owner; one live request per torrent; each worker step is a conditional update, so replicas never repeat one | `internal/db/queries/requests.sql` |
| Supply chain | Actions pinned by SHA with least `permissions`; images pinned by digest; pnpm refuses packages under 7 days old and runs no install scripts; Renovate waits 7 days too; govulncheck, gosec, pnpm audit, osv-scanner, CodeQL; image provenance and SBOM | `.github/`, `web/pnpm-workspace.yaml` |

## Known limits

- Rate limits are per replica, in memory. Behind several replicas, the effective limit multiplies.
- No account lockout, email verification, password reset or MFA. Add them before opening sign-up
  to strangers.
- No audit log of sign-ins.
- Signing out ends the ROM Request session only, not the one at the OIDC provider. Removing a
  user at the provider stops new sign-ins; their existing session lasts until `SESSION_TTL`.
- Every signed-in user can search the operator's indexers and add files to the library. Accounts
  are for people you trust with that.
- A request's license and release page are what the requester typed. ROM Request records them;
  it doesn't verify them.
- Archives are imported as they are, not extracted or inspected.
