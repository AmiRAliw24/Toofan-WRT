import { signal } from '@preact/signals';
import { useEffect, useRef, useState, useCallback } from 'preact/hooks';

export const authed = signal<boolean | null>(null);

export class ApiError extends Error {
  status: number;
  constructor(status: number, msg: string) {
    super(msg);
    this.status = status;
  }
}

export async function api<T = any>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch('/api' + path, {
    method,
    credentials: 'same-origin',
    headers: body !== undefined || method !== 'GET' ? { 'Content-Type': 'application/json' } : {},
    body: body !== undefined ? JSON.stringify(body) : method !== 'GET' ? '{}' : undefined,
  });
  if (res.status === 401 && path !== '/login') {
    authed.value = false;
    throw new ApiError(401, 'login required');
  }
  const ct = res.headers.get('content-type') || '';
  const data = ct.includes('json') ? await res.json() : await res.text();
  if (!res.ok) throw new ApiError(res.status, (data && data.error) || res.statusText);
  return data as T;
}

export const get = <T = any>(p: string) => api<T>('GET', p);
export const post = <T = any>(p: string, b?: unknown) => api<T>('POST', p, b ?? {});
export const del = <T = any>(p: string) => api<T>('DELETE', p);

/** usePoll fetches a resource and refreshes it while the tab is visible. */
export function usePoll<T>(path: string | null, intervalMs = 3000) {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<string | null>(null);
  const timer = useRef<number>();
  const alive = useRef(true);

  const load = useCallback(async () => {
    if (!path) return;
    try {
      const d = await get<T>(path);
      if (alive.current) {
        setData(d);
        setError(null);
      }
    } catch (e: any) {
      if (alive.current) setError(e.message);
    }
  }, [path]);

  useEffect(() => {
    alive.current = true;
    load();
    if (intervalMs > 0) {
      const tick = () => {
        if (!document.hidden) load();
      };
      timer.current = window.setInterval(tick, intervalMs);
    }
    return () => {
      alive.current = false;
      clearInterval(timer.current);
    };
  }, [load, intervalMs]);

  return { data, error, reload: load, setData };
}

// ---- shared types ----

export interface Server {
  id: string;
  name: string;
  protocol: string;
  address: string;
  port: number;
  network: string;
  security: string;
  subId?: string;
  latencyMs: number;
  testedAt?: number;
  testError?: string;
}

export interface Subscription {
  id: string;
  name: string;
  url: string;
  updatedAt?: number;
  error?: string;
  count: number;
}

export interface ProxyEvent { at: number; kind: string; msg: string }

export interface ProxyStatus {
  enabled: boolean;
  mode: 'off' | 'connecting' | 'connected' | 'degraded' | 'fallback' | 'killswitch' | 'safe';
  activeId: string;
  server?: Server;
  latencyMs: number;
  checkedAt: number;
  fails: number;
  lastError?: string;
  since: number;
  events: ProxyEvent[];
  testing: boolean;
}

export interface ProxyConfig {
  enabled: boolean;
  engine: string;
  servers: Server[];
  subscriptions: Subscription[];
  activeId: string;
  preferredId: string;
  splitIran: boolean;
  killSwitch: boolean;
  blockIPv6: boolean;
  guestProxy: boolean;
  autoBest: boolean;
  subAutoUpdate: boolean;
}

export interface Device {
  mac: string;
  ip: string;
  hostname: string;
  online: boolean;
  link: 'wifi5' | 'wifi24' | 'lan' | 'guest' | '';
  signal?: number;
  randomMac: boolean;
  downBps: number;
  upBps: number;
  downTotal: number;
  upTotal: number;
  lastSeen: number;
  guessType: string;
  name: string;
  type: string;
  blocked: boolean;
  direct: boolean;
  priority: boolean;
  downKbps: number;
  upKbps: number;
}

export interface Schedule {
  id: string;
  macs: string[];
  days: number[];
  from: string;
  to: string;
  enabled: boolean;
}

export interface SystemStats {
  model: string;
  hostname: string;
  firmware: string;
  kernel: string;
  uptimeSec: number;
  cpuPercent: number;
  cores: number;
  load: number[];
  memTotalKb: number;
  memAvailKb: number;
  diskTotalKb: number;
  diskFreeKb: number;
  tempC?: number;
}

export interface WAN {
  up: boolean;
  proto: string;
  ip: string;
  gateway: string;
  dns: string[];
  device: string;
  rxBps: number;
  txBps: number;
  uptimeSec: number;
}

export interface NetInfo {
  ip: string;
  isp: string;
  ispFa?: string;
  country: string;
  countryCode: string;
  city: string;
  at: number;
  error?: string;
}

export interface Overview {
  system: SystemStats;
  wan: WAN;
  proxy: ProxyStatus;
  split: boolean;
  devices: { total: number; online: number };
  top: Device[];
  quality: { ext: number; dom: number };
  operator?: NetInfo;
  exit?: NetInfo;
  fwError?: string;
}
