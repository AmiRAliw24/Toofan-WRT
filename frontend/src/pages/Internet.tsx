import { useEffect, useState } from 'preact/hooks';
import { Activity, ArrowDown, ArrowUp, Flag, Gauge as GaugeIcon, Globe, LineChart as LineIcon, Play, Timer, Waypoints } from 'lucide-preact';
import { get, post, usePoll } from '../api';
import { onRefresh, overview } from '../app';
import { L, lang, num, pct } from '../i18n';
import { Btn, Card, CardHead, Gauge, LineChart, PageHead, Seg, Skeleton, Tabs, run } from '../ui';

interface QualityReport {
  samples: { t: number; ext: number; dom: number }[];
  stability: number;
  domesticUp: number;
  foreignUp: number;
  avgExt: number;
  avgDom: number;
  lastExt: number;
  lastDom: number;
}

interface SpeedResult {
  running: boolean;
  phase: 'ping' | 'download' | 'upload' | 'done' | 'error' | '';
  via: string;
  pingMs: number;
  downMbps: number;
  upMbps: number;
  error?: string;
  at: number;
}

function Stat(p: { icon: any; label: string; value: string; tone?: string }) {
  return (
    <Card class="stat">
      <div class="stat-label">{p.icon}{p.label}</div>
      <div class="stat-value" style={{ color: p.tone }}>{p.value}</div>
    </Card>
  );
}

function Quality() {
  const [win, setWin] = useState<'24h' | '7d'>('24h');
  const { data, reload } = usePoll<QualityReport>(`/quality?window=${win}`, 60000);
  useEffect(() => onRefresh(reload), [reload]);
  const tone = (v: number) => (v >= 99 ? 'var(--ok)' : v >= 95 ? 'var(--warn)' : 'var(--danger-strong)');
  return (
    <div class="stack">
      <Card>
        <CardHead icon={<LineIcon size={20} />} title={L('کیفیت شبکه', 'Network quality')}
          sub={L('تأخیر و دسترسی، هر دقیقه توسط خود روتر ثبت می‌شود', 'Latency and reachability, measured every minute by the router itself')}
          action={<Seg value={win} onChange={setWin} items={[{ id: '24h', label: L('۲۴ ساعت', '24 h') }, { id: '7d', label: L('یک هفته', '7 days') }]} />} />
        <div class="card-body">
          {!data ? <Skeleton h={100} /> : (
            <div class="grid g4" style="gap:12px">
              <Stat icon={<Waypoints size={16} />} label={L('پایداری خط', 'Stability')} value={pct(data.stability, 1)} tone={tone(data.stability)} />
              <Stat icon={<Flag size={16} />} label={L('دسترسی داخلی', 'Domestic reach')} value={pct(data.domesticUp, 1)} tone={tone(data.domesticUp)} />
              <Stat icon={<Globe size={16} />} label={L('دسترسی خارجی', 'Foreign reach')} value={pct(data.foreignUp, 1)} tone={tone(data.foreignUp)} />
              <Stat icon={<Timer size={16} />} label={L('میانگین تأخیر خارجی', 'Avg. foreign latency')} value={`${num(data.avgExt)} ms`} />
            </div>
          )}
        </div>
      </Card>
      <Card class="chart-card">
        <div class="chart-head">
          <div class="card-title">{L('تأخیر خارجی', 'International latency')}</div>
          <div class="chart-val">{data && data.lastExt >= 0 ? `${num(data.lastExt)} ms` : '—'}</div>
        </div>
        <div class="tiny faint" style="margin-bottom:8px">
          {overview.value?.proxy.mode === 'connected' ? L('از داخل تونل، با درخواست واقعی', 'Through the tunnel with a real request') : L('اتصال مستقیم', 'Direct connection')}
        </div>
        <LineChart values={(data?.samples || []).map((s) => s.ext)} color="var(--chart-1)" />
      </Card>
      <Card class="chart-card">
        <div class="chart-head">
          <div class="card-title">{L('تأخیر داخلی', 'Domestic latency')}</div>
          <div class="chart-val">{data && data.lastDom >= 0 ? `${num(data.lastDom)} ms` : '—'}</div>
        </div>
        <div class="tiny faint" style="margin-bottom:8px">{L('آپارات، دیجی‌کالا و دیوار', 'Aparat, Digikala and Divar')}</div>
        <LineChart values={(data?.samples || []).map((s) => s.dom)} color="var(--chart-2)" />
      </Card>
      <div class="callout info small">
        <Activity size={18} />
        <div>{L('خطوط قرمز عمودی یعنی آن دقیقه پاسخی نیامد. اگر هر دو نمودار هم‌زمان قرمز شوند، مشکل از خط یا مودم است؛ اگر فقط خارجی قرمز شود، مشکل از سرور یا فیلترینگ است.',
          'Red vertical lines mark minutes with no response. If both charts go red together, the line or modem is at fault; if only international does, it’s the server or filtering.')}</div>
      </div>
    </div>
  );
}

function SpeedTest() {
  const [r, setR] = useState<SpeedResult | null>(null);
  const tunnel = overview.value?.proxy.mode === 'connected';
  useEffect(() => {
    let t: number;
    const poll = async () => {
      try {
        const v = await get<SpeedResult>('/speedtest');
        setR(v);
        t = window.setTimeout(poll, v.running ? 500 : 4000);
      } catch {
        t = window.setTimeout(poll, 4000);
      }
    };
    poll();
    return () => clearTimeout(t);
  }, []);
  const start = async (via: string) => {
    await run(() => post('/speedtest', { via }));
    setR({ ...(r as SpeedResult), running: true, phase: 'ping', downMbps: 0, upMbps: 0, pingMs: 0 });
  };
  const phase = r?.phase;
  const live = phase === 'upload' ? r!.upMbps : r?.downMbps || 0;
  const label = phase === 'upload' ? L('آپلود', 'Upload') : L('دانلود', 'Download');
  return (
    <Card>
      <CardHead icon={<GaugeIcon size={20} />} title={L('تست سرعت', 'Speed test')}
        sub={L('سرعت واقعی را از خود روتر اندازه می‌گیرد، نه از گوشی شما', 'Measures real speed from the router itself, not your phone')} />
      <div class="card-body">
        <div class="gauge-wrap">
          <Gauge value={live} max={Math.max(50, live * 1.3)} label={label} unit="Mbps" />
          {r?.running && <div class="muted small">{phase === 'ping' ? L('اندازه‌گیری پینگ…', 'Measuring ping…') : phase === 'download' ? L('در حال دانلود…', 'Downloading…') : L('در حال آپلود…', 'Uploading…')}</div>}
          {phase === 'error' && <div class="small" style="color:var(--danger-strong)">{r?.error}</div>}
        </div>
        <div class="grid g3" style="gap:12px;margin-top:12px">
          <Stat icon={<Timer size={16} />} label={L('پینگ', 'Ping')} value={r?.pingMs ? `${num(r.pingMs)} ms` : '—'} />
          <Stat icon={<ArrowDown size={16} />} label={L('دانلود', 'Download')} value={r?.downMbps ? `${num(r.downMbps, 1)} Mbps` : '—'} />
          <Stat icon={<ArrowUp size={16} />} label={L('آپلود', 'Upload')} value={r?.upMbps ? `${num(r.upMbps, 1)} Mbps` : '—'} />
        </div>
      </div>
      <div class="card-foot">
        <Btn kind="primary" busy={r?.running} onClick={() => start(tunnel ? 'tunnel' : 'direct')}>
          <Play size={17} />{tunnel ? L('شروع (از تونل)', 'Start (via tunnel)') : L('شروع', 'Start')}
        </Btn>
        {tunnel && <Btn disabled={r?.running} onClick={() => start('direct')}>{L('تست مستقیم (بدون تونل)', 'Direct test (no tunnel)')}</Btn>}
        {r?.at && !r.running && phase === 'done' && <span class="small faint" style="align-self:center">{r.via === 'tunnel' ? L('آخرین تست: از تونل', 'Last test: via tunnel') : L('آخرین تست: مستقیم', 'Last test: direct')}</span>}
      </div>
    </Card>
  );
}

export function Internet() {
  const [tab, setTab] = useState<'quality' | 'speed'>(location.hash.includes('speed') ? 'speed' : 'quality');
  lang.value;
  return (
    <div class="stack">
      <PageHead title={L('اینترنت', 'Internet')} sub={L('اتصال به اینترنت، سرعت واقعی خط و کیفیت آن در طول زمان', 'Your connection, real line speed and quality over time')} />
      <Tabs value={tab} onChange={setTab} items={[
        { id: 'quality', label: L('کیفیت خط', 'Line quality'), icon: <LineIcon size={17} /> },
        { id: 'speed', label: L('تست سرعت', 'Speed test'), icon: <GaugeIcon size={17} /> },
      ]} />
      {tab === 'quality' ? <Quality /> : <SpeedTest />}
    </div>
  );
}
