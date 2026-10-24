import { useEffect, useState } from 'preact/hooks';
import { Activity, ArrowDown, ArrowUp, CheckCircle2, CircleSlash, Flag, Gauge as GaugeIcon, Globe, History, LineChart as LineIcon, Play, Settings2, Stethoscope, Timer, Waypoints, XCircle } from 'lucide-preact';
import { get, post, usePoll } from '../api';
import { UplinkCard, WanCard } from './Network';
import { SpeedTest } from './SpeedTest';
import { onRefresh, overview } from '../app';
import { L, ago, clock, lang, num, pct } from '../i18n';
import { Badge, Btn, Card, CardHead, Gauge, LineChart, PageHead, Seg, Skeleton, Tabs, run } from '../ui';

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
        <LineChart points={(data?.samples || []).map((s) => ({ t: s.t, v: s.ext }))} color="var(--chart-1)" loading={!data} />
      </Card>
      <Card class="chart-card">
        <div class="chart-head">
          <div class="card-title">{L('تأخیر داخلی', 'Domestic latency')}</div>
          <div class="chart-val">{data && data.lastDom >= 0 ? `${num(data.lastDom)} ms` : '—'}</div>
        </div>
        <div class="tiny faint" style="margin-bottom:8px">{L('آپارات، دیجی‌کالا و دیوار', 'Aparat, Digikala and Divar')}</div>
        <LineChart points={(data?.samples || []).map((s) => ({ t: s.t, v: s.dom }))} color="var(--chart-2)" loading={!data} />
      </Card>
      <div class="callout info small">
        <Activity size={18} />
        <div>{L('خطوط قرمز عمودی یعنی آن دقیقه پاسخی نیامد. اگر هر دو نمودار هم‌زمان قرمز شوند، مشکل از خط یا مودم است؛ اگر فقط خارجی قرمز شود، مشکل از سرور یا فیلترینگ است.',
          'Red vertical lines mark minutes with no response. If both charts go red together, the line or modem is at fault; if only international does, it’s the server or filtering.')}</div>
      </div>
    </div>
  );
}

interface Step { id: string; status: 'ok' | 'fail' | 'skip'; ms?: number; detail?: string }
interface DiagReport { steps: Step[]; rootCause: string; at: number }
interface Incident { kind: string; start: number; end?: number }

const stepInfo = (id: string) => ({
  wan: { t: L('اتصال روتر به مودم', 'Router ↔ modem link'), h: L('کابل WAN یا خود مودم را بررسی کنید؛ مودم را یک بار خاموش و روشن کنید.', 'Check the WAN cable or the modem; power-cycle the modem.') },
  gateway: { t: L('پاسخ مودم', 'Modem responds'), h: L('مودم روشن است ولی جواب نمی‌دهد؛ مودم را ری‌استارت کنید.', 'The modem is up but silent; restart it.') },
  'isp-dns': { t: L('DNS اپراتور', 'ISP DNS'), h: L('DNS اپراتور جواب نمی‌دهد؛ معمولاً یعنی خط اینترنت قطع است.', 'The ISP DNS is silent; usually the line itself is down.') },
  domestic: { t: L('اینترنت داخلی', 'Domestic internet'), h: L('سایت‌های ایرانی هم باز نمی‌شوند؛ مشکل از خط یا اپراتور است.', 'Even Iranian sites fail: the line or the ISP is at fault.') },
  foreign: { t: L('اینترنت بین‌الملل (مستقیم)', 'International (direct)'), h: L('مسیر مستقیم خارجی بسته است؛ با اینترنت آزاد مشکلی نیست.', 'The direct international route is blocked; fine when the free internet works.') },
  engine: { t: L('موتور اینترنت آزاد', 'Free-internet engine'), h: L('هیچ سروری جواب نمی‌دهد؛ سرورها را آزمایش کنید یا اشتراک را به‌روز کنید.', 'No server answers; test servers or update the subscription.') },
  tunnel: { t: L('عبور ترافیک از تونل', 'Traffic through the tunnel'), h: L('تونل وصل است ولی ترافیک رد نمی‌شود؛ سرور دیگری انتخاب کنید.', 'The tunnel is up but passes no traffic; pick another server.') },
  'tunnel-dns': { t: L('DNS امن از تونل', 'Secure DNS via tunnel'), h: L('DNS داخل تونل جواب نمی‌دهد؛ DNS دیگری در صفحهٔ DNS انتخاب کنید.', 'DNS inside the tunnel fails; choose another provider on the DNS page.') },
  'iran-direct': { t: L('سایت‌های ایرانی مستقیم', 'Iranian sites direct'), h: L('یک سایت ایرانی از مسیر مستقیم باز نشد؛ جزئیات همین خط علت را می‌گوید (آدرس غیرایرانی یعنی DNS از خارج جواب داده).', 'An Iranian site did not open directly; the detail on this line says why (a non-Iranian address means DNS answered from abroad).') },
  firewall: { t: L('فایروال پنل', 'Panel firewall'), h: L('قوانین فایروال اعمال نشده؛ پنل را ری‌استارت کنید.', 'Firewall rules are not loaded; restart the panel.') },
} as Record<string, { t: string; h: string }>)[id] || { t: id, h: '' };

const incidentLabel = (k: string) => ({
  line: L('قطعی کامل خط', 'Line outage'),
  international: L('قطعی اینترنت بین‌الملل', 'International outage'),
  tunnel: L('قطعی تونل', 'Tunnel outage'),
} as Record<string, string>)[k] || k;

function dur(sec: number) {
  if (sec < 60) return L(`${num(sec)} ثانیه`, `${sec}s`);
  if (sec < 3600) return L(`${num(Math.round(sec / 60))} دقیقه`, `${Math.round(sec / 60)} min`);
  return L(`${num(sec / 3600, 1)} ساعت`, `${(sec / 3600).toFixed(1)} h`);
}

function Diagnostics() {
  const [rep, setRep] = useState<DiagReport | null>(null);
  const [busy, setBusy] = useState(false);
  const inc = usePoll<Incident[]>('/incidents', 0);
  const runDiag = async () => {
    setBusy(true);
    const r = await run(() => get<DiagReport>('/diag'));
    if (r) setRep(r);
    setBusy(false);
  };
  useEffect(() => { runDiag(); }, []);
  const root = rep?.steps.find((s) => s.id === rep.rootCause);
  return (
    <div class="stack">
      <Card>
        <CardHead icon={<Stethoscope size={20} />} title={L('عیب‌یابی لایه‌به‌لایه', 'Layer-by-layer diagnosis')}
          sub={L('از کابل مودم تا DNS داخل تونل؛ اولین حلقهٔ خراب، ریشهٔ مشکل است', 'From the modem cable to DNS inside the tunnel; the first broken link is the root cause')}
          action={<Btn sm kind="primary" busy={busy} onClick={runDiag}><Play size={15} />{L('بررسی دوباره', 'Run again')}</Btn>} />
        {rep && (
          <div class="card-body" style="padding-bottom:6px">
            {root ? (
              <div class="callout danger"><XCircle size={20} /><div>
                <b>{L('ریشهٔ مشکل: ', 'Root cause: ')}{stepInfo(root.id).t}</b>
                {stepInfo(root.id).h}
                {root.detail && <div class="tiny mono" style="margin-top:4px">{root.detail}</div>}
              </div></div>
            ) : (
              <div class="callout ok"><CheckCircle2 size={20} /><div><b>{L('همه چیز سالم است', 'Everything is healthy')}</b>{L('همهٔ حلقه‌های اتصال جواب دادند.', 'Every link in the chain answered.')}</div></div>
            )}
          </div>
        )}
        {!rep && <div class="card-body"><Skeleton h={200} /></div>}
        {rep?.steps.map((s, i) => (
          <div class="list-item">
            <span class="faint small" style="width:18px">{num(i + 1)}</span>
            {s.status === 'ok' ? <CheckCircle2 size={20} style="color:var(--ok)" /> : s.status === 'fail' ? <XCircle size={20} style="color:var(--danger-strong)" /> : <CircleSlash size={20} class="faint" />}
            <div class="grow" style="min-width:0">
              <div style="font-weight:700">{stepInfo(s.id).t}</div>
              {s.detail && <div class="tiny faint truncate ltr" style="text-align:start">{s.detail}</div>}
            </div>
            {s.status === 'skip' ? <span class="tiny faint">{L('رد شد', 'skipped')}</span> : s.ms ? <span class="small mono faint">{s.ms} ms</span> : null}
          </div>
        ))}
      </Card>
      <Card>
        <CardHead icon={<History size={20} />} title={L('تاریخچهٔ مشکلات', 'Problem history')} sub={L('قطعی‌های ۶۰ روز گذشته که روتر ثبت کرده', 'Outages recorded by the router over the last 60 days')} />
        {inc.data && inc.data.length === 0 && <div class="card-body muted">{L('مشکلی ثبت نشده است.', 'No problems recorded.')}</div>}
        {(inc.data || []).map((x) => (
          <div class="list-item">
            <span class={'dot' + (x.end ? ' warn' : ' bad pulse')} />
            <div class="grow">
              <div style="font-weight:700">{incidentLabel(x.kind)}</div>
              <div class="tiny faint">{ago(x.start)} · {clock(x.start)}</div>
            </div>
            {x.end ? <Badge>{dur(x.end - x.start)}</Badge> : <Badge tone="danger">{L('ادامه دارد', 'ongoing')}</Badge>}
          </div>
        ))}
      </Card>
    </div>
  );
}

export function Internet() {
  const initial = location.hash.includes('speed') ? 'speed' : location.hash.includes('diag') ? 'diag' : location.hash.includes('setup') ? 'setup' : 'quality';
  const [tab, setTab] = useState<'quality' | 'speed' | 'diag' | 'setup'>(initial);
  lang.value;
  return (
    <div class="stack">
      <PageHead title={L('اینترنت', 'Internet')} sub={L('اتصال به اینترنت، سرعت واقعی خط و کیفیت آن در طول زمان', 'Your connection, real line speed and quality over time')} />
      <Tabs value={tab} onChange={setTab} items={[
        { id: 'quality', label: L('کیفیت خط', 'Line quality'), icon: <LineIcon size={17} /> },
        { id: 'speed', label: L('تست سرعت', 'Speed test'), icon: <GaugeIcon size={17} /> },
        { id: 'diag', label: L('عیب‌یابی', 'Diagnose'), icon: <Stethoscope size={17} /> },
        { id: 'setup', label: L('تنظیم اتصال', 'Connection setup'), icon: <Settings2 size={17} /> },
      ]} />
      {tab === 'quality' ? <Quality /> : tab === 'speed' ? <SpeedTest /> : tab === 'diag' ? <Diagnostics /> : <div class="stack"><WanCard /><UplinkCard /></div>}
    </div>
  );
}
