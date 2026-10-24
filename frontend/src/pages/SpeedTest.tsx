import { useEffect, useRef, useState } from 'preact/hooks';
import { ArrowDown, ArrowLeft, ArrowRight, ArrowUp, Gauge as GaugeIcon, History, Play, RotateCcw, Search, Server, Shield, Timer, Waves, Wifi } from 'lucide-preact';
import { get, post } from '../api';
import { overview } from '../app';
import { L, ago, lang, num } from '../i18n';
import { Badge, Btn, Card, CardHead, Gauge, Modal, Spinner, run, toast } from '../ui';

interface SpeedServer { id: string; kind: string; name: string; sponsor: string; country: string; countryCode: string; host?: string; url?: string; distanceKm?: number }
interface SpeedResult {
  running: boolean;
  phase: 'ping' | 'download' | 'upload' | 'done' | 'error' | '';
  via: string;
  server: SpeedServer;
  pingMs: number;
  jitterMs: number;
  downMbps: number;
  upMbps: number;
  error?: string;
  at: number;
}

function flag(cc?: string) {
  if (!cc || cc.length !== 2) return '🌐';
  return String.fromCodePoint(...[...cc.toUpperCase()].map((c) => 0x1f1e6 + c.charCodeAt(0) - 65));
}

function serverTitle(s: SpeedServer) {
  return s.kind === 'cloudflare' ? 'Cloudflare' : s.sponsor;
}

function serverSub(s: SpeedServer) {
  if (s.kind === 'cloudflare') return L('نزدیک‌ترین مرکز داده (جهانی)', 'Nearest data centre (global)');
  return `${s.name}، ${s.country}${s.distanceKm ? ` · ${num(s.distanceKm)} km` : ''}`;
}

function store(key: string, v?: unknown) {
  try {
    if (v === undefined) return JSON.parse(localStorage.getItem(key) || 'null');
    localStorage.setItem(key, JSON.stringify(v));
  } catch {}
  return null;
}

function ServerPicker({ via, onPick, onClose }: { via: string; onPick: (s: SpeedServer) => void; onClose: () => void }) {
  const [q, setQ] = useState('');
  const [list, setList] = useState<SpeedServer[] | null>(null);
  const [err, setErr] = useState('');
  const timer = useRef<number>();
  const load = (query: string) => {
    setList(null);
    get<{ servers: SpeedServer[]; error?: string }>(`/speedtest/servers?via=${via}&q=${encodeURIComponent(query)}`)
      .then((d) => { setList(d.servers); setErr(d.error || ''); })
      .catch((e) => { setList([]); setErr(e.message); });
  };
  useEffect(() => load(''), []);
  return (
    <Modal title={L('انتخاب سرور مقصد', 'Choose a test server')} onClose={onClose} wide>
      <div class="input-wrap">
        <input class="input" autoFocus placeholder={L('جستجوی شهر یا شرکت (مثلاً Tehran، MCI، Shatel)', 'Search a city or provider (e.g. Tehran, Frankfurt)')} value={q}
          onInput={(e) => {
            const v = (e.target as HTMLInputElement).value;
            setQ(v);
            clearTimeout(timer.current);
            timer.current = window.setTimeout(() => load(v), 450);
          }} />
        <span class="btn ghost icon" style="pointer-events:none"><Search size={17} /></span>
      </div>
      {err && <div class="small" style="color:var(--warn)">{L('فهرست سرورهای Ookla در دسترس نیست؛ فقط Cloudflare قابل انتخاب است.', 'The Ookla server list is unavailable; only Cloudflare can be used.')} <span class="mono tiny">{err}</span></div>}
      {!list && <div class="row" style="justify-content:center;padding:20px"><Spinner /></div>}
      <div class="card" style="box-shadow:none">
        {(list || []).map((s) => (
          <div class="list-item clickable" onClick={() => { onPick(s); onClose(); }}>
            <span style="font-size:22px">{s.kind === 'cloudflare' ? '☁️' : flag(s.countryCode)}</span>
            <div class="grow" style="min-width:0">
              <div class="truncate" style="font-weight:700"><bdi>{serverTitle(s)}</bdi></div>
              <div class="tiny faint truncate">{serverSub(s)}</div>
            </div>
            {s.countryCode === 'IR' && <Badge tone="violet">{L('داخلی', 'Iran')}</Badge>}
          </div>
        ))}
      </div>
    </Modal>
  );
}

function Metric(p: { icon: any; label: string; value: string; unit?: string; active?: boolean }) {
  return (
    <div class={'card stat'} style={{ boxShadow: 'none', borderColor: p.active ? 'var(--primary)' : undefined }}>
      <div class="stat-label">{p.icon}{p.label}</div>
      <div class="stat-value ltr" style="text-align:start">{p.value}<span class="small faint"> {p.unit}</span></div>
    </div>
  );
}

export function SpeedTest() {
  const o = overview.value;
  const tunnelUp = o?.proxy.mode === 'connected';
  const [via, setVia] = useState<'direct' | 'tunnel'>(tunnelUp ? 'tunnel' : 'direct');
  const [server, setServer] = useState<SpeedServer | null>(null);
  const [picker, setPicker] = useState(false);
  const [r, setR] = useState<SpeedResult | null>(null);
  const [history, setHistory] = useState<SpeedResult[]>([]);
  lang.value;

  // Default destination per path: remembered choice, else nearest server.
  useEffect(() => {
    const saved = store('speed.server.' + via) as SpeedServer | null;
    if (saved) { setServer(saved); return; }
    setServer(null);
    get<{ servers: SpeedServer[] }>(`/speedtest/servers?via=${via}`).then((d) => {
      const list = d.servers || [];
      setServer((via === 'direct' ? list.find((s) => s.kind === 'ookla') : null) || list[0] || null);
    }).catch(() => {});
  }, [via]);

  useEffect(() => {
    let t: number;
    let alive = true;
    const poll = async () => {
      try {
        const v = await get<SpeedResult>('/speedtest');
        if (!alive) return;
        setR((prev) => {
          if (prev?.running && !v.running) get<SpeedResult[]>('/speedtest/history').then(setHistory).catch(() => {});
          return v;
        });
        t = window.setTimeout(poll, v.running ? 400 : 3000);
      } catch {
        t = window.setTimeout(poll, 3000);
      }
    };
    poll();
    get<SpeedResult[]>('/speedtest/history').then(setHistory).catch(() => {});
    return () => { alive = false; clearTimeout(t); };
  }, []);

  const start = async () => {
    if (!server) { toast(L('یک سرور انتخاب کنید', 'Pick a server'), true); return; }
    const ok = await run(() => post('/speedtest', { via, server }));
    if (ok) setR({ running: true, phase: 'ping', via, server, pingMs: 0, jitterMs: 0, downMbps: 0, upMbps: 0, at: Date.now() / 1000 });
  };

  const running = !!r?.running;
  const phase = r?.phase;
  const live = phase === 'upload' ? r!.upMbps : r?.downMbps || 0;
  const Arrow = lang.value === 'fa' ? ArrowLeft : ArrowRight;
  const lineName = o?.operator?.isp ? (lang.value === 'fa' && o.operator.ispFa ? o.operator.ispFa : o.operator.isp) : L('خط شما', 'Your line');

  return (
    <div class="stack">
      <Card>
        <CardHead icon={<GaugeIcon size={20} />} title={L('تست سرعت', 'Speed test')}
          sub={L('سرعت واقعی را از خود روتر اندازه می‌گیرد؛ مبدأ و مقصد را خودتان انتخاب کنید', 'Measured from the router itself; choose where it starts and ends')} />
        <div class="card-body stack" style="gap:18px">
          <div class="speed-route">
            <div class="speed-end">
              <div class="tiny faint">{L('از (مبدأ)', 'From')}</div>
              <div class="seg" style="margin:6px 0 10px">
                <button class={via === 'direct' ? 'active' : ''} disabled={running} onClick={() => setVia('direct')}><Wifi size={14} /> {L('مستقیم', 'Direct')}</button>
                <button class={via === 'tunnel' ? 'active' : ''} disabled={running || !tunnelUp} title={!tunnelUp ? L('تونل وصل نیست', 'Tunnel is not connected') : ''} onClick={() => setVia('tunnel')}><Shield size={14} /> {L('از تونل', 'Tunnel')}</button>
              </div>
              {via === 'direct' ? (
                <>
                  <div style="font-weight:800"><bdi>{lineName}</bdi></div>
                  <div class="tiny faint mono">{o?.operator?.ip || ''}</div>
                </>
              ) : (
                <>
                  <div style="font-weight:800"><bdi>{o?.proxy.server?.name || L('تونل', 'Tunnel')}</bdi></div>
                  <div class="tiny faint mono">{o?.exit?.ip ? `${flag(o.exit.countryCode)} ${o.exit.ip}` : ''}</div>
                </>
              )}
            </div>
            <div class="speed-arrow"><Arrow size={22} /></div>
            <div class="speed-end">
              <div class="tiny faint">{L('به (مقصد)', 'To')}</div>
              {server ? (
                <div class="row" style="margin-top:8px;gap:10px">
                  <span style="font-size:26px">{server.kind === 'cloudflare' ? '☁️' : flag(server.countryCode)}</span>
                  <div class="grow" style="min-width:0">
                    <div class="truncate" style="font-weight:800"><bdi>{serverTitle(server)}</bdi></div>
                    <div class="tiny faint truncate">{serverSub(server)}</div>
                  </div>
                </div>
              ) : <div class="row" style="margin-top:12px"><Spinner /></div>}
              <Btn sm style="margin-top:10px" disabled={running} onClick={() => setPicker(true)}><Server size={15} />{L('تغییر سرور', 'Change server')}</Btn>
            </div>
          </div>

          <div class="gauge-wrap" style="min-height:190px">
            {running || phase === 'done' ? (
              <>
                <Gauge value={live} max={Math.max(50, Math.ceil((Math.max(r!.downMbps, r!.upMbps) * 1.25) / 10) * 10)}
                  label={phase === 'upload' ? L('آپلود', 'Upload') : L('دانلود', 'Download')} unit="Mbps" />
                <div class="muted small">
                  {phase === 'ping' && L('اندازه‌گیری پینگ و جیتر…', 'Measuring ping and jitter…')}
                  {phase === 'download' && L('در حال دانلود…', 'Downloading…')}
                  {phase === 'upload' && L('در حال آپلود…', 'Uploading…')}
                  {phase === 'done' && <Btn kind="primary" onClick={start}><RotateCcw size={17} />{L('دوباره', 'Again')}</Btn>}
                </div>
              </>
            ) : (
              <button class="speed-go" onClick={start} disabled={!server}>
                <Play size={30} />
                <span>{L('شروع', 'GO')}</span>
              </button>
            )}
            {phase === 'error' && (
              <div class="callout danger small" style="margin-top:10px"><div>
                <b>{L('تست انجام نشد', 'Test failed')}</b>
                <span class="mono">{r?.error}</span>
                <div><Btn sm style="margin-top:6px" onClick={start}>{L('تلاش دوباره', 'Try again')}</Btn></div>
              </div></div>
            )}
          </div>

          <div class="grid g4" style="gap:12px">
            <Metric icon={<Timer size={16} />} label={L('پینگ', 'Ping')} value={r?.pingMs ? num(r.pingMs) : '—'} unit="ms" active={phase === 'ping'} />
            <Metric icon={<Waves size={16} />} label={L('جیتر', 'Jitter')} value={r?.pingMs ? num(r.jitterMs) : '—'} unit="ms" active={phase === 'ping'} />
            <Metric icon={<ArrowDown size={16} />} label={L('دانلود', 'Download')} value={r?.downMbps ? num(r.downMbps, 1) : '—'} unit="Mbps" active={phase === 'download'} />
            <Metric icon={<ArrowUp size={16} />} label={L('آپلود', 'Upload')} value={r?.upMbps ? num(r.upMbps, 1) : '—'} unit="Mbps" active={phase === 'upload'} />
          </div>
        </div>
      </Card>

      <Card>
        <CardHead icon={<History size={20} />} title={L('تست‌های قبلی', 'Previous tests')} />
        {history.length === 0 && <div class="card-body muted small">{L('هنوز تستی انجام نشده است.', 'No tests yet.')}</div>}
        {history.map((h) => (
          <div class="list-item">
            <span style="font-size:20px">{h.server.kind === 'cloudflare' ? '☁️' : flag(h.server.countryCode)}</span>
            <div class="grow" style="min-width:0">
              <div class="row" style="gap:6px">
                <b class="truncate"><bdi>{serverTitle(h.server)}</bdi></b>
                <Badge tone={h.via === 'tunnel' ? 'ok' : undefined}>{h.via === 'tunnel' ? L('تونل', 'tunnel') : L('مستقیم', 'direct')}</Badge>
              </div>
              <div class="tiny faint">{ago(h.at)}</div>
            </div>
            <div class="ltr small" style="text-align:end;line-height:1.5">
              <div><b>↓ {num(h.downMbps, 1)}</b> · ↑ {num(h.upMbps, 1)} <span class="faint">Mbps</span></div>
              <div class="faint">{num(h.pingMs)} ms · ±{num(h.jitterMs)}</div>
            </div>
          </div>
        ))}
      </Card>

      {picker && <ServerPicker via={via} onClose={() => setPicker(false)} onPick={(s) => { setServer(s); store('speed.server.' + via, s); }} />}
    </div>
  );
}
