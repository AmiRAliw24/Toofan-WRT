import { useEffect, useMemo, useState } from 'preact/hooks';
import qrcode from 'qrcode-generator';
import { AlertTriangle, Layers, Pencil, Plus, QrCode, Radar, Save, Shield, ShieldOff, SlidersHorizontal, Sparkles, Trash2, UserPlus, Wifi } from 'lucide-preact';
import { del, post, usePoll } from '../api';
import { onRefresh } from '../app';
import { L, digits, lang, num } from '../i18n';
import { Badge, Btn, Card, CardHead, Field, Modal, PageHead, Password, Seg, Setting, Skeleton, Tabs, Toggle, confirmDialog, run, toast } from '../ui';

interface Band {
  band: '2g' | '5g';
  radio: string;
  section: string;
  enabled: boolean;
  ssid: string;
  key: string;
  encryption: string;
  channel: string;
  htmode: string;
  txpower: number;
  hidden: boolean;
  channels: number[] | null;
}
interface Guest { enabled: boolean; ssid: string; key: string; encryption: string; exists: boolean }
interface WifiState { bands: Band[]; guest: Guest; country: string; smartWifi: boolean }
interface ChannelScore { channel: number; aps: number; score: number }
interface ScanResult { band: string; current: number; best: number; channels: ChannelScore[]; networks: number; changed: boolean }

const encOptions = () => [
  { id: 'sae-mixed', label: L('WPA2/WPA3 (پیشنهادی)', 'WPA2/WPA3 (recommended)') },
  { id: 'psk2', label: L('WPA2 (سازگاری بیشتر)', 'WPA2 (most compatible)') },
  { id: 'sae', label: L('WPA3 (امن‌ترین)', 'WPA3 (most secure)') },
  { id: 'none', label: L('بدون رمز (ناامن)', 'Open (insecure)') },
];

function escapeWifi(s: string) {
  return s.replace(/([\\;,:"])/g, '\\$1');
}

export function QRModal({ ssid, keyStr, enc, onClose }: { ssid: string; keyStr: string; enc: string; onClose: () => void }) {
  const svg = useMemo(() => {
    const t = enc === 'none' ? 'nopass' : enc === 'sae' ? 'SAE' : 'WPA';
    const payload = `WIFI:T:${t};S:${escapeWifi(ssid)};${enc === 'none' ? '' : `P:${escapeWifi(keyStr)};`};`;
    const qr = qrcode(0, 'M');
    qr.addData(payload, 'Byte');
    qr.make();
    return qr.createSvgTag({ cellSize: 6, margin: 2, scalable: true });
  }, [ssid, keyStr, enc]);
  return (
    <Modal title={L('اتصال با کد QR', 'Connect with QR code')} onClose={onClose}>
      <div style="background:#fff;border-radius:16px;padding:16px;max-width:300px;margin:0 auto;width:100%" dangerouslySetInnerHTML={{ __html: svg }} />
      <div style="text-align:center">
        <div class="card-title">{ssid}</div>
        <div class="muted small">{L('با دوربین گوشی اسکن کنید تا بدون تایپ رمز وصل شوید', 'Scan with a phone camera to join without typing the password')}</div>
      </div>
    </Modal>
  );
}

interface Live { channel: number; frequency: number; txpower: number; htmode: string; bssid: string; clients: number; up: boolean }
interface Net {
  section: string; radio: string; band: '2g' | '5g'; ssid: string; encryption: string; key: string;
  hidden: boolean; enabled: boolean; primary: boolean; netdev?: string; route: 'tunnel' | 'direct';
  isolate: boolean; maxClients: number; fastRoaming: boolean; mfp: string; macFilter: string; macList: string[] | null;
  live?: Live | null;
}
interface NetsResp { networks: Net[]; routingSupported: boolean; bands: Band[] }

const widths = (band: string) => band === '5g'
  ? [{ id: 'VHT20', l: '20 MHz' }, { id: 'VHT40', l: '40 MHz' }, { id: 'VHT80', l: '80 MHz' }]
  : [{ id: 'HT20', l: '20 MHz' }, { id: 'HT40', l: '40 MHz' }];

function LiveInfo({ live, band }: { live?: Live | null; band: string }) {
  if (!live) return null;
  return (
    <div class="live-grid">
      <div class="live-cell"><div class="k">{L('وضعیت', 'Status')}</div><div class="v" style={{ color: live.up ? 'var(--ok)' : 'var(--text-3)' }}>{live.up ? L('فعال', 'On air') : L('خاموش', 'Off')}</div></div>
      <div class="live-cell"><div class="k">{L('کانال فعلی', 'Channel')}</div><div class="v">{live.channel ? digits(live.channel) : '—'}<span class="tiny faint"> {live.frequency ? `(${digits(live.frequency)} MHz)` : ''}</span></div></div>
      <div class="live-cell"><div class="k">{L('پهنا', 'Width')}</div><div class="v ltr">{live.htmode || '—'}</div></div>
      <div class="live-cell"><div class="k">{L('توان ارسال', 'TX power')}</div><div class="v">{live.txpower ? `${digits(live.txpower)} dBm` : '—'}</div></div>
      <div class="live-cell"><div class="k">{L('دستگاه متصل', 'Clients')}</div><div class="v">{num(live.clients)}</div></div>
      <div class="live-cell"><div class="k">BSSID</div><div class="v mono tiny">{live.bssid || '—'}</div></div>
    </div>
  );
}

function RadioCard({ b, onSaved }: { b: Band; onSaved: () => void }) {
  const [f, setF] = useState({ channel: b.channel || 'auto', htmode: b.htmode || '', txpower: b.txpower || 0 });
  const [busy, setBusy] = useState(false);
  return (
    <details class="adv">
      <summary><Radar size={16} />{L('تنظیمات رادیو', 'Radio settings')} · {b.band === '5g' ? L('۵ گیگ', '5 GHz') : L('۲٫۴ گیگ', '2.4 GHz')}
        <span class="tiny faint" style="margin-inline-start:auto">{L('برای همهٔ شبکه‌های این باند', 'applies to every network on this band')}</span></summary>
      <div class="stack" style="gap:12px;padding-top:12px">
        <div class="grid g3" style="gap:12px">
          <Field label={L('کانال', 'Channel')}>
            <select class="select" value={f.channel} onChange={(e) => setF({ ...f, channel: (e.target as HTMLSelectElement).value })}>
              <option value="auto">{L('خودکار', 'Automatic')}</option>
              {(b.channels || []).map((c) => <option value={String(c)}>{digits(c)}</option>)}
            </select>
          </Field>
          <Field label={L('پهنای کانال', 'Channel width')} hint={L('پهن‌تر = سریع‌تر، ولی تداخل بیشتر', 'Wider = faster, but more interference')}>
            <select class="select" value={f.htmode} onChange={(e) => setF({ ...f, htmode: (e.target as HTMLSelectElement).value })}>
              {widths(b.band).map((w) => <option value={w.id}>{w.l}</option>)}
            </select>
          </Field>
          <Field label={L('توان ارسال', 'Transmit power')} hint={L('کمتر = برد کمتر و تداخل کمتر با همسایه‌ها', 'Lower = shorter range, less interference')}>
            <select class="select" value={String(f.txpower)} onChange={(e) => setF({ ...f, txpower: +(e.target as HTMLSelectElement).value })}>
              <option value="0">{L('حداکثر مجاز', 'Maximum allowed')}</option>
              {[23, 20, 17, 14, 11, 8].map((d) => <option value={String(d)}>{digits(d)} dBm</option>)}
            </select>
          </Field>
        </div>
        <div><Btn sm kind="primary" busy={busy} onClick={async () => {
          setBusy(true);
          const ok = await run(() => post('/wifi/radio', { radio: b.radio, ...f }), L('ذخیره شد؛ وای‌فای این باند چند ثانیه قطع می‌شود', 'Saved; this band restarts for a few seconds'));
          setBusy(false);
          if (ok) onSaved();
        }}><Save size={15} />{L('ذخیرهٔ تنظیمات رادیو', 'Save radio settings')}</Btn></div>
      </div>
    </details>
  );
}

/** RouteChoice picks whether a Wi-Fi network's clients use the tunnel. */
function RouteChoice({ value, onChange, supported }: { value: string; onChange: (v: 'tunnel' | 'direct') => void; supported: boolean }) {
  return (
    <Setting title={L('مسیر اینترنت این شبکه', 'Internet path for this network')}
      desc={!supported
        ? L('برای این قابلیت بستهٔ kmod-nft-bridge لازم است (با نصب دوبارهٔ پنل نصب می‌شود).', 'Needs the kmod-nft-bridge package (installed when the panel is reinstalled).')
        : value === 'direct'
          ? L('مستقیم: دستگاه‌های این وای‌فای بدون فیلترشکن به اینترنت وصل می‌شوند', 'Direct: devices on this Wi-Fi use the internet without the tunnel')
          : L('از تونل: دستگاه‌های این وای‌فای از اینترنت آزاد استفاده می‌کنند', 'Tunnel: devices on this Wi-Fi use the free internet')}>
      <Seg value={value as any} onChange={(v: any) => supported && onChange(v)} items={[
        { id: 'tunnel', label: L('از تونل', 'Tunnel') },
        { id: 'direct', label: L('مستقیم', 'Direct') },
      ]} />
    </Setting>
  );
}

function BandForm({ b, onSaved, net, routing, onRoute }: { b: Band; onSaved: () => void; net?: Net; routing: boolean; onRoute: (v: 'tunnel' | 'direct') => void }) {
  const [f, setF] = useState(b);
  const [busy, setBusy] = useState(false);
  const [qr, setQr] = useState(false);
  useEffect(() => setF(b), [b.band, b.ssid, b.key, b.channel, b.enabled, b.encryption]);
  const up = (p: Partial<Band>) => setF({ ...f, ...p });
  const invalid = !f.ssid || (f.encryption !== 'none' && f.key.length < 8);
  const save = async () => {
    if (f.encryption === 'none' && !(await confirmDialog(L('شبکهٔ بدون رمز یعنی هر کسی در همسایگی می‌تواند وصل شود. ادامه می‌دهید؟', 'An open network lets anyone nearby join. Continue?'), true))) return;
    setBusy(true);
    const ok = await run(() => post('/wifi/band', f), L('ذخیره شد؛ وای‌فای چند ثانیه قطع و وصل می‌شود', 'Saved; Wi-Fi restarts for a few seconds'));
    setBusy(false);
    if (ok) onSaved();
  };
  const is5 = b.band === '5g';
  return (
    <Card>
      <CardHead icon={<Wifi size={20} />} title={is5 ? L('وای‌فای ۵ گیگاهرتز', 'Wi-Fi 5 GHz') : L('وای‌فای ۲٫۴ گیگاهرتز', 'Wi-Fi 2.4 GHz')}
        sub={is5 ? L('سرعت بیشتر، برد کمتر — مناسب گوشی و لپ‌تاپ نزدیک روتر', 'Faster, shorter range — for nearby phones and laptops')
          : L('برد بیشتر، سرعت کمتر — مناسب دستگاه‌های دور و لوازم هوشمند', 'Longer range, slower — for distant and smart-home devices')}
        action={<Badge tone={b.enabled ? 'ok' : undefined}>{b.enabled ? L('روشن', 'On') : L('خاموش', 'Off')}</Badge>} />
      <div class="card-body stack">
        <Setting title={L('فعال باشد', 'Enabled')} desc={is5 ? undefined : L('خاموش کردن این باند، دستگاه‌های قدیمی و هوشمند را قطع می‌کند', 'Turning this off disconnects older and smart-home devices')}>
          <Toggle on={f.enabled} onChange={(v) => up({ enabled: v })} />
        </Setting>
        <Field label={L('نام شبکه (SSID)', 'Network name (SSID)')}>
          <input class="input" value={f.ssid} maxLength={32} onInput={(e) => up({ ssid: (e.target as HTMLInputElement).value })} />
        </Field>
        {f.encryption !== 'none' && (
          <Field label={L('رمز عبور', 'Password')} hint={L('حداقل ۸ کاراکتر', 'At least 8 characters')}>
            <Password value={f.key} onInput={(v) => up({ key: v })} />
          </Field>
        )}
        <div class="grid g2" style="gap:14px">
          <Field label={L('نوع رمزنگاری', 'Security')}>
            <select class="select" value={f.encryption} onChange={(e) => up({ encryption: (e.target as HTMLSelectElement).value })}>
              {encOptions().map((o) => <option value={o.id}>{o.label}</option>)}
            </select>
          </Field>
          <Field label={L('کانال', 'Channel')}>
            <select class="select" value={f.channel} onChange={(e) => up({ channel: (e.target as HTMLSelectElement).value })}>
              <option value="auto">{L('خودکار', 'Automatic')}</option>
              {(f.channels || []).map((c) => <option value={String(c)}>{digits(c)}</option>)}
            </select>
          </Field>
        </div>
        {f.encryption === 'none' && (
          <div class="callout warn"><AlertTriangle size={20} /><div>{L('وای‌فای بی‌رمز توصیه نمی‌شود؛ هر کسی می‌تواند وصل شود و ترافیک را ببیند.', 'An open network is not recommended; anyone can join and see traffic.')}</div></div>
        )}
        <Setting title={L('مخفی کردن نام شبکه', 'Hide network name')} desc={L('در لیست وای‌فای‌ها دیده نمی‌شود؛ باید نام را دستی وارد کرد', 'Not shown in Wi-Fi lists; the name must be typed manually')}>
          <Toggle on={f.hidden} onChange={(v) => up({ hidden: v })} />
        </Setting>
        {net && <RouteChoice value={net.route} supported={routing} onChange={onRoute} />}
      </div>
      <div class="card-foot">
        <Btn kind="primary" busy={busy} disabled={invalid} onClick={save}><Save size={17} />{L('ذخیره', 'Save')}</Btn>
        <Btn onClick={() => setQr(true)} disabled={invalid}><QrCode size={17} />{L('کد QR', 'QR code')}</Btn>
      </div>
      {qr && <QRModal ssid={f.ssid} keyStr={f.key} enc={f.encryption} onClose={() => setQr(false)} />}
    </Card>
  );
}

function GuestForm({ g, onSaved }: { g: Guest; onSaved: () => void }) {
  const [f, setF] = useState({ ...g, encryption: g.encryption || 'psk2', ssid: g.ssid || 'Guest' });
  const [busy, setBusy] = useState(false);
  const [qr, setQr] = useState(false);
  const up = (p: Partial<Guest>) => setF({ ...f, ...p });
  const invalid = f.enabled && (!f.ssid || (f.encryption !== 'none' && f.key.length < 8));
  return (
    <Card>
      <CardHead icon={<UserPlus size={20} />} title={L('شبکهٔ مهمان', 'Guest network')}
        sub={L('مهمان‌ها فقط اینترنت دارند؛ به فایل‌ها، دوربین‌ها و پنل روتر دسترسی ندارند', 'Guests get internet only — no access to your files, cameras or this panel')}
        action={<Badge tone={g.enabled ? 'ok' : undefined}>{g.enabled ? L('روشن', 'On') : L('خاموش', 'Off')}</Badge>} />
      <div class="card-body stack">
        <Setting title={L('شبکهٔ مهمان فعال باشد', 'Enable guest network')} desc={!g.exists ? L('بار اول یک شبکهٔ جدا (192.168.30.x) با فایروال ایزوله ساخته می‌شود', 'First time creates a separate network (192.168.30.x) with an isolated firewall zone') : undefined}>
          <Toggle on={f.enabled} onChange={(v) => up({ enabled: v })} />
        </Setting>
        <Field label={L('نام شبکه', 'Network name')}>
          <input class="input" value={f.ssid} maxLength={32} onInput={(e) => up({ ssid: (e.target as HTMLInputElement).value })} />
        </Field>
        <Field label={L('رمزنگاری', 'Security')}>
          <select class="select" value={f.encryption} onChange={(e) => up({ encryption: (e.target as HTMLSelectElement).value })}>
            {encOptions().map((o) => <option value={o.id}>{o.label}</option>)}
          </select>
        </Field>
        {f.encryption !== 'none' && (
          <Field label={L('رمز عبور', 'Password')} hint={L('حداقل ۸ کاراکتر', 'At least 8 characters')}>
            <Password value={f.key} onInput={(v) => up({ key: v })} />
          </Field>
        )}
      </div>
      <div class="card-foot">
        <Btn kind="primary" busy={busy} disabled={invalid} onClick={async () => {
          setBusy(true);
          const ok = await run(() => post('/wifi/guest', f), L('ذخیره شد', 'Saved'));
          setBusy(false);
          if (ok) onSaved();
        }}><Save size={17} />{L('ذخیره', 'Save')}</Btn>
        <Btn onClick={() => setQr(true)} disabled={!f.ssid || (f.encryption !== 'none' && f.key.length < 8)}><QrCode size={17} />{L('کد QR برای مهمان', 'Guest QR code')}</Btn>
      </div>
      {qr && <QRModal ssid={f.ssid} keyStr={f.key} enc={f.encryption} onClose={() => setQr(false)} />}
    </Card>
  );
}

function ScanBand({ band }: { band: '2g' | '5g' }) {
  const [res, setRes] = useState<ScanResult | null>(null);
  const [busy, setBusy] = useState<'' | 'scan' | 'apply'>('');
  const scan = async (apply: boolean) => {
    setBusy(apply ? 'apply' : 'scan');
    const r = await run(() => post<ScanResult>('/wifi/scan', { band, apply }));
    if (r) {
      setRes(r);
      if (apply) toast(r.changed ? L(`کانال به ${digits(r.best)} تغییر کرد`, `Channel changed to ${r.best}`) : L('کانال فعلی همین حالا مناسب است', 'The current channel is already fine'));
    }
    setBusy('');
  };
  const max = Math.max(1, ...(res?.channels || []).map((c) => c.score));
  return (
    <div class="stack" style="gap:10px">
      <div class="row between wrap">
        <b>{band === '5g' ? L('۵ گیگاهرتز', '5 GHz') : L('۲٫۴ گیگاهرتز', '2.4 GHz')}</b>
        <div class="row" style="gap:6px">
          <Btn sm busy={busy === 'scan'} onClick={() => scan(false)}><Radar size={15} />{L('بررسی هوا', 'Survey')}</Btn>
          <Btn sm kind="primary" busy={busy === 'apply'} disabled={!res} onClick={() => scan(true)}><Sparkles size={15} />{L('انتخاب بهترین', 'Use best')}</Btn>
        </div>
      </div>
      {res && (
        <>
          <div class="tiny faint">{L(`${num(res.networks)} شبکهٔ اطراف · کانال فعلی ${digits(res.current)} · پیشنهاد ${digits(res.best)}`, `${res.networks} nearby networks · current ${res.current} · best ${res.best}`)}</div>
          {[...res.channels].sort((a, b) => a.channel - b.channel).map((c) => (
            <div class="row" style="gap:10px">
              <span class="mono" style="width:34px;text-align:end">{c.channel}</span>
              <div class="meter grow" style="margin:0;height:10px">
                <div style={{ width: Math.max(3, (c.score / max) * 100) + '%', background: c.channel === res.best ? 'var(--ok-dot)' : c.channel === res.current ? 'var(--primary)' : 'var(--border-strong)' }} />
              </div>
              <span class="tiny faint" style="width:70px">{L(`${num(c.aps)} شبکه`, `${c.aps} APs`)}</span>
            </div>
          ))}
        </>
      )}
    </div>
  );
}

function Advanced({ smart, reload, bands }: { smart: boolean; reload: () => void; bands: Band[] }) {
  return (
    <Card>
      <CardHead icon={<Sparkles size={20} />} title={L('وای‌فای هوشمند', 'Smart Wi-Fi')}
        sub={L('هوای اطراف را می‌سنجد و خلوت‌ترین کانال را انتخاب می‌کند؛ نام، رمز و نوع امنیت تغییر نمی‌کند', 'Surveys the air and picks the least crowded channel; name, password and security never change')} />
      <div class="card-body stack">
        <Setting title={L('بررسی خودکار شبانه', 'Automatic nightly check')} desc={L('هر شب ساعت ۴ صبح؛ فقط وقتی کانالی به‌وضوح بهتر باشد عوض می‌شود', 'Every night at 4 AM; only switches for a clearly better channel')}>
          <Toggle on={smart} onChange={async (v) => { await run(() => post('/wifi/smart', { enabled: v }), L('ذخیره شد', 'Saved')); reload(); }} />
        </Setting>
        <div class="small muted">{L('هنگام بررسی دستی، وای‌فای ممکن است یکی دو ثانیه کند شود.', 'During a manual survey Wi-Fi may stall for a second or two.')}</div>
        {bands.map((b) => <ScanBand band={b.band} />)}
      </div>
    </Card>
  );
}

function NetModal({ n, routing, bands, onClose, onSaved }: { n?: Net; routing: boolean; bands: Band[]; onClose: () => void; onSaved: () => void }) {
  const [f, setF] = useState<any>(n ? { ...n, macList: n.macList || [] } : {
    section: '', band: '5g', ssid: '', encryption: 'sae-mixed', key: '', hidden: false, enabled: true, route: 'tunnel',
    isolate: false, maxClients: 0, fastRoaming: false, mfp: '1', macFilter: '', macList: [],
  });
  const [macText, setMacText] = useState((n?.macList || []).join('\n'));
  const [busy, setBusy] = useState(false);
  const up = (p: any) => setF({ ...f, ...p });
  const invalid = !f.ssid || (f.encryption !== 'none' && f.key.length < 8);
  const band = bands.find((b) => b.band === (n ? n.band : f.band));
  return (
    <Modal wide title={n ? <bdi>{n.ssid}</bdi> : L('شبکهٔ وای‌فای جدید', 'New Wi-Fi network')} onClose={onClose}
      foot={<><Btn onClick={onClose}>{L('انصراف', 'Cancel')}</Btn>
        <Btn kind="primary" busy={busy} disabled={invalid} onClick={async () => {
          setBusy(true);
          const macList = macText.split(/[\s,]+/).map((x) => x.trim()).filter(Boolean);
          const ok = await run(() => post('/wifi/networks', { ...f, macList }), L('ذخیره شد؛ وای‌فای چند ثانیه قطع و وصل می‌شود', 'Saved; Wi-Fi restarts for a few seconds'));
          setBusy(false);
          if (ok) { onSaved(); onClose(); }
        }}>{L('ذخیره', 'Save')}</Btn></>}>
      {n && <LiveInfo live={n.live} band={n.band} />}
      {!n && (
        <Field label={L('باند', 'Band')}>
          <Seg value={f.band} onChange={(v: string) => up({ band: v })} items={[
            { id: '2g', label: L('۲٫۴ گیگ', '2.4 GHz') },
            { id: '5g', label: L('۵ گیگ', '5 GHz') },
            { id: 'both', label: L('هر دو', 'Both') },
          ]} />
        </Field>
      )}
      <div class="grid g2" style="gap:12px">
        <Field label={L('نام شبکه (SSID)', 'Network name (SSID)')}>
          <input id="net-ssid" class="input" value={f.ssid} maxLength={32} onInput={(e) => up({ ssid: (e.target as HTMLInputElement).value })} />
        </Field>
        <Field label={L('نوع رمزنگاری', 'Security')}>
          <select id="net-enc" class="select" value={f.encryption} onChange={(e) => up({ encryption: (e.target as HTMLSelectElement).value })}>
            {encOptions().map((o) => <option value={o.id}>{o.label}</option>)}
          </select>
        </Field>
      </div>
      {f.encryption !== 'none' && (
        <Field label={L('رمز عبور', 'Password')} hint={L('حداقل ۸ کاراکتر', 'At least 8 characters')}>
          <Password value={f.key} onInput={(v) => up({ key: v })} />
        </Field>
      )}
      <div>
        <RouteChoice value={f.route} supported={routing} onChange={(v) => up({ route: v })} />
        {n && <Setting title={L('فعال باشد', 'Enabled')}><Toggle on={f.enabled} onChange={(v) => up({ enabled: v })} /></Setting>}
        <Setting title={L('مخفی کردن نام شبکه', 'Hide network name')} desc={L('در فهرست وای‌فای‌ها دیده نمی‌شود', 'Not shown in Wi-Fi lists')}>
          <Toggle on={f.hidden} onChange={(v) => up({ hidden: v })} />
        </Setting>
        <Setting title={L('جداسازی دستگاه‌ها', 'Client isolation')} desc={L('دستگاه‌های این وای‌فای همدیگر را نمی‌بینند (مناسب مهمان و لوازم هوشمند)', 'Devices on this Wi-Fi can’t see each other (good for guests and smart devices)')}>
          <Toggle on={f.isolate} onChange={(v) => up({ isolate: v })} />
        </Setting>
      </div>
      <details class="adv">
        <summary><SlidersHorizontal size={16} />{L('تنظیمات پیشرفته', 'Advanced settings')}</summary>
        <div>
          <Setting title={L('رومینگ سریع (802.11r)', 'Fast roaming (802.11r)')} desc={L('جابه‌جایی بی‌وقفه بین چند روتر با همین نام؛ بعضی دستگاه‌های قدیمی با آن مشکل دارند', 'Seamless hand-off between access points with the same name; a few old devices dislike it')}>
            <Toggle on={f.fastRoaming} onChange={(v) => up({ fastRoaming: v })} />
          </Setting>
          <div class="grid g2" style="gap:12px;padding:10px 0">
            <Field label={L('محافظت فریم مدیریت (PMF)', 'Management frame protection')} hint={L('WPA3 همیشه حداقل «اختیاری» است', 'WPA3 always needs at least “optional”')}>
              <select class="select" value={f.mfp || '0'} onChange={(e) => up({ mfp: (e.target as HTMLSelectElement).value })}>
                <option value="0">{L('خاموش', 'Off')}</option>
                <option value="1">{L('اختیاری (پیشنهادی)', 'Optional (recommended)')}</option>
                <option value="2">{L('اجباری', 'Required')}</option>
              </select>
            </Field>
            <Field label={L('حداکثر دستگاه هم‌زمان', 'Max connected devices')} hint={L('۰ = بدون محدودیت', '0 = unlimited')}>
              <input class="input ltr" type="number" min={0} max={128} value={f.maxClients || 0} onInput={(e) => up({ maxClients: +(e.target as HTMLInputElement).value })} />
            </Field>
          </div>
          <Field label={L('فیلتر آدرس MAC', 'MAC address filter')}>
            <Seg value={f.macFilter || ''} onChange={(v: string) => up({ macFilter: v })} items={[
              { id: '', label: L('خاموش', 'Off') },
              { id: 'allow', label: L('فقط این‌ها وصل شوند', 'Allow only these') },
              { id: 'deny', label: L('این‌ها وصل نشوند', 'Block these') },
            ]} />
          </Field>
          {f.macFilter && (
            <Field label={L('آدرس‌های MAC (هر خط یکی)', 'MAC addresses (one per line)')} hint={L('مراقب باشید: با «فقط این‌ها»، دستگاه خودتان را هم اضافه کنید', 'Careful: with “allow only”, include your own device')}>
              <textarea class="textarea" rows={4} value={macText} placeholder="aa:bb:cc:dd:ee:ff" onInput={(e) => setMacText((e.target as HTMLTextAreaElement).value)} />
            </Field>
          )}
        </div>
      </details>
      {band && <RadioCard b={band} onSaved={onSaved} />}
    </Modal>
  );
}

function Networks({ d, reload }: { d: NetsResp; reload: () => void }) {
  const [edit, setEdit] = useState<Net | 'new' | null>(null);
  const [qr, setQr] = useState<Net | null>(null);
  const band = (b: string) => (b === '5g' ? L('۵ گیگ', '5 GHz') : L('۲٫۴ گیگ', '2.4 GHz'));
  return (
    <Card>
      <CardHead icon={<Layers size={20} />} title={L('شبکه‌های وای‌فای', 'Wi-Fi networks')}
        sub={L('چند وای‌فای جدا روی یک روتر؛ هر کدام می‌تواند از تونل یا مستقیم باشد (مثلاً یکی برای تلویزیون و کنسول بدون فیلترشکن)', 'Several Wi-Fi networks on one router, each through the tunnel or direct (e.g. one for the TV and console without the proxy)')}
        action={<Btn sm kind="primary" onClick={() => setEdit('new')}><Plus size={15} />{L('شبکهٔ جدید', 'New network')}</Btn>} />
      {d.networks.length === 0 && <div class="card-body muted">{L('هیچ شبکه‌ای تعریف نشده است.', 'No networks defined.')}</div>}
      {d.networks.map((n) => (
        <div class="list-item">
          <div class="dev-icon"><Wifi size={20} /><span class={'dot' + (n.enabled ? '' : ' off')} /></div>
          <div class="grow" style="min-width:0">
            <div class="dev-name truncate"><bdi>{n.ssid}</bdi></div>
            <div class="chips">
              <span class="badge">{band(n.band)}</span>
              {n.primary && <span class="badge info">{L('اصلی', 'Main')}</span>}
              {n.route === 'direct'
                ? <Badge tone="warn"><ShieldOff size={12} />{L('مستقیم', 'Direct')}</Badge>
                : <Badge tone="ok"><Shield size={12} />{L('از تونل', 'Tunnel')}</Badge>}
              {n.encryption === 'none' && <Badge tone="danger">{L('بی‌رمز', 'Open')}</Badge>}
              {n.hidden && <span class="badge">{L('مخفی', 'Hidden')}</span>}
              {!n.enabled && <span class="badge">{L('خاموش', 'Off')}</span>}
              {n.live?.up && <span class="badge">{L('کانال', 'ch')} {digits(n.live.channel)} · {L(`${num(n.live.clients)} دستگاه`, `${n.live.clients} clients`)}</span>}
            </div>
          </div>
          <Btn sm icon kind="ghost" aria-label={L('کد QR', 'QR code')} onClick={() => setQr(n)}><QrCode size={16} /></Btn>
          <Btn sm icon kind="ghost" aria-label={L('ویرایش', 'Edit')} onClick={() => setEdit(n)}><Pencil size={16} /></Btn>
          {!n.primary && (
            <Btn sm icon kind="ghost" aria-label={L('حذف', 'Delete')} onClick={async () => {
              if (await confirmDialog(L(`شبکهٔ «${n.ssid}» حذف شود؟`, `Delete “${n.ssid}”?`), true)) {
                await run(() => del(`/wifi/networks/${n.section}`), L('حذف شد', 'Deleted'));
                reload();
              }
            }}><Trash2 size={16} /></Btn>
          )}
        </div>
      ))}
      {edit && <NetModal n={edit === 'new' ? undefined : edit} routing={d.routingSupported} bands={d.bands || []} onClose={() => setEdit(null)} onSaved={() => { reload(); setTimeout(reload, 4000); }} />}
      {qr && <QRModal ssid={qr.ssid} keyStr={qr.key} enc={qr.encryption} onClose={() => setQr(null)} />}
    </Card>
  );
}

type Tab = '2g' | '5g' | 'nets' | 'guest' | 'adv';

export function WifiPage() {
  const { data, reload } = usePoll<WifiState>('/wifi', 0);
  const nets = usePoll<NetsResp>('/wifi/networks', 0);
  const [tab, setTab] = useState<Tab>('2g');
  lang.value;
  useEffect(() => onRefresh(() => { reload(); nets.reload(); }), [reload]);
  const netOf = (section?: string) => nets.data?.networks.find((n) => n.section === section);
  const setRoute = async (section: string, route: string) => {
    await run(() => post('/wifi/route', { section, route }), L('ذخیره شد', 'Saved'));
    nets.reload();
  };
  const band = (data?.bands || []).find((b) => b.band === tab);
  return (
    <div class="stack">
      <PageHead title={L('وای‌فای', 'Wi-Fi')} sub={L('نام، رمز و تنظیمات شبکه‌های بی‌سیم خانه', 'Names, passwords and settings of your wireless networks')} />
      <Tabs value={tab} onChange={setTab} items={[
        { id: '2g', label: L('۲٫۴ گیگاهرتز', '2.4 GHz'), icon: <Wifi size={17} /> },
        { id: '5g', label: L('۵ گیگاهرتز', '5 GHz'), icon: <Wifi size={17} /> },
        { id: 'nets', label: L('شبکه‌ها', 'Networks'), icon: <Layers size={17} /> },
        { id: 'guest', label: L('مهمان', 'Guest'), icon: <UserPlus size={17} /> },
        { id: 'adv', label: L('پیشرفته', 'Advanced'), icon: <SlidersHorizontal size={17} /> },
      ]} />
      {!data && <Skeleton h={400} />}
      {data && (tab === '2g' || tab === '5g') && band && <BandForm key={band.band} b={band} onSaved={() => { reload(); nets.reload(); }}
        net={netOf(band.section)} routing={!!nets.data?.routingSupported} onRoute={(v) => setRoute(band.section, v)} />}
      {data && (tab === '2g' || tab === '5g') && !band && <Card pad>{L('این باند روی دستگاه پیدا نشد', 'This band was not found on the device')}</Card>}
      {tab === 'nets' && (nets.data ? <Networks d={nets.data} reload={() => { nets.reload(); reload(); }} /> : <Skeleton h={300} />)}
      {data && tab === 'guest' && <GuestForm g={data.guest} onSaved={reload} />}
      {data && tab === 'adv' && <Advanced smart={data.smartWifi} reload={reload} bands={data.bands || []} />}
    </div>
  );
}
