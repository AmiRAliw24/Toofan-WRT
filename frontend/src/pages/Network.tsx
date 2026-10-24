import { useEffect, useState } from 'preact/hooks';
import { Clock, Lock, Network as NetIcon, Radar, Router, Save, Wifi } from 'lucide-preact';
import { get, post } from '../api';
import { L } from '../i18n';
import { Btn, Card, CardHead, Field, Modal, Password, Seg, Skeleton, confirmDialog, run, toast } from '../ui';

interface WAN { proto: string; username?: string; password?: string; ipaddr?: string; netmask?: string; gateway?: string; dns?: string[] | null }
interface LAN { ipaddr: string; netmask: string; dhcpStart: number; dhcpLimit: number; leaseTime: string }
interface Sys { hostname: string; zone: string }
interface Net { wan: WAN; lan: LAN; system: Sys; zones: string[] }

function useNet() {
  const [d, setD] = useState<Net | null>(null);
  useEffect(() => { get<Net>('/network').then(setD).catch(() => {}); }, []);
  return d;
}

const inp = (set: (v: string) => void) => (e: Event) => set((e.target as HTMLInputElement).value);

export function WanCard() {
  const d = useNet();
  const [f, setF] = useState<WAN | null>(null);
  const [busy, setBusy] = useState(false);
  useEffect(() => { if (d) setF({ ...d.wan, password: '' }); }, [d]);
  if (!f) return <Skeleton h={260} />;
  const up = (p: Partial<WAN>) => setF({ ...f, ...p });
  return (
    <Card>
      <CardHead icon={<NetIcon size={20} />} title={L('نوع اتصال به اینترنت', 'Internet connection type')}
        sub={L('روتر چطور از مودم یا خط اینترنت بگیرد', 'How the router gets internet from the modem or line')} />
      <div class="card-body stack" style="gap:14px">
        <Seg value={f.proto as any} onChange={(v: string) => up({ proto: v })} items={[
          { id: 'dhcp', label: L('خودکار (DHCP)', 'Automatic (DHCP)') },
          { id: 'pppoe', label: 'PPPoE' },
          { id: 'static', label: L('IP ثابت', 'Static IP') },
        ]} />
        {f.proto === 'dhcp' && <div class="small muted">{L('برای وقتی که روتر پشت مودم است؛ رایج‌ترین حالت.', 'When the router sits behind a modem; the most common setup.')}</div>}
        {f.proto === 'pppoe' && (
          <div class="grid g2" style="gap:12px">
            <Field label={L('نام کاربری PPPoE', 'PPPoE username')}><input class="input ltr" value={f.username || ''} onInput={inp((v) => up({ username: v }))} /></Field>
            <Field label={L('رمز PPPoE', 'PPPoE password')} hint={L('خالی بگذارید تا رمز قبلی بماند', 'Leave empty to keep the saved one')}><Password value={f.password || ''} onInput={(v) => up({ password: v })} /></Field>
          </div>
        )}
        {f.proto === 'static' && (
          <div class="grid g3" style="gap:12px">
            <Field label="IP"><input class="input ltr" value={f.ipaddr || ''} onInput={inp((v) => up({ ipaddr: v }))} /></Field>
            <Field label={L('ماسک', 'Netmask')}><input class="input ltr" value={f.netmask || '255.255.255.0'} onInput={inp((v) => up({ netmask: v }))} /></Field>
            <Field label={L('دروازه (Gateway)', 'Gateway')}><input class="input ltr" value={f.gateway || ''} onInput={inp((v) => up({ gateway: v }))} /></Field>
          </div>
        )}
        <Field label={L('DNS دلخواه برای خط (اختیاری)', 'Custom line DNS (optional)')} hint={L('خالی = از اپراتور. وقتی اینترنت آزاد روشن است، DNS امن جداگانه استفاده می‌شود.', 'Empty = from the ISP. With the free internet on, secure DNS is used separately.')}>
          <input class="input ltr" value={(f.dns || []).join(' ')} placeholder="178.22.122.100 185.51.200.2" onInput={inp((v) => up({ dns: v.split(/[\s,]+/).filter(Boolean) }))} />
        </Field>
      </div>
      <div class="card-foot">
        <Btn kind="primary" busy={busy} onClick={async () => {
          if (!(await confirmDialog(L('اینترنت روتر چند ثانیه قطع و دوباره وصل می‌شود. ادامه می‌دهید؟', 'The router’s internet will drop for a few seconds. Continue?')))) return;
          setBusy(true);
          await run(() => post('/network/wan', f), L('ذخیره شد؛ اتصال دوباره برقرار می‌شود', 'Saved; reconnecting'));
          setBusy(false);
        }}><Save size={17} />{L('ذخیره', 'Save')}</Btn>
      </div>
    </Card>
  );
}

export function LanCard() {
  const d = useNet();
  const [f, setF] = useState<LAN | null>(null);
  const [busy, setBusy] = useState(false);
  useEffect(() => { if (d) setF(d.lan); }, [d]);
  if (!f || !d) return null;
  const up = (p: Partial<LAN>) => setF({ ...f, ...p });
  return (
    <Card>
      <CardHead icon={<Router size={20} />} title={L('شبکهٔ داخلی (LAN)', 'Home network (LAN)')}
        sub={L('آدرس خود روتر و بازهٔ IP دستگاه‌ها', 'The router’s own address and the device IP range')} />
      <div class="card-body stack" style="gap:12px">
        <div class="grid g2" style="gap:12px">
          <Field label={L('آدرس روتر', 'Router address')}><input class="input ltr" value={f.ipaddr} onInput={inp((v) => up({ ipaddr: v }))} /></Field>
          <Field label={L('ماسک', 'Netmask')}><input class="input ltr" value={f.netmask} onInput={inp((v) => up({ netmask: v }))} /></Field>
        </div>
        <div class="grid g3" style="gap:12px">
          <Field label={L('شروع DHCP', 'DHCP start')}><input class="input ltr" type="number" value={f.dhcpStart} onInput={inp((v) => up({ dhcpStart: +v }))} /></Field>
          <Field label={L('تعداد', 'Count')}><input class="input ltr" type="number" value={f.dhcpLimit} onInput={inp((v) => up({ dhcpLimit: +v }))} /></Field>
          <Field label={L('مدت اجاره', 'Lease time')}><input class="input ltr" value={f.leaseTime} onInput={inp((v) => up({ leaseTime: v }))} /></Field>
        </div>
      </div>
      <div class="card-foot">
        <Btn kind="primary" busy={busy} onClick={async () => {
          const changed = f.ipaddr !== d.lan.ipaddr;
          if (!(await confirmDialog(changed
            ? L(`آدرس روتر به ${f.ipaddr} تغییر می‌کند. همهٔ دستگاه‌ها چند ثانیه قطع می‌شوند و پنل از آدرس جدید باز می‌شود.`, `The router moves to ${f.ipaddr}. Devices drop for a few seconds and the panel reopens at the new address.`)
            : L('شبکهٔ داخلی چند ثانیه قطع می‌شود. ادامه می‌دهید؟', 'The home network drops for a few seconds. Continue?'), changed))) return;
          setBusy(true);
          const ok = await run(() => post('/network/lan', f));
          setBusy(false);
          if (ok && changed) {
            toast(L('در حال انتقال به آدرس جدید…', 'Moving to the new address…'));
            setTimeout(() => { location.href = `${location.protocol}//${f.ipaddr}${location.port ? ':' + location.port : ''}/`; }, 9000);
          } else if (ok) toast(L('ذخیره شد', 'Saved'));
        }}><Save size={17} />{L('ذخیره', 'Save')}</Btn>
      </div>
    </Card>
  );
}

export function SystemSettingsCard() {
  const d = useNet();
  const [f, setF] = useState<Sys | null>(null);
  const [busy, setBusy] = useState(false);
  useEffect(() => { if (d) setF(d.system); }, [d]);
  if (!f || !d) return null;
  return (
    <Card>
      <CardHead icon={<Clock size={20} />} title={L('نام روتر و ساعت', 'Router name & clock')}
        sub={L('منطقهٔ زمانی برای زمان‌بندی‌ها و خاموشی شبانه مهم است', 'The time zone matters for schedules and night mode')} />
      <div class="card-body grid g2" style="gap:12px">
        <Field label={L('نام روتر', 'Router name')}><input class="input ltr" value={f.hostname} onInput={inp((v) => setF({ ...f, hostname: v }))} /></Field>
        <Field label={L('منطقهٔ زمانی', 'Time zone')}>
          <select class="select ltr" value={f.zone} onChange={(e) => setF({ ...f, zone: (e.target as HTMLSelectElement).value })}>
            {d.zones.map((z) => <option value={z}>{z === 'Asia/Tehran' ? L('تهران (Asia/Tehran)', 'Tehran (Asia/Tehran)') : z}</option>)}
          </select>
        </Field>
      </div>
      <div class="card-foot">
        <Btn kind="primary" busy={busy} onClick={async () => {
          setBusy(true);
          await run(() => post('/network/system', f), L('ذخیره شد', 'Saved'));
          setBusy(false);
        }}><Save size={17} />{L('ذخیره', 'Save')}</Btn>
      </div>
    </Card>
  );
}

interface WifiNet { ssid: string; bssid: string; band: '2g' | '5g'; channel: number; signal: number; encryption: string }
interface Uplink { configured: boolean; ssid: string; band: string; connected: boolean; signal: number; ip: string }

function Bars({ dbm }: { dbm: number }) {
  const n = dbm >= -55 ? 4 : dbm >= -65 ? 3 : dbm >= -75 ? 2 : 1;
  return (
    <span class="ltr" style="display:inline-flex;gap:2px;align-items:flex-end;height:16px" title={`${dbm} dBm`}>
      {[1, 2, 3, 4].map((i) => (
        <span style={{ width: '4px', height: 4 + i * 3 + 'px', borderRadius: '1px', background: i <= n ? 'var(--primary)' : 'var(--border-strong)' }} />
      ))}
    </span>
  );
}

/** UplinkCard lets the router take its internet from another Wi-Fi network. */
export function UplinkCard() {
  const [st, setSt] = useState<Uplink | null>(null);
  const [nets, setNets] = useState<WifiNet[] | null>(null);
  const [busy, setBusy] = useState<'' | 'scan' | 'connect' | 'off'>('');
  const [pick, setPick] = useState<WifiNet | null>(null);
  const [key, setKey] = useState('');
  const load = () => get<Uplink>('/network/uplink').then(setSt).catch(() => {});
  useEffect(() => {
    load();
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  }, []);
  const scan = async () => {
    setBusy('scan');
    const r = await run(() => post<WifiNet[]>('/network/uplink/scan'));
    if (r) setNets(r);
    setBusy('');
  };
  const connect = async () => {
    if (!pick) return;
    setBusy('connect');
    const ok = await run(() => post('/network/uplink', { ...pick, key }), L('در حال اتصال؛ چند ثانیه صبر کنید…', 'Connecting; give it a few seconds…'));
    setBusy('');
    if (ok) {
      setPick(null);
      setKey('');
      setNets(null);
      setTimeout(load, 8000);
    }
  };
  const bandLabel = (b: string) => (b === '5g' ? L('۵ گیگ', '5 GHz') : L('۲٫۴ گیگ', '2.4 GHz'));
  return (
    <Card>
      <CardHead icon={<Wifi size={20} />} title={L('اینترنت از طریق وای‌فای', 'Internet over Wi-Fi')}
        sub={L('به‌جای کابل، روتر با وای‌فای به مودم وصل شود (حالت تکرارکننده). اگر کابل WAN هم وصل باشد، کابل اولویت دارد.', 'Instead of a cable, the router joins the modem’s Wi-Fi (repeater mode). If the WAN cable is also connected, the cable wins.')} />
      <div class="card-body stack" style="gap:12px">
        {st?.configured ? (
          <div class={'callout small ' + (st.connected ? 'ok' : 'warn')}>
            <Wifi size={18} />
            <div class="grow">
              <b>{st.connected ? L('وصل به ', 'Connected to ') : L('در حال اتصال به ', 'Connecting to ')}<bdi>{st.ssid}</bdi></b>
              <span class="row" style="gap:8px">
                {bandLabel(st.band)}
                {st.connected && <Bars dbm={st.signal} />}
                {st.ip && <span class="mono">{st.ip}</span>}
              </span>
            </div>
            <Btn sm kind="danger" busy={busy === 'off'} onClick={async () => {
              if (!(await confirmDialog(L('اتصال وای‌فای به مودم قطع شود؟ اگر کابل WAN وصل نباشد، اینترنت روتر قطع می‌شود.', 'Disconnect from the modem’s Wi-Fi? Without a WAN cable the router loses internet.'), true))) return;
              setBusy('off');
              await run(() => post('/network/uplink/disconnect'), L('قطع شد', 'Disconnected'));
              setBusy('');
              load();
            }}>{L('قطع', 'Disconnect')}</Btn>
          </div>
        ) : (
          <div class="small muted">{L('هنوز به هیچ وای‌فایی وصل نیست. شبکه‌های اطراف را جستجو کنید.', 'Not using Wi-Fi for internet. Search for nearby networks.')}</div>
        )}
        {nets && nets.length === 0 && <div class="small muted">{L('شبکه‌ای پیدا نشد', 'No networks found')}</div>}
        {nets && nets.length > 0 && (
          <div class="card" style="box-shadow:none">
            {nets.map((n) => (
              <div class="list-item clickable" onClick={() => { setPick(n); setKey(''); }}>
                <Bars dbm={n.signal} />
                <div class="grow" style="min-width:0">
                  <div class="truncate" style="font-weight:700"><bdi>{n.ssid}</bdi></div>
                  <div class="tiny faint">{bandLabel(n.band)} · {L('کانال', 'ch')} {n.channel}</div>
                </div>
                {n.encryption === 'none' ? <span class="badge warn">{L('بی‌رمز', 'Open')}</span>
                  : n.encryption === 'enterprise' ? <span class="badge">{L('سازمانی', 'Enterprise')}</span>
                  : <Lock size={15} class="faint" />}
              </div>
            ))}
          </div>
        )}
        <div class="tiny faint">{L('نکته: رادیویی که به مودم وصل می‌شود، وای‌فای خودش را روی همان کانال مودم می‌برد. اگر مودم خاموش شود، وای‌فای آن باند هم ممکن است موقتاً قطع شود؛ باند دیگر کار می‌کند.', 'Note: the radio used for the uplink moves its own Wi-Fi to the modem’s channel. If the modem goes away, that band’s Wi-Fi may pause; the other band keeps working.')}</div>
      </div>
      <div class="card-foot">
        <Btn kind="primary" busy={busy === 'scan'} onClick={scan}><Radar size={17} />{L('جستجوی وای‌فای‌ها', 'Search networks')}</Btn>
      </div>
      {pick && (
        <Modal title={<bdi>{pick.ssid}</bdi>} onClose={() => setPick(null)}
          foot={<><Btn onClick={() => setPick(null)}>{L('انصراف', 'Cancel')}</Btn>
            <Btn kind="primary" busy={busy === 'connect'} disabled={pick.encryption === 'enterprise' || (pick.encryption !== 'none' && key.length < 8)} onClick={connect}>{L('اتصال', 'Connect')}</Btn></>}>
          {pick.encryption === 'enterprise' ? (
            <div class="callout warn small"><div>{L('شبکه‌های سازمانی (802.1x) پشتیبانی نمی‌شوند.', 'Enterprise (802.1x) networks are not supported.')}</div></div>
          ) : pick.encryption === 'none' ? (
            <div class="callout warn small"><div>{L('این شبکه رمز ندارد؛ ترافیک بین روتر و مودم رمزنگاری نمی‌شود.', 'This network is open; traffic between the router and the modem is not encrypted.')}</div></div>
          ) : (
            <Field label={L('رمز وای‌فای مودم', 'Modem Wi-Fi password')}>
              <Password value={key} onInput={setKey} />
            </Field>
          )}
          <div class="small muted">{bandLabel(pick.band)} · {L('کانال', 'channel')} {pick.channel} · <span class="mono">{pick.bssid}</span></div>
        </Modal>
      )}
    </Card>
  );
}
