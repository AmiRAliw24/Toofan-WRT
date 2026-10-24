import { useEffect, useState } from 'preact/hooks';
import { Clock, Network as NetIcon, Router, Save } from 'lucide-preact';
import { get, post } from '../api';
import { L } from '../i18n';
import { Btn, Card, CardHead, Field, Password, Seg, Skeleton, confirmDialog, run, toast } from '../ui';

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
