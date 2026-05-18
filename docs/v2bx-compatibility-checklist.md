# V2bX Compatibility Checklist

Scope: sing-box only, Xboard only, single-node only.

## Must-do

- [x] Xboard UniProxy endpoints
  - [x] `GET /api/v1/server/UniProxy/config`
  - [x] `GET /api/v1/server/UniProxy/user`
  - [x] `GET /api/v1/server/UniProxy/alivelist`
  - [x] `POST /api/v1/server/UniProxy/alive`
  - [x] `POST /api/v1/server/UniProxy/push`
- [x] Request identity
  - [x] `node_id`
  - [x] `token`
  - [x] `node_type=vless`
- [x] Config parsing
  - [x] `protocol=vless` only
  - [x] `network=tcp` only
  - [x] `REALITY` fields
  - [x] `base_config.pull_interval` / `push_interval` tolerate string or number
  - [x] `routes` tolerate object or array
- [x] User sync
  - [x] periodic fetch
  - [x] hot reload on config/user change
  - [x] rollback on reload failure
  - [x] ETag / 304 cache for users
- [x] Traffic reporting
  - [x] sing-box stats collection
  - [x] local buffering on push failure
  - [x] recovery after restart
- [x] Online reporting
  - [x] alive payload by uid -> IP list
  - [x] `alivelist` decoding
- [x] Limits
  - [x] `device_limit`
  - [x] per-user active connection limit
  - [x] per-IP active connection limit
  - [x] per-user new connection rate
  - [x] per-IP new connection rate
  - [x] `speed_limit`
- [x] Route safety
  - [x] default reject for bittorrent
  - [x] reject private and local networks
  - [x] reject localhost domains
  - [x] default direct outbound
- [x] Runtime defaults
  - [x] `reuse_addr`
  - [x] `tcp_fast_open`
  - [x] keepalive settings

## Optional parity

- [ ] Support additional Xboard route rule shapes if the panel emits them
- [ ] Map more Xboard config fields into sing-box options if needed
- [ ] Add more tolerant decoding for rare panel payload variants
- [ ] Improve diagnostics around panel version drift

## Non-goals

- [ ] Multi-panel support
- [ ] Multi-node controller list
- [ ] Multi-core support
- [ ] Protocols other than VLESS
- [ ] Transports other than TCP for this project scope
- [ ] Other panel ecosystems
- [ ] ACME / certificate management
- [ ] V2bX-style plugin/controller architecture
