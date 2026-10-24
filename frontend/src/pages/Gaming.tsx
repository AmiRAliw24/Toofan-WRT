import { useEffect, useState } from 'preact/hooks';
import { Activity, Gamepad2, Gauge, Network, Play, Plug, SlidersHorizontal, Zap, AlertTriangle } from 'lucide-preact';
import { get, post, usePoll } from '../api';
import { go, onRefresh } from '../app';
import { L, ago, lang, num } from '../i18n';
import { Badge, Btn, Card, CardHead, Empty, Field, PageHead, Setting, Skeleton, Toggle, run } from '../ui';

interface NAT { type: string; publicIp: string; wanIp: string; doubleNat: boolean; mapping: string; servers: number; testedAt: number; error?: string }
interface Mapping { proto: string; extPort: number; intAddr: string; intPort: number; desc: string }
interface Bloat { running: boolean; phase: string; idleMs: number; loadedMs: number; grade: string; downMbps: number; error?: string; at: number }
interface GamingResp {
  nat: NAT | null;
  upnp: { installed: boolean; enabled: boolean; mappings: Mapping[] | null };
  sqm: { enabled: boolean; downMbps: number; upMbps: number };
  sqmAvailable: boolean;
  bloat: Bloat;
  priority: string[] | null;
  latency: { ext: number; dom: number };
}

function natInfo(t: string) {
  switch (t) {
    case 'open': return { tone: 'ok' as const, label: L('باز (Open)', 'Open'), desc: L('بهترین حالت؛ با همه می‌توانید بازی کنید و میزبان شوید', 'Best: you can play with everyone and host') };
    case 'moderate': return { tone: 'warn' as const, label: L('متوسط (Moderate)', 'Moderate'), desc: L('بیشتر بازی‌ها کار می‌کنند؛ گاهی میزبانی یا چت صوتی مشکل دارد', 'Most games work; hosting or voice chat may struggle') };
    case 'strict': return { tone: 'danger' as const, label: L('سخت (Strict)', 'Strict'), desc: L('پیدا کردن هم‌تیمی و چت صوتی سخت است', 'Matchmaking and voice chat are hard') };
    default: return { tone: undefined, label: L('نامشخص', 'Unknown'), desc: '' };
  }
}

function gradeTone(g: string) {
  if (g === 'A+' || g === 'A') return 'ok' as const;
  if (g === 'B' || g === 'C') return 'warn' as const;
  return 'danger' as const;
}

function NATCard({ nat, upnpOn }: { nat: NAT | null; upnpOn: boolean }) {
  const [busy, setBusy] = useState(false);
  const [res, setRes] = useState<NAT | null>(nat);
  useEffect(() => setRes(nat), [nat?.testedAt]);
  const n = res ? natInfo(res.type) : null;
  return (
    <Card>
      <CardHead icon={<Network size={20} />} title={L('نوع NAT', 'NAT type')}
        sub={L('تعیین می‌کند کنسول و بازی‌ها چقدر راحت به بقیه وصل شوند', 'How easily consoles and games connect to other players')}
        action={<Btn sm kind="primary" busy={busy} onClick={async () => {
          setBusy(true);
          const r = await run(() => post<NAT>('/gaming/nat'));
          if (r) setRes(r);
          setBusy(false);
        }}><Play size={15} />{L('بررسی', 'Check')}</Btn>} />
      <div class="card-body">
        {!res && <div class="muted">{L('هنوز بررسی نشده است.', 'Not checked yet.')}</div>}
        {res && n && (
          <div class="stack" style="gap:10px">
            <div class="row wrap"><Badge tone={n.tone}>{n.label}</Badge><span class="small muted">{n.desc}</span></div>
            {res.error && <div class="small" style="color:var(--danger-strong)">{res.error}</div>}
            <div class="kv"><span class="k">{L('IP عمومی', 'Public IP')}</span><span class="v mono">{res.publicIp || '—'}</span></div>
            <div class="kv"><span class="k">{L('IP روی WAN روتر', 'Router WAN IP')}</span><span class="v mono">{res.wanIp || '—'}</span></div>
            {res.doubleNat && (
              <div class="callout warn small"><AlertTriangle size={18} /><div>
                <b>{L('NAT دوبل', 'Double NAT')}</b>
                {L('روتر پشت مودم یا اپراتور با NAT جداگانه است، پس UPnP روتر به‌تنهایی پورت را از بیرون باز نمی‌کند. اگر می‌توانید مودم را در حالت Bridge بگذارید یا روتر را در DMZ مودم قرار دهید.',
                  'The router sits behind another NAT (modem or ISP), so UPnP here can’t open ports from the outside alone. Put the modem in bridge mode or the router in its DMZ if possible.')}
              </div></div>
            )}
            {!res.doubleNat && res.type !== 'open' && !upnpOn && (
              <div class="small muted">{L('روشن کردن UPnP در پایین معمولاً NAT را بهتر می‌کند.', 'Turning on UPnP below usually improves NAT.')}</div>
            )}
            {res.testedAt > 0 && <div class="tiny faint">{ago(res.testedAt)}</div>}
          </div>
        )}
      </div>
    </Card>
  );
}

function UPnPCard({ u, reload }: { u: GamingResp['upnp']; reload: () => void }) {
  return (
    <Card>
      <CardHead icon={<Plug size={20} />} title={L('باز کردن خودکار پورت (UPnP)', 'Automatic port opening (UPnP)')}
        sub={L('کنسول و بازی‌ها خودشان پورت لازم را باز می‌کنند', 'Consoles and games open the ports they need by themselves')} />
      <div class="card-body" style="padding-block:0">
        <Setting title={L('UPnP و NAT-PMP', 'UPnP and NAT-PMP')}
          desc={!u.installed ? L('بستهٔ miniupnpd-nftables نصب نیست', 'miniupnpd-nftables is not installed') : L('فقط برای دستگاه‌های داخل شبکهٔ اصلی', 'Only for devices on the main network')}>
          <Toggle on={u.enabled} disabled={!u.installed} onChange={async (v) => { await run(() => post('/gaming/upnp', { enabled: v }), L('ذخیره شد', 'Saved')); reload(); }} />
        </Setting>
      </div>
      {u.enabled && (u.mappings || []).length > 0 && (
        <div>
          {(u.mappings || []).map((m) => (
            <div class="list-item">
              <Badge>{m.proto}</Badge>
              <span class="mono">{m.extPort}</span>
              <span class="faint">→</span>
              <span class="mono">{m.intAddr}:{m.intPort}</span>
              <span class="grow small muted truncate ltr" style="text-align:end">{m.desc}</span>
            </div>
          ))}
        </div>
      )}
      {u.enabled && !(u.mappings || []).length && <div class="card-body small muted" style="border-top:1px solid var(--border)">{L('هنوز دستگاهی پورتی باز نکرده است.', 'No device has opened a port yet.')}</div>}
    </Card>
  );
}

function BloatCard({ initial }: { initial: Bloat }) {
  const [b, setB] = useState<Bloat>(initial);
  useEffect(() => {
    if (!b.running) return;
    const t = setInterval(async () => {
      try { setB(await get<Bloat>('/gaming/bloat')); } catch {}
    }, 800);
    return () => clearInterval(t);
  }, [b.running]);
  return (
    <Card>
      <CardHead icon={<Activity size={20} />} title={L('تأخیر زیر بار (Bufferbloat)', 'Latency under load (Bufferbloat)')}
        sub={L('وقتی کسی دانلود می‌کند، پینگ بازی چقدر بالا می‌رود؟', 'How much does game ping rise while someone downloads?')}
        action={<Btn sm kind="primary" busy={b.running} onClick={async () => {
          const ok = await run(() => post('/gaming/bloat'));
          if (ok) setB({ ...b, running: true, phase: 'idle', grade: '', idleMs: 0, loadedMs: 0 });
        }}><Gauge size={15} />{L('آزمایش', 'Test')}</Btn>} />
      <div class="card-body">
        {b.running && <div class="muted small">{b.phase === 'idle' ? L('اندازه‌گیری پینگ در حالت آرام…', 'Measuring idle ping…') : L('دانلود سنگین و اندازه‌گیری هم‌زمان پینگ…', 'Saturating the line and measuring ping…')}</div>}
        {!b.running && b.phase === 'error' && <div class="small" style="color:var(--danger-strong)">{b.error}</div>}
        {!b.running && !b.grade && b.phase !== 'error' && <div class="muted small">{L('حدود ۱۵ ثانیه طول می‌کشد و خط را پر می‌کند.', 'Takes about 15 seconds and saturates the line.')}</div>}
        {b.grade && (
          <div class="row wrap" style="gap:18px">
            <div style="font-size:44px;font-weight:900;line-height:1" class={'ltr'}>
              <span style={{ color: `var(--${gradeTone(b.grade) === 'ok' ? 'ok' : gradeTone(b.grade) === 'warn' ? 'warn' : 'danger-strong'})` }}>{b.grade}</span>
            </div>
            <div class="grow">
              <div class="kv"><span class="k">{L('پینگ آرام', 'Idle ping')}</span><span class="v">{num(b.idleMs)} ms</span></div>
              <div class="kv"><span class="k">{L('پینگ زیر بار', 'Loaded ping')}</span><span class="v">{num(b.loadedMs)} ms <span class="faint">(+{num(Math.max(0, b.loadedMs - b.idleMs))})</span></span></div>
              <div class="kv"><span class="k">{L('سرعت دانلود حین آزمایش', 'Download during test')}</span><span class="v">{num(b.downMbps, 1)} Mbps</span></div>
            </div>
          </div>
        )}
        {b.grade && gradeTone(b.grade) !== 'ok' && (
          <div class="small muted" style="margin-top:10px">{L('برای بهتر شدن، SQM را در پایین روشن کنید.', 'Turn on SQM below to improve this.')}</div>
        )}
      </div>
    </Card>
  );
}

function SQMCard({ d, reload, lastDown }: { d: GamingResp; reload: () => void; lastDown: number }) {
  const [f, setF] = useState(d.sqm);
  const [busy, setBusy] = useState(false);
  return (
    <Card>
      <CardHead icon={<SlidersHorizontal size={20} />} tone="violet" title={L('صف هوشمند (SQM)', 'Smart queue (SQM)')}
        sub={L('سرعت را کمی پایین‌تر از سقف خط نگه می‌دارد تا صف در مودم پر نشود و پینگ پایین بماند', 'Keeps speed slightly under the line limit so the modem’s queue never fills and ping stays low')} />
      <div class="card-body stack" style="gap:12px">
        {!d.sqmAvailable && <div class="callout warn small"><AlertTriangle size={18} /><div>{L('بسته‌های tc-tiny، kmod-sched-cake و kmod-ifb نصب نیستند.', 'Packages tc-tiny, kmod-sched-cake and kmod-ifb are not installed.')}</div></div>}
        <Setting title={L('فعال باشد', 'Enabled')}><Toggle on={f.enabled} disabled={!d.sqmAvailable} onChange={(v) => setF({ ...f, enabled: v })} /></Setting>
        <div class="grid g2" style="gap:12px">
          <Field label={L('سرعت دانلود خط (Mbps)', 'Line download (Mbps)')} hint={L('حدود ۹۰٪ سرعت واقعی', 'About 90% of the real speed')}>
            <input class="input ltr" type="number" min={1} value={f.downMbps || ''} onInput={(e) => setF({ ...f, downMbps: +(e.target as HTMLInputElement).value })} />
          </Field>
          <Field label={L('سرعت آپلود خط (Mbps)', 'Line upload (Mbps)')} hint={L('حدود ۹۰٪ سرعت واقعی', 'About 90% of the real speed')}>
            <input class="input ltr" type="number" min={1} value={f.upMbps || ''} onInput={(e) => setF({ ...f, upMbps: +(e.target as HTMLInputElement).value })} />
          </Field>
        </div>
        {lastDown > 0 && !f.downMbps && (
          <Btn sm onClick={() => setF({ ...f, downMbps: Math.max(1, Math.round(lastDown * 0.9)) })}>
            {L(`پیشنهاد از آخرین تست: ${num(Math.round(lastDown * 0.9))} Mbps`, `Suggest from last test: ${Math.round(lastDown * 0.9)} Mbps`)}
          </Btn>
        )}
        <div class="small muted">{L('دستگاه‌هایی که «اولویت» دارند (از صفحهٔ دستگاه‌ها) در صف جلو می‌افتند.', 'Devices marked “priority” (on the Devices page) jump the queue.')}</div>
      </div>
      <div class="card-foot">
        <Btn kind="primary" busy={busy} disabled={!d.sqmAvailable && f.enabled} onClick={async () => {
          setBusy(true);
          await run(() => post('/gaming/sqm', f), L('ذخیره شد', 'Saved'));
          setBusy(false);
          reload();
        }}>{L('ذخیره', 'Save')}</Btn>
      </div>
    </Card>
  );
}

export function Gaming() {
  const { data, reload } = usePoll<GamingResp>('/gaming', 10000);
  const speed = usePoll<{ downMbps: number }>('/speedtest', 0);
  lang.value;
  useEffect(() => onRefresh(reload), [reload]);
  if (!data) return <Skeleton h={500} />;
  const prio = data.priority || [];
  return (
    <div class="stack">
      <PageHead title={L('بازی و کیفیت اتصال', 'Gaming & connection quality')} sub={L('نوع NAT، باز کردن پورت، تأخیر زیر بار و اولویت دستگاه‌های بازی', 'NAT type, port opening, latency under load and gaming device priority')} />
      <div class="grid g3">
        <Card class="stat">
          <div class="stat-label"><Zap size={16} />{L('پینگ داخلی', 'Domestic ping')}</div>
          <div class="stat-value">{data.latency.dom >= 0 ? `${num(data.latency.dom)} ms` : '—'}</div>
        </Card>
        <Card class="stat">
          <div class="stat-label"><Zap size={16} />{L('پینگ خارجی', 'International ping')}</div>
          <div class="stat-value">{data.latency.ext >= 0 ? `${num(data.latency.ext)} ms` : '—'}</div>
        </Card>
        <Card class="stat">
          <div class="stat-label"><Gamepad2 size={16} />{L('دستگاه‌های اولویت‌دار', 'Priority devices')}</div>
          <div class="stat-value">{num(prio.length)}</div>
          <div class="stat-sub"><a href="#/devices" onClick={(e) => { e.preventDefault(); go('devices'); }}>{L('تنظیم از صفحهٔ دستگاه‌ها', 'Set on the Devices page')}</a></div>
        </Card>
      </div>
      <div class="grid g2">
        <NATCard nat={data.nat} upnpOn={data.upnp.enabled} />
        <UPnPCard u={data.upnp} reload={reload} />
      </div>
      <div class="grid g2">
        <BloatCard initial={data.bloat} />
        <SQMCard d={data} reload={reload} lastDown={speed.data?.downMbps || data.bloat.downMbps || 0} />
      </div>
      {prio.length === 0 && (
        <Empty icon={<Gamepad2 size={30} />} text={L('برای کنسول یا کامپیوتر بازی، از صفحهٔ دستگاه‌ها «اولویت ترافیک» و در صورت نیاز «مستقیم، بدون فیلترشکن» را روشن کنید.', 'For a console or gaming PC, turn on “traffic priority” (and “direct, no proxy” if needed) on the Devices page.')} />
      )}
    </div>
  );
}
