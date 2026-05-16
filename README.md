# phantom-node

`phantom-node` is a small Go daemon that runs a single panel-managed `sing-box`
node. It is designed for a UniProxy or Xboard-style control plane that provides
node settings, user lists, traffic collection, and online user reporting.

## What it does

- Pulls node settings and user lists from the panel at startup
- Builds and starts an embedded `sing-box` instance
- Periodically reloads users and node config when panel state changes
- Reports traffic usage and heartbeat state back to the panel
- Optionally manages a Cloudflare DNS A record for the node

## Current protocol scope

The current runtime configuration is focused on a single deployment shape:

- `VLESS`
- `REALITY`
- `XTLS Vision`

The panel can still change some values such as users, server name, and Reality
destination port, but this project is not a general-purpose multi-protocol node
manager yet.

## Required environment variables

- `PANEL_HOST`: panel base URL, including `https://`
- `PANEL_TOKEN`: node API token
- `NODE_ID`: numeric node identifier

## Optional environment variables

- `SYNC_INTERVAL`: user and config sync interval in seconds, default `60`
- `REPORT_INTERVAL`: traffic report interval in seconds, default `60`
- `LISTEN_PORT`: inbound listen port, default `443`
- `LOG_LEVEL`: `debug`, `info`, `warn`, or `error`, default `info`
- `CF_ENABLED`: `true` to enable Cloudflare DNS registration
- `CF_API_TOKEN`: required when `CF_ENABLED=true`
- `CF_ZONE_ID`: required when `CF_ENABLED=true`
- `CF_RECORD_NAME`: required when `CF_ENABLED=true`

## Local development

Run tests:

```bash
go test ./...
```

Build the binary:

```bash
go build ./...
```

## Deployment

The repository includes a Linux installer script:

```bash
bash deploy/install.sh \
  --node-id=5 \
  --panel=https://panel.example.com \
  --token=secret
```

Optional Cloudflare arguments:

```bash
--cf-enabled
--cf-token=...
--cf-zone=...
--cf-record=node.example.com
```

## Reliability notes

- Traffic reports are buffered in memory if the panel push fails.
- The next successful report flushes the buffered usage plus any new traffic.
- `sing-box` reloads attempt to roll back to the previous instance if the new
  instance fails to start.
- Cloudflare DNS registration updates an existing A record instead of always
  creating a new one.

## Known limits

- Buffered traffic is in-memory only. A process crash can still lose pending
  usage that was not pushed yet.
- The stats gRPC listener is fixed to `127.0.0.1:10085`.
- The current implementation assumes a single embedded `sing-box` instance.
