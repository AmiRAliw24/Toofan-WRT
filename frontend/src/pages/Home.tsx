import type { ComponentChildren } from 'preact';
import {
  Activity, Gamepad2, ArrowDown, ArrowUp, Building2, ChevronLeft, ChevronRight, Clock, Cpu, Gauge as GaugeIcon,
  Globe, KeyRound, Power, RotateCw, Router, Server as ServerIcon, Shield, ShieldAlert, ShieldCheck,
  ShieldOff, Smartphone, Stethoscope, Users, Zap,
} from 'lucide-preact';
import { post, type Overview, type ProxyStatus } from '../api';
import { go, overview } from '../app';
import { L, duration, lang, num, pct, rate } from '../i18n';
import { Badge, Card, CardHead, Meter, Skeleton, confirmDialog, run, Btn } from '../ui';
import { DeviceRow } from './Devices';

export function modeInfo(p: ProxyStatus, wanUp: boolean) {
  const name = p.server?.name || '';
  if (!wanUp) return { tone: 'bad', title: L('اینترنت قطع است', 'Internet is down'), icon: <ShieldOff size={34} /> };
  switch (p.mode) {
    case 'connected':
      return { tone: 'ok', title: L('اینترنت آزاد وصل است', 'Free internet is connected'), icon: <ShieldCheck size={34} />, server: name };
    case 'connecting':
      return { tone: 'warn', title: L('در حال اتصال…', 'Connecting…'), icon: <Shield size={34} class="pulse" /> };
    case 'degraded':
      return { tone: 'warn', title: L('اتصال ناپایدار است؛ نگهبان در حال ترمیم', 'Tunnel unstable; watchdog is recovering'), icon: <ShieldAlert size={34} /> };
    case 'fallback':
      return { tone: 'warn', title: L('سرورها در دسترس نیستند؛ اینترنت مستقیم', 'Servers unreachable; direct internet'), icon: <ShieldAlert size={34} /> };
    case 'killswitch':
      return { tone: 'bad', title: L('قطع اضطراری فعال است', 'Kill switch engaged'), icon: <ShieldOff size={34} /> };
    case 'safe':
      return { tone: 'bad', title: L('حالت امن: موتور اتصال متوقف شد', 'Safe mode: engine stopped'), icon: <ShieldAlert size={34} /> };
    default:
      return { tone: 'off', title: L('اینترنت مستقیم (فیلترشکن خاموش)', 'Direct internet (proxy off)'), icon: <Globe size={34} /> };
  }
}

function Hop(p: { icon: ComponentChildren; title: string; sub: ComponentChildren; state: 'ok' | 'bad' | 'off' }) {
  return (
    <div class={'hop' + (p.state === 'ok' ? '' : ' ' + p.state)}>
      <span class={'dot' + (p.state === 'bad' ? ' bad' : p.state === 'off' ? ' off' : '')} />
      <div class="grow" style="min-width:0">
        <div class="hop-title truncate">{p.title}</div>
        <div class="hop-sub truncate">{p.sub}</div>
      </div>
      <span class="faint">{p.icon}</span>
    </div>
  );
}

function Rate(p: { dir: 'down' | 'up'; bps: number }) {
  const r = rate(p.bps);
  return (
    <div class="rate">
      <div class="rate-icon">{p.dir === 'down' ? <ArrowDown size={18} /> : <ArrowUp size={18} />}</div>
      <div>
        <div class="rate-val ltr">{r.v} <span class="small">{r.u}</span></div>
        <div class="rate-label">{p.dir === 'down' ? L('دانلود', 'Download') : L('آپلود', 'Upload')}</div>
      </div>
    </div>
  );
}

function flag(cc?: string) {
  if (!cc || cc.length !== 2) return '';
  return String.fromCodePoint(...[...cc.toUpperCase()].map((c) => 0x1f1e6 + c.charCodeAt(0) - 65));
}

function maskIP(ip: string) {
  if (!ip) return '—';
  const p = ip.split('.');
  return p.length === 4 ? `${p[0]}.${p[1]}.•••.•••` : ip;
}

function Hero({ o }: { o: Overview }) {
  const m = modeInfo(o.proxy, o.wan.up);
  const extOk = o.quality.ext >= 0;
  const domOk = o.quality.dom >= 0;
  const sub =
    o.proxy.mode === 'connected'
      ? (
          <>
            {L('ترافیک جهانی از ', 'Global traffic goes through ')}
            <bdi style="font-weight:700">{m.server}</bdi>
            {L(' عبور می‌کند', '')}
            {o.split ? L(' • سایت‌های ایرانی مستقیم', ' • Iranian sites direct') : ''}
          </>
        )
      : o.proxy.lastError && o.proxy.mode !== 'off'
        ? o.proxy.lastError
        : L('همهٔ دستگاه‌ها مستقیم به اینترنت وصل‌اند', 'All devices use the internet directly');
  return (
    <Card class={'hero' + (m.tone === 'bad' ? ' bad' : m.tone === 'warn' ? ' warn' : '')}>
      <div class="hero-main">
        <div class={'status-ring ' + (m.tone === 'ok' ? '' : m.tone)}>{m.icon}</div>
        <div class="grow">
          <h2 class="hero-title">{m.title}</h2>
          <div class="muted small">{sub}</div>
        </div>
        <div class="rates">
          <Rate dir="down" bps={o.wan.rxBps} />
          <Rate dir="up" bps={o.wan.txBps} />
        </div>
      </div>
      <div class="chain">
        <Hop icon={<Router size={18} />} title={L('مودم / خط', 'Modem / line')} sub={<span class="mono">{o.wan.gateway || '—'}</span>} state={o.wan.up ? 'ok' : 'bad'} />
        <Hop icon={<ServerIcon size={18} />}
          title={o.operator?.isp ? (lang.value === 'fa' && o.operator.ispFa ? o.operator.ispFa : o.operator.isp) : L('اپراتور', 'Operator')}
          sub={<span class="mono">{o.operator?.ip || maskIP(o.wan.ip)}</span>} state={o.wan.up ? 'ok' : 'bad'} />
        <Hop
          icon={<Building2 size={18} />}
          title={L('شبکهٔ ملی (ایران)', 'Domestic network')}
          sub={domOk ? L(`پینگ ${num(o.quality.dom)}ms`, `${o.quality.dom} ms`) : L('بدون پاسخ', 'no response')}
          state={domOk ? 'ok' : o.wan.up ? 'bad' : 'off'}
        />
        <Hop
          icon={<Globe size={18} />}
          title={L('اینترنت بین‌الملل', 'International')}
          sub={
            extOk
              ? (o.proxy.mode === 'connected'
                  ? (o.exit?.ip ? `${flag(o.exit.countryCode)} ${o.exit.ip} · ` : L('از تونل · ', 'via tunnel · '))
                  : '') + num(o.quality.ext) + 'ms'
              : L('بدون پاسخ', 'no response')
          }
          state={extOk ? 'ok' : o.wan.up ? 'bad' : 'off'}
        />
      </div>
    </Card>
  );
}

function Stat(p: { icon: ComponentChildren; label: string; value: ComponentChildren; sub?: ComponentChildren; children?: ComponentChildren }) {
  return (
    <Card class="stat">
      <div class="stat-label">{p.icon}{p.label}</div>
      <div class="stat-value">{p.value}</div>
      {p.sub && <div class="stat-sub">{p.sub}</div>}
      {p.children}
    </Card>
  );
}

function Quick(p: { icon: ComponentChildren; label: string; onClick: () => void }) {
  return (
    <button class="quick-btn" onClick={p.onClick}>
      {p.icon}
      <span>{p.label}</span>
    </button>
  );
}

function HealthRow(p: { label: string; value: string; tone: 'ok' | 'warn' | 'bad' | 'off' }) {
  const color = { ok: 'var(--ok)', warn: 'var(--warn)', bad: 'var(--danger-strong)', off: 'var(--text-3)' }[p.tone];
  return (
    <div class="kv">
      <span class="k">{p.label}</span>
      <span class="v" style={{ color }}>{p.value}</span>
    </div>
  );
}

export function Home() {
  const o = overview.value;
  lang.value;
  if (!o) {
    return (
      <div class="stack">
        <Skeleton h={230} />
        <div class="grid g4"><Skeleton /><Skeleton /><Skeleton /><Skeleton /></div>
      </div>
    );
  }
  const p = o.proxy;
  const proxyOn = p.enabled;
  const Chev = lang.value === 'fa' ? ChevronLeft : ChevronRight;

  const toggleProxy = async () => {
    if (proxyOn && !(await confirmDialog(L('اینترنت آزاد برای همهٔ دستگاه‌ها خاموش شود؟', 'Turn off the free internet for all devices?')))) return;
    await run(() => post('/proxy/settings', { enabled: !proxyOn }), proxyOn ? L('خاموش شد', 'Turned off') : L('روشن شد', 'Turned on'));
  };

  const tunnelTone = p.mode === 'connected' ? 'ok' : p.mode === 'off' ? 'off' : p.mode === 'connecting' || p.mode === 'degraded' ? 'warn' : 'bad';
  const tunnelText = {
    connected: L('فعال', 'Active'),
    connecting: L('در حال اتصال', 'Connecting'),
    degraded: L('ناپایدار', 'Unstable'),
    fallback: L('مستقیم (پشتیبان)', 'Direct (fallback)'),
    killswitch: L('قطع اضطراری', 'Kill switch'),
    safe: L('حالت امن', 'Safe mode'),
    off: L('خاموش', 'Off'),
  }[p.mode];

  return (
    <div class="stack">
      <Hero o={o} />
      {o.fwError && (
        <div class="callout danger">
          <ShieldAlert size={20} />
          <div><b>{L('خطای فایروال', 'Firewall error')}</b><span class="mono">{o.fwError}</span></div>
        </div>
      )}

      <div class="grid g4">
        <Stat icon={<Smartphone size={17} />} label={L('دستگاه متصل', 'Devices online')} value={num(o.devices.online)}
          sub={L(`${num(o.devices.total)} دستگاه شناخته‌شده`, `${o.devices.total} known`)} />
        <Stat icon={<Shield size={17} />} label={L('اینترنت آزاد', 'Free internet')}
          value={proxyOn ? L('روشن', 'On') : L('خاموش', 'Off')}
          sub={p.server ? <span class="truncate" style="display:block">{p.server.name}{p.latencyMs > 0 ? ` · ${num(p.latencyMs)}ms` : ''}</span> : '—'} />
        <Stat icon={<Cpu size={17} />} label={L('بار پردازنده', 'CPU load')} value={pct(o.system.cpuPercent)}
          sub={L(`${num(o.system.cores)} هسته`, `${o.system.cores} cores`)}>
          <Meter value={o.system.cpuPercent} color={o.system.cpuPercent > 85 ? 'var(--danger-strong)' : undefined} />
        </Stat>
        <Stat icon={<Clock size={17} />} label={L('روشن از', 'Uptime')} value={<span style="font-size:22px">{duration(o.system.uptimeSec)}</span>} />
      </div>

      <Card>
        <CardHead icon={<Zap size={20} />} title={L('کارهای پرکاربرد', 'Quick actions')} sub={L('آنچه بیشتر از همه لازم می‌شود، یک کلیک دورتر', 'The things you need most, one click away')} />
        <div class="quick">
          <Quick icon={<GaugeIcon size={24} />} label={L('تست سرعت', 'Speed test')} onClick={() => (location.hash = '/internet?speed')} />
          <Quick icon={<Power size={24} />} label={proxyOn ? L('خاموش کردن فیلترشکن', 'Turn proxy off') : L('روشن کردن فیلترشکن', 'Turn proxy on')} onClick={toggleProxy} />
          <Quick icon={<KeyRound size={24} />} label={L('رمز وای‌فای', 'Wi-Fi password')} onClick={() => go('wifi')} />
          <Quick icon={<Users size={24} />} label={L('دستگاه‌ها', 'Devices')} onClick={() => go('devices')} />
          <Quick icon={<Gamepad2 size={24} />} label={L('بازی', 'Gaming')} onClick={() => go('gaming')} />
          <Quick icon={<Stethoscope size={24} />} label={L('عیب‌یابی', 'Diagnose')} onClick={() => (location.hash = '/internet?diag')} />
          <Quick icon={<RotateCw size={24} />} label={L('راه‌اندازی مجدد', 'Restart')} onClick={async () => {
            if (await confirmDialog(L('روتر راه‌اندازی مجدد شود؟ حدود یک دقیقه اینترنت قطع می‌شود.', 'Restart the router? Internet drops for about a minute.'), true)) {
              run(() => post('/system/reboot'), L('در حال راه‌اندازی مجدد…', 'Restarting…'));
            }
          }} />
        </div>
      </Card>

      <div class="grid g2">
        <Card>
          <CardHead icon={<Smartphone size={20} />} title={L('پرمصرف‌ترین دستگاه‌ها', 'Top devices')}
            action={<Btn sm onClick={() => go('devices')}>{L('همه', 'All')} <Chev size={16} /></Btn>} />
          {o.top.length === 0 && <div class="empty">{L('دستگاهی پیدا نشد', 'No devices yet')}</div>}
          {o.top.map((d) => <DeviceRow d={d} compact />)}
        </Card>
        <Card>
          <CardHead icon={<Activity size={20} />} title={L('سلامت اتصال', 'Connection health')}
            action={<Btn sm onClick={() => go('internet')}>{L('جزئیات', 'Details')} <Chev size={16} /></Btn>} />
          <div class="card-body" style="padding-top:4px;padding-bottom:4px">
            <HealthRow label={L('اتصال به اینترنت', 'Internet link')} value={o.wan.up ? L('برقرار', 'Up') : L('قطع', 'Down')} tone={o.wan.up ? 'ok' : 'bad'} />
            <HealthRow label={L('تونل', 'Tunnel')} value={tunnelText} tone={tunnelTone} />
            <HealthRow label={L('شبکهٔ داخلی', 'Domestic')} value={o.quality.dom >= 0 ? num(o.quality.dom) + ' ms' : L('بدون پاسخ', 'No response')} tone={o.quality.dom >= 0 ? 'ok' : 'bad'} />
            <HealthRow label={L('شبکهٔ خارجی', 'International')} value={o.quality.ext >= 0 ? num(o.quality.ext) + ' ms' : L('بدون پاسخ', 'No response')} tone={o.quality.ext >= 0 ? 'ok' : 'bad'} />
            <HealthRow label={L('فایروال', 'Firewall')} value={o.fwError ? L('خطا', 'Error') : L('سالم', 'Healthy')} tone={o.fwError ? 'bad' : 'ok'} />
            <HealthRow label={L('حافظه', 'Memory')}
              value={pct((1 - o.system.memAvailKb / Math.max(1, o.system.memTotalKb)) * 100)}
              tone={o.system.memAvailKb / Math.max(1, o.system.memTotalKb) < 0.1 ? 'warn' : 'ok'} />
          </div>
        </Card>
      </div>
      {p.mode === 'killswitch' && (
        <div class="callout danger"><ShieldOff size={20} /><div>
          <b>{L('قطع اضطراری', 'Kill switch')}</b>
          {L('هیچ سروری جواب نمی‌دهد و چون قطع اضطراری روشن است، ترافیک خارجی مسدود شده. سایت‌های ایرانی کار می‌کنند.', 'No server responds and the kill switch is on, so foreign traffic is blocked. Iranian sites still work.')}
        </div></div>
      )}
      <div class="tiny faint" style="text-align:center">
        <Badge>{o.system.firmware}</Badge>
      </div>
    </div>
  );
}
