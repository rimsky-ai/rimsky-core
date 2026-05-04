import { config } from './config.js';

// PeerEndpoint mirrors the per-peer fields the dashboard server cares
// about — name plus a derived HTTP base URL.
type PeerEndpoint = { name: string; observabilityHttpUrl: string };

let executors = new Map<string, PeerEndpoint>();
let stores = new Map<string, PeerEndpoint>();
let lastRefresh = 0;
const TTL = 30_000;

export async function getExecutorEndpoint(name: string): Promise<PeerEndpoint | null> {
  await refreshIfStale();
  if (executors.has(name)) return executors.get(name)!;
  await refresh();
  return executors.get(name) ?? null;
}

export async function getStoreEndpoint(name: string): Promise<PeerEndpoint | null> {
  await refreshIfStale();
  if (stores.has(name)) return stores.get(name)!;
  await refresh();
  return stores.get(name) ?? null;
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

function deriveObsUrl(peer: Record<string, any>): string {
  // Preferred: peer's advertised HTTP bridge URL from the observability
  // capabilities handshake. Falls back to the operator-declared
  // observability_endpoint or dispatch endpoint when the peer didn't
  // declare an HTTP bridge — but in that case the dashboard's HTTP
  // proxy can't dial a gRPC listener and routes will fail.
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
  const ep = peer.observability_endpoint || peer.endpoint;
  if (!ep || typeof ep !== 'string') return '';
  return ep.startsWith('http') ? ep : `http://${ep}`;
}
