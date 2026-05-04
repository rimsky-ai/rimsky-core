import { config } from './config.js';

// PeerEndpoint mirrors the per-peer fields the dashboard server cares
// about — name plus a derived HTTP base URL. observabilityHttpUrl is
// "" when the peer didn't advertise an http_bridge_url; callers MUST
// treat that as "no HTTP proxy available" and 503 with a clear message
// (issue 21) rather than silently dialling the gRPC listener.
type PeerEndpoint = { name: string; observabilityHttpUrl: string };

let executors = new Map<string, PeerEndpoint>();
let stores = new Map<string, PeerEndpoint>();
let lastRefresh = 0;
const TTL = 30_000;

export async function getExecutorEndpoint(name: string): Promise<PeerEndpoint | null> {
  await refreshIfStale();
  if (executors.has(name)) {
    const ep = executors.get(name)!;
    return ep.observabilityHttpUrl ? ep : null;
  }
  await refresh();
  const ep = executors.get(name);
  return ep && ep.observabilityHttpUrl ? ep : null;
}

export async function getStoreEndpoint(name: string): Promise<PeerEndpoint | null> {
  await refreshIfStale();
  if (stores.has(name)) {
    const ep = stores.get(name)!;
    return ep.observabilityHttpUrl ? ep : null;
  }
  await refresh();
  const ep = stores.get(name);
  return ep && ep.observabilityHttpUrl ? ep : null;
}

// peerHasBridge returns true when the peer is known but lacks an
// http_bridge_url. The proxy uses this to choose between 404 (unknown)
// and 503 (known but no bridge — the dashboard can't proxy to it).
export async function peerHasNoBridge(kind: 'executor' | 'store', name: string): Promise<boolean> {
  await refreshIfStale();
  const map = kind === 'executor' ? executors : stores;
  const ep = map.get(name);
  return Boolean(ep && !ep.observabilityHttpUrl);
}

// invalidate clears the cache so the next lookup re-fetches. Used by
// the admin refresh-discovery endpoint (issue 25).
export function invalidateDiscoveryCache(): void {
  lastRefresh = 0;
}

// status reports the current cache age so the dashboard can render a
// "discovery is stale" banner / refresh button (issue 31).
export function discoveryCacheStatus(): {
  last_refresh_ms: number | null;
  age_ms: number | null;
  ttl_ms: number;
  executor_count: number;
  store_count: number;
} {
  return {
    last_refresh_ms: lastRefresh > 0 ? lastRefresh : null,
    age_ms: lastRefresh > 0 ? Date.now() - lastRefresh : null,
    ttl_ms: TTL,
    executor_count: executors.size,
    store_count: stores.size,
  };
}

async function refreshIfStale() {
  if (Date.now() - lastRefresh < TTL) return;
  await refresh();
}

async function refresh() {
  try {
    const [exs, sts] = await Promise.all([
      fetch(`${config.controlApiUrl}/v1/observability/executors`).then((r) => r.json()),
      fetch(`${config.controlApiUrl}/v1/observability/stores`).then((r) => r.json()),
    ]);
    executors = new Map(
      ((exs as { executors?: Array<Record<string, any>> }).executors ?? []).map((e) => [
        String(e.name),
        { name: String(e.name), observabilityHttpUrl: deriveObsUrl(e) },
      ]),
    );
    stores = new Map(
      ((sts as { stores?: Array<Record<string, any>> }).stores ?? []).map((s) => [
        String(s.name),
        { name: String(s.name), observabilityHttpUrl: deriveObsUrl(s) },
      ]),
    );
    lastRefresh = Date.now();
  } catch {
    // Silent: refresh is best-effort.
  }
}

// deriveObsUrl reads the peer's advertised http_bridge_url. Returns
// "" when none is set — callers translate that to a 503 with a clear
// "peer X does not expose an HTTP bridge" message (issue 21). The old
// fallback-to-dispatch-endpoint behaviour silently routed proxy traffic
// to a gRPC listener and produced confusing failures.
function deriveObsUrl(peer: Record<string, any>): string {
  const httpBridge =
    typeof peer.http_bridge_url === 'string' ? peer.http_bridge_url : '';
  if (httpBridge) {
    return httpBridge.startsWith('http') ? httpBridge : `http://${httpBridge}`;
  }
  const caps = peer.observability_capabilities;
  if (caps && typeof caps.http_bridge_url === 'string' && caps.http_bridge_url) {
    return caps.http_bridge_url.startsWith('http')
      ? caps.http_bridge_url
      : `http://${caps.http_bridge_url}`;
  }
  return '';
}
