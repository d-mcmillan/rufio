# Rufio hosted mode

Two agents on different infrastructures coordinate securely through a hosted Rufio daemon, with a file-native local mirror preserved on every client.

This page is the operational guide. For the CLI surface, see [cli-reference.md](./cli-reference.md). For the MCP tool surface, see [mcp.md](./mcp.md).

## Threat model

Rufio v1.0.x ships **trusted-collaborator** auth: bearer tokens minted by a local operator, identity resolved server-side. This is sufficient for "one team runs the server, others bring tokens" topologies (Slack-trust).

PKI / cryptographic identity / federation is the **v1.2 frontier**. Hosted mode is honestly labeled "Research Preview" — production deployments outside trusted-collaborator settings should wait for v1.2.

## Server setup

### 1. Provision substrate

```bash
mkdir /var/lib/rufio
cd /var/lib/rufio
rufio init my-org
```

### 2. TLS certificate

For production, use a CA-signed cert (Let's Encrypt, internal CA). For dev, self-signed works:

```bash
openssl req -x509 -newkey rsa:2048 \
  -keyout key.pem -out cert.pem \
  -days 365 -nodes -subj "/CN=rufio.example.com"
```

Clients with self-signed certs need `--insecure-tls` on every CLI call (loud stderr warning).

### 3. Mint bearer tokens

One token per agent. The plaintext is shown EXACTLY ONCE — capture it:

```bash
TOKEN_ALICE=$(rufio admin token mint --agent=alice | grep ^token= | cut -d= -f2)
echo "$TOKEN_ALICE" > /etc/rufio/alice.token   # protect with 0600
```

Tokens stored on disk are SHA-256 hashed in `.rufio/.admin/tokens.gdl` (0600 perms). The plaintext NEVER touches disk.

### 4. Start serve

```bash
rufio serve --port=8443 \
    --tls-cert=/etc/rufio/cert.pem \
    --tls-key=/etc/rufio/key.pem
```

For systemd:

```ini
[Service]
ExecStart=/usr/local/bin/rufio serve --port=8443 \
    --tls-cert=/etc/rufio/cert.pem \
    --tls-key=/etc/rufio/key.pem
WorkingDirectory=/var/lib/rufio
Restart=on-failure
```

TLS is mandatory. The only exception is localhost dev: `--insecure --bind=127.0.0.1` is honoured with a loud stderr warning. Any other `--insecure` configuration is rejected with exit 2.

## Client usage

### Cognition verbs through the remote

Set `RUFIO_SERVER` and `RUFIO_TOKEN` env vars, or pass `--server` + `--token`:

```bash
export RUFIO_SERVER=https://rufio.example.com:8443
export RUFIO_TOKEN="$(cat ~/.rufio/alice.token)"

rufio attend --intent="testing" --entities=test:1
rufio think --type=hypothesis --subject=test:1 --content="alpha" --scope=fleet
rufio recall --topics=alpha
rufio confirm <thought-id>
```

Identity is server-authoritative: the token resolves to an agent at the server; the client cannot override via `RUFIO_AGENT_ID` or any other flag.

### File-native local mirror

The mirror keeps a read-only file shadow of the remote substrate. Writes always go through the server (single canonical store).

**Snapshot (one-shot):**

```bash
rufio mirror pull --to=./mirror
```

**Continuous (default):**

```bash
rufio mirror sync --to=./mirror &
```

The continuous mode opens an SSE stream to `/listen`, writes incoming events atomically (.tmp + rename), persists a cursor at `./mirror/.rufio/.mirror-cursor`, and reconnects with exponential backoff (1s, 2s, 4s, max 30s) on disconnect.

The mirror layout is byte-identical to a local substrate:

```
./mirror/
  live/outbox/<author>/<id>.gdl
  live/reasoning/<id>.gdl
  live/confirms/<id>.gdl
  given/<content-path>
  learned/<content-path>
```

### JSONL interop

```bash
rufio export --format=jsonl > backup.jsonl
cat backup.jsonl | rufio import --format=jsonl   # round-trip into a fresh substrate
```

JSONL is import/export only. The GDL-on-disk manifesto stays — JSONL is sugar for pipelines that don't speak GDL (jq, pandas, BigQuery import).

## Privacy floor

The privacy floor (`scope=agent` records are NEVER visible to other agents) is enforced **server-side** before bytes leave. Every read path goes through `privacy.IsVisible` with the bearer-token's resolved identity:

- `recall`, `open`, `listen`, `goals_list` — server-filtered before response
- mirror pull / sync — server emits only records the requesting agent can see
- JSONL export — same server-side filter

The client cannot bypass this by sending a different `Authorization` header or any other flag.

## Token management

```bash
rufio admin token list           # show all minted tokens
rufio admin token revoke <id>    # mark a token as revoked (idempotent)
```

Revoked tokens are rejected by the server on subsequent calls. The record stays in `tokens.gdl` with `revoked:true` so audits can see the lifecycle.

**Lost a token?** Revoke it (`rufio admin token revoke tok-...`) and mint a new one. Plaintext is never recoverable from the on-disk hash.

## Operational notes

- The server logs the resolved agent identity + token ID on every authenticated request. Plaintext tokens NEVER appear in logs.
- `/health` (no auth) is the load-balancer probe.
- Mirror clients tolerate brief server downtime via the SSE reconnect-with-backoff loop.
- Atomic writes (.tmp + rename) mean a kill -9 during mirror sync can leave .tmp files but never corrupted .gdl records.

## See also

- [cli-reference.md](./cli-reference.md) — flag-level CLI reference
- [mcp.md](./mcp.md) — MCP tool surface (21 tools)
- [release-history/v1.0.4.md](./release-history/v1.0.4.md) — what shipped + threat-model framing
