// Empty by default: in production the UI is reverse-proxied by
// portly-server onto its own origin, so relative requests already land on
// the right place — no build-time coupling to a host/port, which matters
// because the domain can change (via the in-app Setup page) without a
// rebuild. Set NEXT_PUBLIC_API_BASE only for local dev, where `next dev`
// on :3000 talks directly to a portly-server on a different port.
export const API_BASE = process.env.NEXT_PUBLIC_API_BASE ?? "";

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

async function request<T>(
  path: string,
  init?: RequestInit
): Promise<T> {
  const res = await fetch(`${API_BASE}${path}`, {
    ...init,
    credentials: "include",
    headers: {
      "Content-Type": "application/json",
      ...(init?.headers ?? {}),
    },
  });

  if (res.status === 204) {
    return undefined as T;
  }

  const isJson = res.headers.get("content-type")?.includes("application/json");
  const body = isJson ? await res.json() : undefined;

  if (!res.ok) {
    throw new ApiError(res.status, body?.error ?? `HTTP ${res.status}`);
  }
  return body as T;
}

export interface Me {
  username: string;
  must_change_password: boolean;
}

export interface ServerInfo {
  advertise_host: string;
  control_port: number;
  api_port: number;
  ca_fingerprint: string;
}

export interface Client {
  id: string;
  name: string;
  created_at: number;
  last_seen?: number;
  connected: boolean;
}

export type TunnelProtocol = "tcp" | "udp";

export interface Tunnel {
  id: string;
  client_id: string;
  name: string;
  protocol: TunnelProtocol;
  local_host: string;
  local_port: number;
  public_port: number;
  enabled: boolean;
  traffic_limit_bytes?: number;
  bytes_in_total: number;
  bytes_out_total: number;
  created_at: number;
}

export interface TrafficSample {
  ts: number;
  bytes_in: number;
  bytes_out: number;
}

export interface Device {
  ip: string;
  mac?: string;
  hostname?: string;
}

export interface CreateClientResult {
  client: Client;
  install_command: string;
  enroll_code: string;
  expires_at: number;
}

export type CertState = "" | "pending" | "ready" | "error";

export interface SetupStatus {
  public_ip: string;
  public_ipv6?: string;
  control_port: number;
  domain?: string;
  cert_state?: CertState;
  cert_error?: string;
}

export const api = {
  login: (username: string, password: string) =>
    request<Me>("/api/auth/login", {
      method: "POST",
      body: JSON.stringify({ username, password }),
    }),
  logout: () => request<void>("/api/auth/logout", { method: "POST" }),
  me: () => request<Me>("/api/auth/me"),
  changeCredentials: (params: {
    current_password: string;
    new_username?: string;
    new_password: string;
  }) =>
    request<Me>("/api/auth/change-credentials", {
      method: "POST",
      body: JSON.stringify(params),
    }),
  serverInfo: () => request<ServerInfo>("/api/server/info"),

  listClients: () => request<Client[]>("/api/clients"),
  createClient: (name: string) =>
    request<CreateClientResult>("/api/clients", {
      method: "POST",
      body: JSON.stringify({ name }),
    }),
  deleteClient: (id: string) =>
    request<void>(`/api/clients/${id}`, { method: "DELETE" }),
  listDevices: (clientId: string) =>
    request<Device[]>(`/api/clients/${clientId}/devices`),

  listTunnels: (clientId?: string) =>
    request<Tunnel[]>(
      `/api/tunnels${clientId ? `?client_id=${clientId}` : ""}`
    ),
  createTunnel: (params: {
    client_id: string;
    name: string;
    protocol: TunnelProtocol;
    local_host: string;
    local_port: number;
    public_port: number;
  }) =>
    request<Tunnel>("/api/tunnels", {
      method: "POST",
      body: JSON.stringify(params),
    }),
  setTunnelEnabled: (id: string, enabled: boolean) =>
    request<void>(`/api/tunnels/${id}`, {
      method: "PATCH",
      body: JSON.stringify({ enabled }),
    }),
  deleteTunnel: (id: string) =>
    request<void>(`/api/tunnels/${id}`, { method: "DELETE" }),

  tunnelTraffic: (id: string, sinceUnix: number) =>
    request<TrafficSample[]>(`/api/tunnels/${id}/traffic?since=${sinceUnix}`),

  getSetup: () => request<SetupStatus>("/api/setup"),
  setDomain: (domain: string) =>
    request<{ domain: string }>("/api/setup/domain", {
      method: "POST",
      body: JSON.stringify({ domain }),
    }),
};

export interface LiveTunnelStat {
  id: string;
  name: string;
  client_id: string;
  connected: boolean;
  enabled: boolean;
  bytes_in_total: number;
  bytes_out_total: number;
  rate_in_bps: number;
  rate_out_bps: number;
}

export interface LiveTick {
  type: "tick";
  ts: number;
  tunnels: LiveTunnelStat[];
}

function wsBaseURL(): string {
  if (API_BASE) return API_BASE.replace(/^http/, "ws");
  const proto = window.location.protocol === "https:" ? "wss" : "ws";
  return `${proto}://${window.location.host}`;
}

export function connectLiveWS(
  onTick: (tick: LiveTick) => void
): () => void {
  const ws = new WebSocket(`${wsBaseURL()}/api/ws/live`);
  ws.onmessage = (ev) => {
    try {
      const data = JSON.parse(ev.data);
      if (data.type === "tick") onTick(data as LiveTick);
    } catch {
      // ignore malformed frames
    }
  };
  return () => ws.close();
}
