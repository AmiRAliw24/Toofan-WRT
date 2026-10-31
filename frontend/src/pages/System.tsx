import { useState } from 'preact/hooks';
import { Cpu, HardDrive, KeyRound, MemoryStick, Power, Router, Thermometer } from 'lucide-preact';
import { noPassword } from '../app';
import { post, usePoll, type SystemStats, type WAN } from '../api';
import { L, bytes, duration, lang, num, pct } from '../i18n';
import { LedCard } from './Led';
import { BackupCard, LuciCard, UpdateCard } from './Maintenance';
import { LanCard, SystemSettingsCard } from './Network';
import { Btn, Card, CardHead, Field, Meter, PageHead, Password, Skeleton, confirmDialog, run, toast } from '../ui';

interface SysResp { system: SystemStats; wan: WAN; version: string }

function Res(p: { icon: any; label: string; used: number; total: string; value: number }) {
  return (
    <Card class="stat">
      <div class="stat-label">{p.icon}{p.label}</div>
      <div class="stat-value">{pct(p.value)}</div>
      <div class="stat-sub"><span class="ltr">{p.total}</span></div>
      <Meter value={p.value} color={p.value > 85 ? 'var(--danger-strong)' : undefined} />
    </Card>
  );
}

// Sensor names as the kernel reports them, made readable.
function sensorName(n: string) {
  const phy = n.match(/\(phy(\d)\)/);
  if (/ath1[01]k|ath9k/i.test(n)) return L(`رادیو وای‌فای${phy ? ' ' + num(+phy[1] + 1) : ''}`, `Wi-Fi radio${phy ? ' ' + (+phy[1] + 1) : ''}`);
  if (/cpu|soc|tsens/i.test(n)) return L('پردازنده', 'CPU');
  return n;
}

export function System() {
  const { data } = usePoll<SysResp>('/system', 3000);
  const [cur, setCur] = useState('');
  const [pw, setPw] = useState('');
  const [pw2, setPw2] = useState('');
  const [busy, setBusy] = useState(false);
  lang.value;
  if (!data) return <Skeleton h={400} />;
  const s = data.system;
  const memUsed = (1 - s.memAvailKb / Math.max(1, s.memTotalKb)) * 100;
  const diskUsed = (1 - s.diskFreeKb / Math.max(1, s.diskTotalKb)) * 100;
  return (
    <div class="stack">
      <PageHead title={L('سیستم', 'System')} sub={L('سخت‌افزار، پشتیبان‌گیری و امنیت ورود', 'Hardware, backup and login security')} />
      <div class="grid g4">
        <Res icon={<Cpu size={16} />} label={L('پردازنده', 'CPU')} used={s.cpuPercent} value={s.cpuPercent}
          total={L(`${num(s.cores)} هسته · بار ${num(s.load[0], 2)}`, `${s.cores} cores · load ${s.load[0].toFixed(2)}`)} />
        <Res icon={<MemoryStick size={16} />} label={L('حافظه', 'Memory')} used={memUsed} value={memUsed}
          total={`${bytes((s.memTotalKb - s.memAvailKb) * 1024)} / ${bytes(s.memTotalKb * 1024)}`} />
        <Res icon={<HardDrive size={16} />} label={L('فضای ذخیره', 'Storage')} used={diskUsed} value={diskUsed}
          total={`${bytes((s.diskTotalKb - s.diskFreeKb) * 1024)} / ${bytes(s.diskTotalKb * 1024)}`} />
        <Card class="stat">
          <div class="stat-label"><Thermometer size={16} />{L('دما', 'Temperature')}</div>
          <div class="stat-value ltr" style="text-align:start">
            {s.tempC ? <>{num(s.tempC, 1)}<span class="unit"> °C</span></> : '—'}
          </div>
          <div class="stat-sub" title={(s.temps || []).map((t) => `${sensorName(t.name)}: ${t.c} °C`).join('\n')}>
            {s.tempC
              ? <>{sensorName(s.tempSensor || '')}{(s.temps?.length || 0) > 1 ? L(` · ${num(s.temps!.length)} حسگر`, ` · ${s.temps!.length} sensors`) : ''}</>
              : L('حسگر دما پیدا نشد', 'No temperature sensor found')}
          </div>
          <div class="stat-sub">{L('روشن از ', 'Up ')}{duration(s.uptimeSec)}</div>
        </Card>
      </div>
      <div class="grid g2">
        <Card>
          <CardHead icon={<Router size={20} />} title={L('اطلاعات دستگاه', 'Device info')} />
          <div class="card-body" style="padding-block:4px">
            <div class="kv"><span class="k">{L('مدل', 'Model')}</span><span class="v">{s.model}</span></div>
            <div class="kv"><span class="k">{L('سیستم‌عامل', 'Firmware')}</span><span class="v ltr">{s.firmware}</span></div>
            <div class="kv"><span class="k">{L('کرنل', 'Kernel')}</span><span class="v mono">{s.kernel}</span></div>
            <div class="kv"><span class="k">{L('نسخهٔ پنل', 'Panel version')}</span><span class="v mono">{data.version}</span></div>
            <div class="kv"><span class="k">{L('IP اینترنت', 'WAN IP')}</span><span class="v mono">{data.wan.ip || '—'}</span></div>
            <div class="kv"><span class="k">{L('نوع اتصال', 'WAN type')}</span><span class="v mono">{data.wan.proto || '—'}</span></div>
          </div>
          <div class="card-foot">
            <Btn kind="danger" onClick={async () => {
              if (await confirmDialog(L('روتر راه‌اندازی مجدد شود؟ حدود یک دقیقه اینترنت قطع می‌شود.', 'Restart the router? Internet drops for about a minute.'), true)) {
                run(() => post('/system/reboot'), L('در حال راه‌اندازی مجدد…', 'Restarting…'));
              }
            }}><Power size={17} />{L('راه‌اندازی مجدد', 'Restart')}</Btn>
          </div>
        </Card>
        <Card>
          <CardHead icon={<KeyRound size={20} />} tone="warn" title={L('تغییر رمز روتر', 'Change router password')}
            sub={L('رمز ورود به این پنل و SSH با هم عوض می‌شود', 'Changes the password for this panel and SSH together')} />
          <div class="card-body stack" style="gap:12px">
            <Field label={L('رمز فعلی', 'Current password')}><Password value={cur} onInput={setCur} autocomplete="current-password" /></Field>
            <Field label={L('رمز جدید', 'New password')} hint={L('حداقل ۸ کاراکتر', 'At least 8 characters')}><Password value={pw} onInput={setPw} /></Field>
            <Field label={L('تکرار رمز جدید', 'Repeat new password')}><Password value={pw2} onInput={setPw2} /></Field>
          </div>
          <div class="card-foot">
            <Btn kind="primary" busy={busy} disabled={pw.length < 8} onClick={async () => {
              if (pw !== pw2) { toast(L('تکرار رمز یکسان نیست', 'Passwords don’t match'), true); return; }
              setBusy(true);
              const ok = await run(() => post('/system/password', { current: cur, password: pw }), L('رمز تغییر کرد', 'Password changed'));
              setBusy(false);
              if (ok) { setCur(''); setPw(''); setPw2(''); noPassword.value = false; }
            }}>{L('تغییر رمز', 'Change password')}</Btn>
          </div>
        </Card>
      </div>
      <div class="grid g2">
        <LanCard />
        <SystemSettingsCard />
      </div>
      <div class="grid g2">
        <UpdateCard />
        <BackupCard />
      </div>
      <LedCard />
      <LuciCard />
    </div>
  );
}
