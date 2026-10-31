import { useEffect, useState } from 'preact/hooks';
import type { ComponentChildren } from 'preact';
import {
  ArrowDown, ArrowUp, Ban, Cable, CalendarClock, Camera, Cpu, Gamepad2, Laptop, Monitor, Plus,
  ShieldOff, SlidersHorizontal, Smartphone, Tablet, Trash2, Tv, Wifi, Zap, HelpCircle, Users, Globe, Info, Power,
} from 'lucide-preact';
import { del, get, post, usePoll, type Device, type Schedule, type SitesResp } from '../api';
import { onRefresh } from '../app';
import { L, ago, bytes, dateLocale, dayNames, duration, lang, num, rate, weekOrder } from '../i18n';
import { Badge, Btn, Card, CardHead, Empty, Field, Modal, PageHead, Seg, Setting, Skeleton, Tabs, Toggle, confirmDialog, run } from '../ui';

export const deviceTypes = () => [
  { id: 'phone', label: L('گوشی', 'Phone'), icon: Smartphone },
  { id: 'laptop', label: L('لپ‌تاپ', 'Laptop'), icon: Laptop },
  { id: 'computer', label: L('کامپیوتر', 'Computer'), icon: Monitor },
  { id: 'tablet', label: L('تبلت', 'Tablet'), icon: Tablet },
  { id: 'tv', label: L('تلویزیون', 'TV'), icon: Tv },
  { id: 'console', label: L('کنسول بازی', 'Console'), icon: Gamepad2 },
  { id: 'camera', label: L('دوربین', 'Camera'), icon: Camera },
  { id: 'iot', label: L('اینترنت اشیا', 'Smart device'), icon: Cpu },
  { id: 'unknown', label: L('نامشخص', 'Unknown'), icon: HelpCircle },
];

function TypeIcon({ type, size = 20 }: { type: string; size?: number }) {
  const t = deviceTypes().find((x) => x.id === type) || deviceTypes()[8];
  const I = t.icon;
  return <I size={size} />;
}

function linkLabel(d: Device) {
  switch (d.link) {
    case 'wifi5': return { icon: <Wifi size={13} />, text: L('وای‌فای ۵ گیگاهرتز', 'Wi-Fi 5 GHz') };
    case 'wifi24': return { icon: <Wifi size={13} />, text: L('وای‌فای ۲٫۴ گیگاهرتز', 'Wi-Fi 2.4 GHz') };
    case 'lan': return { icon: <Cable size={13} />, text: L('کابل شبکه', 'Ethernet') };
    case 'guest': return { icon: <Users size={13} />, text: L('شبکهٔ مهمان', 'Guest') };
    default: return { icon: <HelpCircle size={13} />, text: L('نامشخص', 'Unknown') };
  }
}

function SpeedChip({ dir, bps }: { dir: 'down' | 'up'; bps: number }) {
  const r = rate(bps);
  return (
    <span class={'speed-chip' + (bps < 50 ? ' idle' : '')}>
      {dir === 'down' ? <ArrowDown size={12} /> : <ArrowUp size={12} />}
      <span>{r.v} {r.u}</span>
    </span>
  );
}

export function DeviceRow({ d, compact, onEdit }: { d: Device; compact?: boolean; onEdit?: () => void }) {
  const l = linkLabel(d);
  return (
    <div class={'list-item' + (onEdit ? ' clickable' : '')} onClick={onEdit}>
      <div class="dev-icon">
        <TypeIcon type={d.type} />
        <span class={'dot' + (d.blocked ? ' bad' : d.online ? '' : ' off')} />
      </div>
      <div class="grow" style="min-width:0">
        <div class="dev-name truncate">{d.name || d.hostname || d.mac}</div>
        <div class="chips">
          {d.ip && <span class="badge mono">{d.ip}</span>}
          {!compact && <span class="badge">{l.icon}{l.text}</span>}
          {d.blocked && <Badge tone="danger"><Ban size={12} />{L('مسدود', 'Blocked')}</Badge>}
          {d.direct && <Badge tone="ok"><ShieldOff size={12} />{L('مستقیم (بدون فیلترشکن)', 'Direct (no proxy)')}</Badge>}
          {d.priority && <Badge tone="violet"><Zap size={12} />{L('اولویت', 'Priority')}</Badge>}
          {(d.downKbps > 0 || d.upKbps > 0) && <Badge tone="info"><SlidersHorizontal size={12} />{L('محدود', 'Limited')}</Badge>}
          {!compact && d.randomMac && <Badge tone="warn" title={L('این دستگاه آدرس MAC تصادفی دارد؛ ممکن است بعد از مدتی با آدرس جدید ظاهر شود', 'Private (random) MAC: may reappear with a new address')}>{L('مک تصادفی', 'Random MAC')}</Badge>}
          {!d.online && <span class="badge">{L('آفلاین · ', 'offline · ')}{ago(d.lastSeen)}</span>}
        </div>
      </div>
      {d.online && (
        <div class="speeds">
          <SpeedChip dir="down" bps={d.downBps} />
          <SpeedChip dir="up" bps={d.upBps} />
        </div>
      )}
      {onEdit && <SlidersHorizontal size={18} class="faint hide-sm" />}
    </div>
  );
}

const limitPresets = [0, 1000, 2000, 5000, 10000, 20000];

interface DayUsage { day: string; down: number; up: number }
interface DeviceInfo {
  days: DayUsage[]; today: DayUsage; month: { down: number; up: number };
  staticIp: string; connections: number; sites: number; firstSeen: number;
}

type DevTab = 'settings' | 'info' | 'sites';

function signalText(dbm: number) {
  if (dbm >= -55) return { t: L('عالی', 'Excellent'), tone: 'ok' as const };
  if (dbm >= -67) return { t: L('خوب', 'Good'), tone: 'ok' as const };
  if (dbm >= -75) return { t: L('متوسط', 'Fair'), tone: 'warn' as const };
  return { t: L('ضعیف', 'Weak'), tone: 'danger' as const };
}

/** UsageBars: 30 days of download, one bar per day, hover shows the day. */
function UsageBars({ days }: { days: DayUsage[] }) {
  const [hover, setHover] = useState<number | null>(null);
  const max = Math.max(1, ...days.map((d) => d.down));
  const sel = hover !== null ? days[hover] : days[days.length - 1];
  const fmtDay = (d: string) => new Date(d + 'T12:00:00').toLocaleDateString(dateLocale(), { month: 'short', day: 'numeric' });
  return (
    <div class="stack" style="gap:6px">
      <div class="row small" style="justify-content:space-between">
        <span class="muted">{hover === null ? L('امروز', 'Today') : fmtDay(sel.day)}</span>
        <span class="ltr num"><b>↓ {bytes(sel.down)}</b> <span class="muted">· ↑ {bytes(sel.up)}</span></span>
      </div>
      <div class="usage-bars" role="img" aria-label={L('مصرف روزانهٔ ۳۰ روز اخیر', 'Daily usage, last 30 days')} onMouseLeave={() => setHover(null)}>
        {days.map((d, i) => (
          <div key={d.day} class={'ub' + (hover !== null && hover !== i ? ' dim' : '')} onMouseEnter={() => setHover(i)} onClick={() => setHover(i)}>
            {d.down > 0 && <span style={{ height: `${Math.max(3, (d.down / max) * 100)}%` }} />}
          </div>
        ))}
      </div>
      <div class="row tiny faint" style="justify-content:space-between;direction:ltr">
        <span>{fmtDay(days[0].day)}</span><span>{fmtDay(days[days.length - 1].day)}</span>
      </div>
    </div>
  );
}

// The site's own name, without www./api. and the TLD: "www.digikala.com" -> "D".
function siteLetter(domain: string) {
  const parts = domain.split('.');
  const main = parts.length >= 2 ? parts[parts.length - 2] : parts[0];
  return (main[0] || '?').toUpperCase();
}

function SitesList({ mac, compact }: { mac?: string; compact?: boolean }) {
  const { data, reload } = usePoll<SitesResp>(mac ? `/sites?mac=${mac}` : '/sites', 5000);
  const [q, setQ] = useState('');
  if (!data) return <Skeleton h={120} />;
  if (!data.enabled) {
    return (
      <div class="stack" style="align-items:flex-start">
        <div class="small muted">{L('ثبت سایت‌ها خاموش است. با روشن کردنش، سایت‌هایی که هر دستگاه باز می‌کند از همین لحظه اینجا دیده می‌شوند.', 'Site logging is off. Turn it on to see, from now on, which sites each device opens.')}</div>
        <Btn kind="primary" sm onClick={async () => { await run(() => post('/sites', { enabled: true }), L('روشن شد', 'Turned on')); reload(); }}>
          <Globe size={15} />{L('روشن کردن ثبت سایت‌ها', 'Turn on site logging')}</Btn>
      </div>
    );
  }
  const list = data.sites.filter((s) => !q || s.domain.includes(q.toLowerCase()));
  const shown = list.slice(0, compact ? 60 : 300);
  return (
    <div class="stack" style="gap:10px">
      <div class="row" style="gap:8px">
        <input class="input grow ltr" style="height:40px" placeholder={L('جستجوی سایت…', 'Search sites…')} value={q} onInput={(e) => setQ((e.target as HTMLInputElement).value)} />
        <Btn sm kind="ghost" onClick={async () => {
          if (await confirmDialog(mac ? L('فهرست سایت‌های این دستگاه پاک شود؟', 'Clear this device’s sites?') : L('فهرست سایت‌های همهٔ دستگاه‌ها پاک شود؟', 'Clear sites of all devices?'), true)) {
            await run(() => del(mac ? `/sites?mac=${mac}` : '/sites'));
            reload();
          }
        }}><Trash2 size={15} />{L('پاک کردن', 'Clear')}</Btn>
      </div>
      <div class="tiny faint">{L(`از ${ago(data.since)} · ${num(list.length)} سایت`, `Since ${ago(data.since)} · ${list.length} sites`)}</div>
      {shown.length === 0 && <div class="small muted">{L('هنوز سایتی ثبت نشده است.', 'No sites recorded yet.')}</div>}
      <div class="site-list">
        {shown.map((s) => (
          <div class="site-row" key={s.domain}>
            <span class="site-fav" aria-hidden="true">{siteLetter(s.domain)}</span>
            <span class="grow ltr truncate site-domain" title={s.domain}>{s.domain}</span>
            {s.devices && s.devices > 1 && <span class="badge tiny">{L(`${num(s.devices)} دستگاه`, `${s.devices} devices`)}</span>}
            <span class="tiny faint num" title={L('تعداد درخواست', 'Requests')}>×{num(s.count)}</span>
            <span class="tiny muted site-when">{ago(s.last)}</span>
          </div>
        ))}
      </div>
      {list.length > shown.length && <div class="tiny faint">{L(`و ${num(list.length - shown.length)} سایت دیگر؛ برای پیدا کردن جستجو کنید`, `and ${list.length - shown.length} more; use search`)}</div>}
    </div>
  );
}

function DeviceModal({ d, onClose, onSaved }: { d: Device; onClose: () => void; onSaved: () => void }) {
  const [tab, setTab] = useState<DevTab>('settings');
  const [info, setInfo] = useState<DeviceInfo | null>(null);
  const [f, setF] = useState({
    name: d.name || d.hostname || '',
    type: d.type,
    blocked: d.blocked,
    direct: d.direct,
    priority: d.priority,
    downKbps: d.downKbps,
    upKbps: d.upKbps,
    staticIp: '',
    fixed: false,
  });
  const [busy, setBusy] = useState(false);
  const [waking, setWaking] = useState(false);
  useEffect(() => {
    get<DeviceInfo>(`/devices/${d.mac}/info`).then((i) => {
      setInfo(i);
      setF((cur) => ({ ...cur, staticIp: i.staticIp || d.ip, fixed: !!i.staticIp }));
    }).catch(() => {});
  }, [d.mac]);
  const up = (p: Partial<typeof f>) => setF({ ...f, ...p });
  const save = async () => {
    setBusy(true);
    const { fixed, staticIp, ...rest } = f;
    const body: Record<string, unknown> = { ...rest };
    if (info && (fixed ? staticIp : '') !== (info.staticIp || '')) body.staticIp = fixed ? staticIp.trim() : '';
    const ok = await run(() => post(`/devices/${d.mac}`, body), L('ذخیره شد', 'Saved'));
    setBusy(false);
    if (ok) {
      onSaved();
      onClose();
    }
  };
  const mbps = (k: number) => (k === 0 ? L('بدون محدودیت', 'Unlimited') : `${num(k / 1000)} Mbps`);
  const wifi = d.link === 'wifi5' || d.link === 'wifi24' || (d.link === 'guest' && !!d.ssid);
  const sig = d.signal ? signalText(d.signal) : null;
  const kv = (k: string, v: ComponentChildren) => <div class="kv"><span class="k">{k}</span><span class="v">{v}</span></div>;

  return (
    <Modal wide title={<span class="row" style="gap:10px"><TypeIcon type={f.type} />{d.name || d.hostname || d.mac}</span>} onClose={onClose}
      foot={tab === 'settings' ? <><Btn onClick={onClose}>{L('انصراف', 'Cancel')}</Btn><Btn kind="primary" busy={busy} onClick={save}>{L('ذخیره', 'Save')}</Btn></> : undefined}>
      <Tabs value={tab} onChange={setTab} items={[
        { id: 'settings', label: L('تنظیمات', 'Settings'), icon: <SlidersHorizontal size={16} /> },
        { id: 'info', label: L('اطلاعات', 'Details'), icon: <Info size={16} /> },
        { id: 'sites', label: L('سایت‌ها', 'Sites'), icon: <Globe size={16} /> },
      ]} />

      {tab === 'settings' && (
        <>
          <div class="grid g2" style="gap:12px">
            <Field label={L('نام', 'Name')}>
              <input class="input" value={f.name} maxLength={64} onInput={(e) => up({ name: (e.target as HTMLInputElement).value })} />
            </Field>
            <Field label={L('نوع', 'Type')}>
              <select class="select" value={f.type} onChange={(e) => up({ type: (e.target as HTMLSelectElement).value })}>
                {deviceTypes().map((t) => <option value={t.id}>{t.label}</option>)}
              </select>
            </Field>
          </div>
          <div>
            <Setting title={L('مسدود کردن اینترنت', 'Block internet')} desc={L('دستگاه واقعاً قطع می‌شود؛ فقط آدرس IP می‌گیرد', 'Fully cut off; it only keeps getting an IP')}>
              <Toggle on={f.blocked} onChange={(v) => up({ blocked: v })} />
            </Setting>
            <Setting title={L('مستقیم، بدون فیلترشکن', 'Direct, no proxy')} desc={L('برای تلویزیون، کنسول یا برنامه‌های بانکی', 'For TVs, consoles or banking apps')}>
              <Toggle on={f.direct} onChange={(v) => up({ direct: v })} />
            </Setting>
            <Setting title={L('اولویت ترافیک', 'Traffic priority')} desc={L('بسته‌های این دستگاه برچسب اولویت می‌گیرند تا پینگ بازی پایین بماند', 'Packets are tagged high-priority to keep gaming ping low')}>
              <Toggle on={f.priority} onChange={(v) => up({ priority: v })} />
            </Setting>
            <Setting title={L('آدرس IP ثابت', 'Fixed IP address')} desc={L('روتر همیشه همین آدرس را به این دستگاه می‌دهد (برای پورت‌فوروارد، دوربین، پرینتر). از اتصال بعدی دستگاه اعمال می‌شود.', 'The router always gives this device the same address (port forwarding, cameras, printers). Applies from its next connection.')}>
              <Toggle on={f.fixed} onChange={(v) => up({ fixed: v })} />
            </Setting>
            {f.fixed && (
              <Field label={L('آدرس ثابت', 'Fixed address')}>
                <input class="input ltr mono" value={f.staticIp} placeholder="192.168.1.50" onInput={(e) => up({ staticIp: (e.target as HTMLInputElement).value })} />
              </Field>
            )}
          </div>
          <Field label={L('سقف سرعت دانلود', 'Download limit')}>
            <div class="row wrap" style="gap:6px">
              {limitPresets.map((k) => (
                <button class={'btn sm' + (f.downKbps === k ? ' primary' : '')} onClick={() => up({ downKbps: k })}>{mbps(k)}</button>
              ))}
            </div>
          </Field>
          <Field label={L('سقف سرعت آپلود', 'Upload limit')}>
            <div class="row wrap" style="gap:6px">
              {limitPresets.map((k) => (
                <button class={'btn sm' + (f.upKbps === k ? ' primary' : '')} onClick={() => up({ upKbps: k })}>{mbps(k)}</button>
              ))}
            </div>
          </Field>
          {!d.online && !d.randomMac && (
            <div class="row" style="gap:10px">
              <Btn busy={waking} onClick={async () => {
                setWaking(true);
                await run(() => post(`/devices/${d.mac}/wake`), L('سیگنال روشن شدن فرستاده شد', 'Wake signal sent'));
                setWaking(false);
              }}><Power size={16} />{L('روشن کردن از راه دور (Wake-on-LAN)', 'Wake up (Wake-on-LAN)')}</Btn>
              <span class="tiny faint">{L('فقط برای کامپیوترهایی که این قابلیت را فعال کرده‌اند', 'Only for computers with Wake-on-LAN enabled')}</span>
            </div>
          )}
        </>
      )}

      {tab === 'info' && (
        <>
          <div class="grid g3 stat-mini">
            <div><div class="tiny muted">{L('مصرف امروز', 'Today')}</div><div class="num ltr"><b>{info ? bytes(info.today.down + info.today.up) : '…'}</b></div></div>
            <div><div class="tiny muted">{L('مصرف این ماه', 'This month')}</div><div class="num ltr"><b>{info ? bytes(info.month.down + info.month.up) : '…'}</b></div></div>
            <div><div class="tiny muted">{L('اتصال‌های باز', 'Open connections')}</div><div class="num"><b>{info ? (info.connections < 0 ? '—' : num(info.connections)) : '…'}</b></div></div>
          </div>
          {info && <UsageBars days={info.days} />}
          <div class="card" style="padding:4px 16px;box-shadow:none">
            {kv(L('سازنده', 'Manufacturer'), d.vendor || (d.randomMac ? L('نامشخص (آدرس تصادفی)', 'Unknown (private address)') : L('نامشخص', 'Unknown')))}
            {kv(L('نام شبکه‌ای', 'Hostname'), <span class="mono">{d.hostname || '—'}</span>)}
            {kv('MAC', <span>{d.randomMac ? <span class="faint small">{L('تصادفی', 'private')} · </span> : null}<span class="mono">{d.mac}</span></span>)}
            {kv('IP', <span>{info?.staticIp ? <span class="faint small">{L('ثابت', 'fixed')} · </span> : null}<span class="mono">{d.ip || '—'}</span></span>)}
            {d.leaseExpires ? kv(L('اعتبار آدرس (DHCP)', 'DHCP lease'), d.leaseExpires > Date.now() / 1000 ? L(`تا ${duration(d.leaseExpires - Date.now() / 1000)} دیگر`, `${duration(d.leaseExpires - Date.now() / 1000)} left`) : L('منقضی', 'expired')) : null}
            {kv(L('نوع اتصال', 'Connection'), linkLabel(d).text)}
            {wifi && d.ssid ? kv(L('نام وای‌فای', 'Wi-Fi network'), d.ssid) : null}
            {wifi && d.signal ? kv(L('قدرت سیگنال', 'Signal'), <span><span class="mono">{d.signal} dBm</span> {sig && <Badge tone={sig.tone}>{sig.t}</Badge>}</span>) : null}
            {wifi && d.noise ? kv(L('نویز', 'Noise'), <span class="mono">{d.noise} dBm</span>) : null}
            {wifi && d.wifiGen ? kv(L('استاندارد', 'Standard'), `Wi-Fi ${num(d.wifiGen)} (802.11${{ 4: 'n', 5: 'ac', 6: 'ax' }[d.wifiGen] || ''})`) : null}
            {wifi && (d.txMbps || d.rxMbps) ? kv(L('سرعت لینک وای‌فای', 'Wi-Fi link rate'), <span class="ltr num">↓ {num(Math.round(d.txMbps || 0))} · ↑ {num(Math.round(d.rxMbps || 0))} Mbps</span>) : null}
            {d.online && (d.connectedSec || d.onlineSince) ? kv(L('مدت اتصال', 'Connected for'), duration(d.connectedSec || Date.now() / 1000 - (d.onlineSince || 0))) : null}
            {!d.online ? kv(L('آخرین بار دیده‌شده', 'Last seen'), ago(d.lastSeen)) : null}
            {info?.firstSeen ? kv(L('اولین بار دیده‌شده', 'First seen'), ago(info.firstSeen)) : null}
            {kv(L('مصرف از روشن شدن پنل', 'Since panel start'), <span class="ltr">↓ {bytes(d.downTotal)} · ↑ {bytes(d.upTotal)}</span>)}
          </div>
        </>
      )}

      {tab === 'sites' && <SitesList mac={d.mac} />}
    </Modal>
  );
}

/** SiteLogCard: the network-wide view of visited sites, with a device filter. */
function SiteLogCard({ devices }: { devices: Device[] }) {
  const { data, reload } = usePoll<SitesResp>('/sites', 10000);
  const [mac, setMac] = useState('');
  const counts = data?.counts || {};
  const named = devices.filter((d) => counts[d.mac]);
  return (
    <Card>
      <CardHead icon={<Globe size={20} />} tone="info" title={L('ثبت سایت‌های بازشده', 'Visited sites')}
        sub={L('سایت‌هایی که هر دستگاه باز می‌کند. فقط در حافظهٔ موقت روتر می‌ماند و با خاموش شدن روتر پاک می‌شود.', 'Sites each device opens. Kept in the router’s RAM only and erased on reboot.')}
        action={data && <Toggle on={data.enabled} onChange={async (v) => { await run(() => post('/sites', { enabled: v }), L('ذخیره شد', 'Saved')); reload(); }} />} />
      {data?.enabled && (
        <div class="card-body stack">
          {named.length > 0 && (
            <div class="chip-row">
              <button class={'chip' + (mac === '' ? ' active' : '')} onClick={() => setMac('')}>{L('همهٔ دستگاه‌ها', 'All devices')}</button>
              {named.map((d) => (
                <button class={'chip' + (mac === d.mac ? ' active' : '')} onClick={() => setMac(d.mac)}>
                  <TypeIcon type={d.type} size={14} />{d.name || d.hostname || d.mac}<span class="faint num">{num(counts[d.mac])}</span>
                </button>
              ))}
            </div>
          )}
          <SitesList key={mac} mac={mac || undefined} compact />
          <div class="tiny faint">{L('دستگاه‌هایی که DNS خصوصی یا رمزشده (مثل Private DNS اندروید) روشن دارند اینجا دیده نمی‌شوند.', 'Devices using their own encrypted DNS (e.g. Android Private DNS) don’t show up here.')}</div>
        </div>
      )}
      {data && !data.enabled && (
        <div class="card-body small muted">{L('خاموش است. با روشن کردن، از همین لحظه سایت‌های هر دستگاه اینجا و در جزئیات همان دستگاه دیده می‌شود.', 'Off. Turn it on to see each device’s sites here and in that device’s details, from now on.')}</div>
      )}
    </Card>
  );
}

function ScheduleModal({ devices, s, onClose, onSaved }: { devices: Device[]; s?: Schedule; onClose: () => void; onSaved: () => void }) {
  const [f, setF] = useState<Schedule>(s || { id: '', macs: [], days: [0, 1, 2, 3, 6], from: '23:00', to: '07:00', enabled: true });
  const [busy, setBusy] = useState(false);
  const toggleDay = (d: number) => setF({ ...f, days: f.days.includes(d) ? f.days.filter((x) => x !== d) : [...f.days, d] });
  const toggleMac = (m: string) => setF({ ...f, macs: f.macs.includes(m) ? f.macs.filter((x) => x !== m) : [...f.macs, m] });
  const workdays = lang.value === 'fa' ? [6, 0, 1, 2, 3] : [1, 2, 3, 4, 5];
  const weekend = lang.value === 'fa' ? [4, 5] : [0, 6];
  return (
    <Modal title={s ? L('ویرایش زمان‌بندی', 'Edit schedule') : L('زمان‌بندی جدید', 'New schedule')} onClose={onClose}
      foot={<><Btn onClick={onClose}>{L('انصراف', 'Cancel')}</Btn>
        <Btn kind="primary" busy={busy} disabled={f.macs.length === 0 || f.days.length === 0} onClick={async () => {
          setBusy(true);
          const ok = await run(() => post('/schedules', f), L('ذخیره شد', 'Saved'));
          setBusy(false);
          if (ok) { onSaved(); onClose(); }
        }}>{L('ذخیره', 'Save')}</Btn></>}>
      <div class="callout info small">{L('در این بازه اینترنت دستگاه‌های انتخاب‌شده قطع می‌شود.', 'Internet is cut for the selected devices during this window.')}</div>
      <div class="grid g2" style="gap:12px">
        <Field label={L('از ساعت', 'From')}><input class="input ltr" type="time" value={f.from} onInput={(e) => setF({ ...f, from: (e.target as HTMLInputElement).value })} /></Field>
        <Field label={L('تا ساعت', 'To')}><input class="input ltr" type="time" value={f.to} onInput={(e) => setF({ ...f, to: (e.target as HTMLInputElement).value })} /></Field>
      </div>
      <Field label={L('روزها', 'Days')}>
        <div class="daypick">
          {weekOrder().map((d) => <button class={f.days.includes(d) ? 'on' : ''} onClick={() => toggleDay(d)}>{dayNames()[d]}</button>)}
        </div>
        <div class="row" style="gap:6px;margin-top:6px">
          <Btn sm onClick={() => setF({ ...f, days: workdays })}>{L('روزهای کاری', 'Workdays')}</Btn>
          <Btn sm onClick={() => setF({ ...f, days: weekend })}>{L('آخر هفته', 'Weekend')}</Btn>
          <Btn sm onClick={() => setF({ ...f, days: [0, 1, 2, 3, 4, 5, 6] })}>{L('همه', 'Every day')}</Btn>
        </div>
      </Field>
      <Field label={L('دستگاه‌ها', 'Devices')}>
        <div class="stack" style="gap:6px">
          {devices.map((d) => (
            <label class="row" style="cursor:pointer;gap:10px">
              <input type="checkbox" checked={f.macs.includes(d.mac)} onChange={() => toggleMac(d.mac)} style="width:18px;height:18px" />
              <TypeIcon type={d.type} size={17} />
              <span class="grow">{d.name || d.hostname || d.mac}</span>
              <span class="mono faint">{d.ip}</span>
            </label>
          ))}
        </div>
      </Field>
    </Modal>
  );
}

type Filter = 'all' | 'online' | 'offline' | 'blocked';

export function Devices() {
  const { data, reload } = usePoll<Device[]>('/devices', 3000);
  const sch = usePoll<Schedule[]>('/schedules', 0);
  const [filter, setFilter] = useState<Filter>('all');
  const [q, setQ] = useState('');
  const [edit, setEdit] = useState<Device | null>(null);
  const [schedEdit, setSchedEdit] = useState<Schedule | 'new' | null>(null);
  lang.value;
  useEffect(() => onRefresh(reload), [reload]);

  const list = (data || []).filter((d) => {
    if (filter === 'online' && !d.online) return false;
    if (filter === 'offline' && d.online) return false;
    if (filter === 'blocked' && !d.blocked) return false;
    if (q && !(d.name + d.hostname + d.ip + d.mac).toLowerCase().includes(q.toLowerCase())) return false;
    return true;
  });
  const online = (data || []).filter((d) => d.online).length;
  const nameOf = (mac: string) => {
    const d = data?.find((x) => x.mac === mac);
    return d ? d.name || d.hostname || mac : mac;
  };

  return (
    <div class="stack">
      <PageHead title={L('دستگاه‌ها', 'Devices')} sub={L('هر چیزی که به یک دستگاه مربوط است: نام، سرعت، زمان، مسدودسازی', 'Everything about each device: name, speed, schedule, blocking')}
        action={<Seg value={filter} onChange={setFilter} items={[
          { id: 'all', label: L('همه', 'All') },
          { id: 'online', label: L('متصل', 'Online') },
          { id: 'offline', label: L('آفلاین', 'Offline') },
          { id: 'blocked', label: L('مسدود', 'Blocked') },
        ]} />} />
      <Card>
        <CardHead icon={<Smartphone size={20} />} title={L('دستگاه‌های شبکه', 'Network devices')}
          sub={data ? L(`${num(online)} متصل از ${num(data.length)} دستگاه`, `${online} online of ${data.length}`) : undefined}
          action={<input class="input hide-sm" style="width:220px;height:40px" placeholder={L('جستجوی نام یا IP…', 'Search name or IP…')} value={q} onInput={(e) => setQ((e.target as HTMLInputElement).value)} />} />
        {!data && <div class="card-body"><Skeleton h={200} /></div>}
        {data && list.length === 0 && <Empty icon={<Smartphone size={34} />} text={L('دستگاهی پیدا نشد', 'No devices')} />}
        {list.map((d) => <DeviceRow key={d.mac} d={d} onEdit={() => setEdit(d)} />)}
      </Card>

      <SiteLogCard devices={data || []} />

      <Card>
        <CardHead icon={<CalendarClock size={20} />} title={L('زمان‌بندی اینترنت دستگاه‌ها', 'Internet schedules')}
          sub={L('در بازه‌های مشخص اینترنت یک دستگاه قطع شود', 'Cut a device’s internet during set hours')}
          action={<Btn sm kind="primary" onClick={() => setSchedEdit('new')}><Plus size={15} />{L('افزودن', 'Add')}</Btn>} />
        {sch.data && sch.data.length === 0 && <Empty icon={<CalendarClock size={34} />} text={L('هنوز زمان‌بندی‌ای تعریف نشده است', 'No schedules yet')} />}
        {(sch.data || []).map((s) => (
          <div class="list-item">
            <Toggle on={s.enabled} onChange={async (v) => { await run(() => post('/schedules', { ...s, enabled: v })); sch.reload(); }} />
            <div class="grow clickable" style="cursor:pointer;min-width:0" onClick={() => setSchedEdit(s)}>
              <div class="row" style="gap:8px"><b class="ltr">{s.from} – {s.to}</b>
                <span class="small muted">{weekOrder().filter((d) => s.days.includes(d)).map((d) => dayNames()[d]).join(L('، ', ', '))}</span></div>
              <div class="tiny faint truncate">{s.macs.map(nameOf).join(L('، ', ', '))}</div>
            </div>
            <Btn sm icon kind="ghost" aria-label={L('حذف', 'Delete')} onClick={async () => {
              if (await confirmDialog(L('این زمان‌بندی حذف شود؟', 'Delete this schedule?'), true)) {
                await run(() => del(`/schedules/${s.id}`));
                sch.reload();
              }
            }}><Trash2 size={15} /></Btn>
          </div>
        ))}
      </Card>

      {edit && <DeviceModal d={edit} onClose={() => setEdit(null)} onSaved={reload} />}
      {schedEdit && <ScheduleModal devices={data || []} s={schedEdit === 'new' ? undefined : schedEdit} onClose={() => setSchedEdit(null)} onSaved={sch.reload} />}
    </div>
  );
}
