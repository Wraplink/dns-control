# Deploy a new WrapLink DNS Control node (Debian 13)

This guide is for a **new standalone DNS Control host**. The central platform already includes its own `dns-control` service; do not start a second instance there without planning DNS and API port ownership.

## 1. Prerequisites

- Debian 13, Docker Engine, Docker Compose plugin, Git.
- Public DNS IPv4 assigned/forwarded to this host; inbound UDP **and** TCP port 53.
- Outbound DNS to upstream resolvers and HTTPS to the central Backend.
- Unique DNS node ID and region; Backend network control sync key; DNS Control agent token.
- Central Backend HTTPS URL accessible from this node, with its egress IPv4 permitted by the central API allowlist. The current central `api.wraplink.ir` vhost may allow **only** Edge Agent IPs; extend its allowlist explicitly for the DNS node before deploying.
- Never commit tokens to Git or expose the DNS Control management API (8080) publicly without appropriate HTTPS, IP filtering, and authentication.

## 2. Check out and configure

```bash
git clone https://github.com/Wraplink/dns-control.git /opt/wraplink/dns-control
cd /opt/wraplink/dns-control
```

The standalone `config.yaml` uses environment placeholders. Example values:

```dotenv
DNS_LISTEN=:53
DNS_UPSTREAM=1.1.1.1:53,8.8.8.8:53
API_LISTEN=127.0.0.1:8080
DNS_CONTROL_AGENT_TOKEN=<secret>
WRAPLINK_BACKEND_URL=https://api.wraplink.ir
NETWORK_CONTROL_SYNC_KEY=<secret>
NODE_ID=dns-fra-02
NODE_REGION=eu-central
NODE_PUBLIC_IP=<public-DNS-IPv4>
```

`DNS_UPSTREAM` is inserted into YAML as a comma-separated list (`upstream: [${DNS_UPSTREAM}]`); keep valid `IP:53` entries. The Go config loader expands environment variables before YAML parsing. Ensure the environment is actually passed to the container, not merely present in a host `.env` file.

**Important:** the current DNS Control loader uses `os.ExpandEnv`, which replaces unset variables with empty strings. Review and validate required values before starting.

## 3. Standalone Docker Compose example

Create `compose.standalone.yml`:

```yaml
services:
  dns-control:
    build: .
    restart: unless-stopped
    env_file: .env.local
    volumes:
      - ./config.yaml:/etc/dns-control/config.yaml:ro
      - dnsqueryspool:/var/lib/wraplink/dns-query-spool
    ports:
      - "53:53/udp"
      - "53:53/tcp"
    # API_LISTEN must bind to an address reachable within the container
    # if an authenticated internal API or reverse proxy is later required.
volumes:
  dnsqueryspool:
```

Copy the example variables to `.env.local`, insert actual secrets, and run `chmod 600 .env.local`. To enable DNS query reporting after deploying compatible Backend ingestion and DNS-Control versions, add `WRAPLINK_DNS_QUERY_REPORT_ENABLED=true`, `WRAPLINK_DNS_QUERY_SPOOL_DIR=/var/lib/wraplink/dns-query-spool`, and optionally `WRAPLINK_DNS_QUERY_SPOOL_MAX_BYTES=67108864` to `.env.local`. The named volume preserves persisted batches across container recreation; do not run `docker compose down -v`. The image provisions the spool directory for UID 10001. See [query reporting operations](dns-query-reporting-operations.md) for recovery testing and limitations. In this Compose example the management API is **not published**. If API access is required, design a private network or authenticated HTTPS reverse proxy first; do not publish `8080:8080` directly.

## 4. Deploy

```bash
docker compose -f compose.standalone.yml config --quiet
docker compose -f compose.standalone.yml up -d --build
docker compose -f compose.standalone.yml ps
docker compose -f compose.standalone.yml logs --tail=100 -f dns-control
```

Check for existing DNS listeners before deploying: `ss -lntup | grep ':53 '`. Disable/reconfigure conflicting services deliberately; do not indiscriminately disable system DNS.

## 5. Validate

```bash
dig @127.0.0.1 example.com A
dig +tcp @127.0.0.1 example.com A
```

DNS responses may be refused for clients without entitlements; verify behavior with an authorized client. On the central Backend, verify that the new node ID registers, heartbeats, and advertises the expected public DNS address. Check logs for sync/auth errors, upstream DNS failures, and lease reconciliation issues.

## 6. Common issues

- No registration: check central Backend URL, token, HTTPS trust, Nginx IP allowlist, and connectivity.
- DNS timeouts: check both UDP/TCP 53 forwarding and upstream resolver egress.
- Agent API unauthorized: verify `DNS_CONTROL_AGENT_TOKEN`; do not print it.
- Unexpected empty YAML values: check environment injection and placeholder expansion.
- Multiple DNS nodes: use unique `NODE_ID` and advertise only IPs actually routed to each host.

Production deployments should implement certificate renewal and restrictive firewall rules for any additional HTTPS API hostnames.

## Backend-authoritative agent snapshots

After upgrading Backend and Edge Agents, verify the Backend `network_agent_health` table contains fresh online agent heartbeats. Then add `WRAPLINK_BACKEND_AGENT_SNAPSHOT_ENABLED=true` to the DNS Control container environment (not only an unpassed host shell variable) and recreate DNS Control. Leave unset/false during initial deployment or rollback. Snapshot pages must share one revision; investigate synchronization errors before switching traffic. See [coordinated rollout](https://github.com/Wraplink/backend/blob/master/docs/agent-registry-rollout.md).

## Optional Edge Agent origin resolver (experimental)

DNS Control can delegate **management origin lookups** received at
`POST /api/v1/dns/resolve` to an Edge Agent instead of using its local
upstream DNS resolvers. This is **disabled by default** and does not change
ordinary client DNS queries or managed hijack answers.

In `config.yaml`, uncomment:

```yaml
dns:
  origin_agent_url: "https://edge-resolver.example.com"
  origin_agent_token: "${DNS_ORIGIN_AGENT_TOKEN}"
```

The URL must be an HTTPS origin without a path. The token must be at least
32 characters and must be provisioned to the agent's origin-resolution API.
The agent must implement authenticated `POST /api/v1/dns/resolve`, accept
`application/dns-message` requests, and return an unmodified DNS wire
response with matching request ID and question. This endpoint is **not yet
implemented in the Edge Agent repository**; do not enable this setting in
production until the agent implementation is deployed and tested.

Agent resolution is fail-closed: HTTP failures, timeouts, or an NXDOMAIN
response do not trigger fallback to local DNS. Avoid recursive loops: the
agent must resolve via its independent upstream resolvers, never through
WrapLink's managed/hijacked DNS service.

This option currently applies to the DNS Control management resolve API.
Edge Agent lease activation still resolves locally until its integration
is updated to request origin resolution through DNS Control.
