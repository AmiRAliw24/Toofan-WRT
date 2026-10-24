import { useEffect, useState } from 'preact/hooks';
import {
  ArrowDown, ArrowUp, Ban, Cable, CalendarClock, Camera, Cpu, Gamepad2, Laptop, Monitor, Plus,
  ShieldOff, SlidersHorizontal, Smartphone, Tablet, Trash2, Tv, Wifi, Zap, HelpCircle, Users,
} from 'lucide-preact';
import { del, post, usePoll, type Device, type Schedule } from '../api';
import { onRefresh } from '../app';
import { L, ago, bytes, dayNames, lang, num, rate, weekOrder } from '../i18n';
import { Badge, Btn, Card, CardHead, Empty, Field, Modal, PageHead, Seg, Setting, Skeleton, Toggle, confirmDialog, run } from '../ui';

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

function EditDevice({ d, onClose, onSaved }: { d: Device; onClose: () => void; onSaved: () => void }) {
  const [f, setF] = useState({
    name: d.name || d.hostname || '',
    type: d.type,
    blocked: d.blocked,
    direct: d.direct,
    priority: d.priority,
    downKbps: d.downKbps,
    upKbps: d.upKbps,
  });
  const [busy, setBusy] = useState(false);
  const up = (p: Partial<typeof f>) => setF({ ...f, ...p });
  const save = async () => {
    setBusy(true);
    const ok = await run(() => post(`/devices/${d.mac}`, f), L('ذخیره شد', 'Saved'));
    setBusy(false);
    if (ok) {
      onSaved();
      onClose();
    }
  };
  const mbps = (k: number) => (k === 0 ? L('بدون محدودیت', 'Unlimited') : `${num(k / 1000)} Mbps`);
  return (
    <Modal title={d.name || d.hostname || d.mac} onClose={onClose}
      foot={<><Btn onClick={onClose}>{L('انصراف', 'Cancel')}</Btn><Btn kind="primary" busy={busy} onClick={save}>{L('ذخیره', 'Save')}</Btn></>}>
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
      <div class="card" style="padding:4px 16px;box-shadow:none">
        <div class="kv"><span class="k">MAC</span><span class="v mono">{d.mac}</span></div>
        <div class="kv"><span class="k">IP</span><span class="v mono">{d.ip || '—'}</span></div>
        <div class="kv"><span class="k">{L('اتصال', 'Link')}</span><span class="v">{linkLabel(d).text}{d.signal ? <span class="mono faint"> ({d.signal} dBm)</span> : null}</span></div>
        <div class="kv"><span class="k">{L('مصرف از روشن شدن پنل', 'Usage since panel start')}</span>
          <span class="v ltr">↓ {bytes(d.downTotal)} · ↑ {bytes(d.upTotal)}</span></div>
      </div>
    </Modal>
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
                <span class="small muted">{weekOrder().filter((d) => s.days.includes(d)).map((d) => dayNames()[d]).join('، ')}</span></div>
              <div class="tiny faint truncate">{s.macs.map(nameOf).join('، ')}</div>
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

      {edit && <EditDevice d={edit} onClose={() => setEdit(null)} onSaved={reload} />}
      {schedEdit && <ScheduleModal devices={data || []} s={schedEdit === 'new' ? undefined : schedEdit} onClose={() => setSchedEdit(null)} onSaved={sch.reload} />}
    </div>
  );
}
