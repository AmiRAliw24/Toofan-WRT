import { useEffect, useMemo, useState } from 'preact/hooks';
import qrcode from 'qrcode-generator';
import { AlertTriangle, QrCode, Radar, Save, SlidersHorizontal, Sparkles, UserPlus, Wifi } from 'lucide-preact';
import { post, usePoll } from '../api';
import { onRefresh } from '../app';
import { L, digits, lang, num } from '../i18n';
import { Badge, Btn, Card, CardHead, Field, Modal, PageHead, Password, Setting, Skeleton, Tabs, Toggle, confirmDialog, run, toast } from '../ui';

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

function BandForm({ b, onSaved }: { b: Band; onSaved: () => void }) {
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

type Tab = '2g' | '5g' | 'guest' | 'adv';

export function WifiPage() {
  const { data, reload } = usePoll<WifiState>('/wifi', 0);
  const [tab, setTab] = useState<Tab>('2g');
  lang.value;
  useEffect(() => onRefresh(reload), [reload]);
  const band = (data?.bands || []).find((b) => b.band === tab);
  return (
    <div class="stack">
      <PageHead title={L('وای‌فای', 'Wi-Fi')} sub={L('نام، رمز و تنظیمات شبکه‌های بی‌سیم خانه', 'Names, passwords and settings of your wireless networks')} />
      <Tabs value={tab} onChange={setTab} items={[
        { id: '2g', label: L('۲٫۴ گیگاهرتز', '2.4 GHz'), icon: <Wifi size={17} /> },
        { id: '5g', label: L('۵ گیگاهرتز', '5 GHz'), icon: <Wifi size={17} /> },
        { id: 'guest', label: L('مهمان', 'Guest'), icon: <UserPlus size={17} /> },
        { id: 'adv', label: L('پیشرفته', 'Advanced'), icon: <SlidersHorizontal size={17} /> },
      ]} />
      {!data && <Skeleton h={400} />}
      {data && (tab === '2g' || tab === '5g') && band && <BandForm key={band.band} b={band} onSaved={reload} />}
      {data && (tab === '2g' || tab === '5g') && !band && <Card pad>{L('این باند روی دستگاه پیدا نشد', 'This band was not found on the device')}</Card>}
      {data && tab === 'guest' && <GuestForm g={data.guest} onSaved={reload} />}
      {data && tab === 'adv' && <Advanced smart={data.smartWifi} reload={reload} bands={data.bands || []} />}
    </div>
  );
}
