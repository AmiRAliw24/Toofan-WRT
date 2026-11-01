import { useEffect, useState } from 'preact/hooks';
import { AlertTriangle, CheckCircle2, Cpu, ExternalLink, PackageCheck, RefreshCw, ShieldCheck, XCircle } from 'lucide-preact';
import { get, post, usePoll } from '../api';
import { L, ago, bytes, num } from '../i18n';
import { Badge, Btn, Card, CardHead, Meter, Setting, Skeleton, Toggle, confirmDialog, run, toast } from '../ui';

interface Board { model: string; boardName: string; version: string; revision: string; target: string; compat: string; pm: string }
interface Option { version: string; series: string; current: boolean; allowed: boolean; reason?: string; stable: boolean; pm: string; notes: string; changes: boolean }
interface Job {
  state: 'idle' | 'building' | 'downloading' | 'checking' | 'ready' | 'flashing' | 'failed';
  version?: string; custom: boolean; fallback?: string; stage?: string; done: number; total: number;
  image?: string; sha256?: string; size: number; kitSize: number; error?: string;
}
interface Marker { from: string; to: string; at: number; custom: boolean; healed: boolean; healedAt?: number; result?: string }
interface Heal { state: 'idle' | 'running' | 'ok' | 'failed'; try?: number; error?: string }
interface FwResp { board: Board; options: Option[] | null; error?: string; job: Job; last?: Marker; heal: Heal; memFree: number }

const busy = (s: Job['state']) => s === 'building' || s === 'downloading' || s === 'checking';

function stageText(j: Job) {
  switch (j.state) {
    case 'building':
      return j.stage === 'building_image'
        ? L('سرور OpenWrt در حال ساختن فرم‌ور اختصاصی شماست (۱ تا ۵ دقیقه)…', 'OpenWrt’s server is building your custom firmware (1–5 min)…')
        : L('درخواست ساخت فرم‌ور اختصاصی در صف سرور OpenWrt…', 'Custom firmware request queued at OpenWrt’s server…');
    case 'downloading': return L('دانلود فرم‌ور…', 'Downloading the firmware…');
    case 'checking': return L('بررسی سلامت فایل و سازگاری با روتر (sysupgrade -T)…', 'Checking the file and its compatibility (sysupgrade -T)…');
    default: return '';
  }
}

function reasonText(o: Option) {
  switch (o.reason) {
    case 'current': return L('نسخهٔ فعلی', 'Current');
    case 'older': return L('قدیمی‌تر', 'Older');
    case 'step': return L('اول به سری قبلی ارتقا دهید (مرحله‌ای)', 'Upgrade to the previous series first');
    default: return '';
  }
}

/** Waits for the router to go down and come back after a firmware flash. */
async function waitForReturn(onTick: (s: string) => void): Promise<boolean> {
  const ping = async () => { try { await get('/ping'); return true; } catch { return false; } };
  const t0 = Date.now();
  let wentDown = false;
  while (Date.now() - t0 < 12 * 60_000) {
    await new Promise((r) => setTimeout(r, 5000));
    const up = await ping();
    if (!up) wentDown = true;
    const min = Math.floor((Date.now() - t0) / 60000), sec = Math.floor(((Date.now() - t0) % 60000) / 1000);
    onTick(`${min}:${String(sec).padStart(2, '0')}`);
    if (up && wentDown) return true;
  }
  return false;
}

/** FirmwareCard: upgrade OpenWrt itself (official releases for this board). */
export function FirmwareCard() {
  const { data, reload } = usePoll<FwResp>('/firmware', 0);
  const [pick, setPick] = useState('');
  const [custom, setCustom] = useState(true);
  const [flashing, setFlashing] = useState<string | null>(null);
  const state = data?.job.state;
  useEffect(() => {
    if (!state || !busy(state)) return;
    const t = setInterval(reload, 1500);
    return () => clearInterval(t);
  }, [state]);
  useEffect(() => {
    if (data?.heal.state !== 'running') return;
    const t = setInterval(reload, 3000);
    return () => clearInterval(t);
  }, [data?.heal.state]);
  useEffect(() => {
    if (!data?.options || pick) return;
    const best = data.options.find((o) => o.allowed && !o.changes) || data.options.find((o) => o.allowed);
    if (best) setPick(best.version);
  }, [data?.options]);

  if (!data) return <Card><Skeleton h={220} /></Card>;
  const b = data.board, j = data.job;
  const opts = data.options || [];
  const chosen = opts.find((o) => o.version === pick);
  const anyAllowed = opts.some((o) => o.allowed);

  const prepare = async () => {
    if (!chosen) return;
    const ok = await run(() => post('/firmware/prepare', { version: chosen.version, custom }));
    if (ok) reload();
  };
  const flash = async () => {
    const yes = await confirmDialog(L(
      `OpenWrt ${j.version} روی روتر نوشته شود؟\n\nحدود ۳ تا ۵ دقیقه طول می‌کشد و اینترنت و وای‌فای قطع می‌شود. در این مدت به هیچ عنوان برق روتر را قطع نکنید. همهٔ تنظیمات، پنل و سرورها حفظ می‌شوند.`,
      `Write OpenWrt ${j.version} to the router?\n\nIt takes about 3–5 minutes; internet and Wi-Fi go down meanwhile. Do NOT unplug the router during this time. All settings, the panel and your servers are kept.`), true);
    if (!yes) return;
    const ok = await run(() => post('/firmware/flash'));
    if (!ok) { reload(); return; }
    setFlashing('0:00');
    const back = await waitForReturn(setFlashing);
    if (back) {
      toast(L('روتر با فرم‌ور جدید بالا آمد', 'The router is back on the new firmware'));
      setTimeout(() => location.reload(), 1500);
    } else {
      setFlashing(null);
      toast(L('روتر هنوز جواب نمی‌دهد؛ چند دقیقهٔ دیگر صبر کنید و صفحه را دوباره باز کنید (برق را قطع نکنید)', 'The router is not answering yet; wait a few more minutes and reopen the page (don’t unplug it)'), true);
    }
  };

  return (
    <Card>
      <CardHead icon={<Cpu size={20} />} tone="violet" title={L('به‌روزرسانی فرم‌ور OpenWrt', 'OpenWrt firmware upgrade')}
        sub={L('فقط نسخه‌های رسمی OpenWrt مخصوص همین مدل روتر؛ تنظیمات و پنل حفظ می‌شوند', 'Official OpenWrt releases for this exact model only; settings and the panel are kept')}
        action={<Badge tone="info">{b.version || '—'}</Badge>} />
      <div class="card-body stack" style="gap:12px">
        <div class="grid g2" style="gap:0 18px">
          <div class="kv"><span class="k">{L('مدل', 'Model')}</span><span class="v">{b.model || '—'}</span></div>
          <div class="kv"><span class="k">{L('نسخهٔ فعلی', 'Current version')}</span><span class="v mono">{b.version} <span class="faint tiny">{b.revision}</span></span></div>
          <div class="kv"><span class="k">{L('شناسهٔ برد', 'Board')}</span><span class="v mono">{b.boardName} · {b.target}</span></div>
          <div class="kv"><span class="k">{L('حافظهٔ آزاد', 'Free memory')}</span><span class="v">{bytes(data.memFree)}</span></div>
        </div>

        {flashing !== null && (
          <div class="callout warn"><span class="spinner" /><div>
            <b>{L('در حال نوشتن فرم‌ور و راه‌اندازی دوباره…', 'Writing the firmware and restarting…')} <span class="mono">{flashing}</span></b>
            {L('برق روتر را قطع نکنید. وای‌فای چند دقیقه قطع است؛ اگر گوشی/لپ‌تاپ به شبکهٔ دیگری وصل شد، بعد از برگشتن وای‌فای دوباره به روتر وصل شوید. این صفحه خودش منتظر می‌ماند.',
              'Don’t unplug the router. Wi-Fi is down for a few minutes; if your device joins another network, reconnect once the Wi-Fi is back. This page waits by itself.')}
          </div></div>
        )}

        {data.last && (
          <div class={'callout small ' + (data.last.healed ? 'ok' : 'info')}>
            {data.last.healed ? <CheckCircle2 size={18} /> : <RefreshCw size={18} />}
            <div>
              <b>{L('آخرین ارتقا: ', 'Last upgrade: ')}<span class="mono">{data.last.from} → {data.last.to}</span></b> · {ago(data.last.at)}
              {!data.last.healed && data.heal.state !== 'failed' && <div>{L('در حال تکمیل: نصب بسته‌های لازم پنل روی فرم‌ور جدید…', 'Finishing: installing the panel’s packages on the new firmware…')}</div>}
            </div>
          </div>
        )}
        {data.heal.state === 'failed' && (
          <div class="callout danger small"><XCircle size={18} /><div>
            <b>{L('نصب بسته‌های لازم بعد از ارتقا انجام نشد', 'Installing the needed packages after the upgrade failed')}</b>
            <span class="mono tiny">{data.heal.error}</span>
            <div>{L('اینترنت آزاد تا نصب این بسته‌ها کار نمی‌کند؛ روتر هر ۵ دقیقه دوباره تلاش می‌کند.', 'The free internet needs these packages; the router retries every 5 minutes.')}</div>
            <Btn sm style="margin-top:8px" onClick={async () => { await run(() => post('/firmware/repair')); setTimeout(reload, 1500); }}><PackageCheck size={15} />{L('تلاش دوباره', 'Retry now')}</Btn>
          </div></div>
        )}

        {data.error ? (
          <div class="callout danger small"><XCircle size={18} /><div>
            <b>{L('فهرست نسخه‌ها دریافت نشد', 'Could not get the release list')}</b><span class="tiny mono">{data.error}</span>
            <Btn sm style="margin-top:8px" onClick={reload}><RefreshCw size={15} />{L('دوباره', 'Retry')}</Btn>
          </div></div>
        ) : (
          <div class="fw-list">
            {opts.map((o) => (
              <div role="button" tabIndex={o.allowed ? 0 : -1} class={'fw-opt' + (pick === o.version ? ' on' : '') + (o.allowed ? '' : ' off')}
                onClick={() => { if (o.allowed && !busy(j.state) && j.state !== 'flashing') setPick(o.version); }}>
                <div class="row" style="gap:6px;justify-content:space-between">
                  <b class="mono">OpenWrt {o.version}</b>
                  <span class="row" style="gap:4px">
                    {o.stable && <Badge tone="ok">{L('پایدار', 'stable')}</Badge>}
                    {o.current && <Badge tone="info">{L('نصب‌شده', 'installed')}</Badge>}
                    {o.pm === 'apk' && <Badge tone="warn">apk</Badge>}
                  </span>
                </div>
                <div class="tiny muted">
                  {o.allowed ? (o.changes ? L('نسخهٔ اصلی بعدی (تغییرات بیشتر)', 'Next major release (bigger change)') : L('رفع اشکال و امنیت همین نسخه', 'Bug and security fixes for your release')) : reasonText(o)}
                  {' · '}<a href={o.notes} target="_blank" rel="noreferrer" onClick={(e) => e.stopPropagation()}>{L('یادداشت انتشار', 'Release notes')} <ExternalLink size={11} /></a>
                </div>
              </div>
            ))}
            {!anyAllowed && opts.length > 0 && <div class="tiny muted">{L('آخرین نسخهٔ مجاز روی روتر نصب است.', 'The newest allowed release is installed.')}</div>}
          </div>
        )}

        {chosen?.changes && j.state === 'idle' && (
          <div class="callout warn small"><AlertTriangle size={18} /><div>
            {L(`ارتقا به سری ${chosen.series} تغییر بزرگ‌تری است. تنظیمات منتقل می‌شوند، ولی پیشنهاد می‌شود قبلش از «پشتیبان‌گیری» یک نسخه از تنظیمات دانلود کنید.`,
              `Moving to the ${chosen.series} series is a bigger change. Settings are carried over, but download a settings backup first (Backup card).`)}
            {chosen.pm === 'apk' && <div>{L('این نسخه به‌جای opkg از apk برای بسته‌ها استفاده می‌کند.', 'This release uses apk instead of opkg for packages.')}</div>}
          </div></div>
        )}

        {(j.state === 'idle' || j.state === 'failed') && anyAllowed && (
          <Setting title={L('فرم‌ور اختصاصی (پیشنهادی)', 'Custom firmware (recommended)')}
            desc={L('سرور رسمی OpenWrt فرم‌ور را همراه بسته‌های لازم پنل (Xray، TPROXY، OpenVPN، UPnP، SQM) می‌سازد تا اینترنت آزاد بلافاصله بعد از ارتقا کار کند. اگر نشد، فرم‌ور رسمی دانلود و بسته‌ها بعداً نصب می‌شوند.',
              'OpenWrt’s official server builds the firmware with the panel’s packages (Xray, TPROXY, OpenVPN, UPnP, SQM) so the free internet works right after the upgrade. If that fails, the official image is used and the packages are installed afterwards.')}>
            <Toggle on={custom} onChange={setCustom} />
          </Setting>
        )}

        {busy(j.state) && (
          <div>
            <div class="small muted row" style="gap:8px"><span class="spinner" />{stageText(j)}</div>
            {j.state === 'downloading' && j.total > 0 && (
              <><Meter value={(j.done / j.total) * 100} /><div class="tiny faint">{bytes(j.done)} / {bytes(j.total)}</div></>
            )}
          </div>
        )}
        {j.state === 'failed' && (
          <div class="callout danger small"><XCircle size={18} /><div>
            <b>{L('آماده‌سازی متوقف شد؛ چیزی روی روتر تغییر نکرد', 'Preparation stopped; nothing on the router changed')}</b>
            <span class="tiny mono" style="word-break:break-word">{j.error}</span>
          </div></div>
        )}
        {j.state === 'ready' && (
          <div class="callout ok small"><ShieldCheck size={18} /><div>
            <b>{L(`OpenWrt ${j.version} آماده و بررسی شد`, `OpenWrt ${j.version} is ready and checked`)}</b>
            <div>{j.custom ? L('فرم‌ور اختصاصی همراه بسته‌های پنل', 'Custom firmware with the panel’s packages') : L('فرم‌ور رسمی (بسته‌های پنل بعد از ارتقا نصب می‌شوند)', 'Official firmware (the panel’s packages are installed after the upgrade)')}
              {' · '}{bytes(j.size)}</div>
            {j.fallback && <div class="tiny">{L('ساخت اختصاصی نشد: ', 'Custom build failed: ')}<span class="mono">{j.fallback}</span></div>}
            <div class="tiny">✓ SHA-256 <span class="mono">{j.sha256?.slice(0, 16)}…</span> ✓ sysupgrade -T ✓ {L('حافظه', 'memory')} ({L('پشتیبان پنل', 'panel backup')} {bytes(j.kitSize)})</div>
          </div></div>
        )}

        <div class="row" style="gap:8px;flex-wrap:wrap">
          {(j.state === 'idle' || j.state === 'failed') && (
            <Btn kind="primary" disabled={!chosen?.allowed} onClick={prepare}>{L('دانلود و بررسی', 'Download and check')}{chosen ? ` ${chosen.version}` : ''}</Btn>
          )}
          {j.state === 'ready' && flashing === null && (
            <Btn kind="danger" onClick={flash}>{L(`نصب OpenWrt ${j.version}`, `Install OpenWrt ${j.version}`)}</Btn>
          )}
          {(busy(j.state) || j.state === 'ready') && flashing === null && (
            <Btn onClick={async () => { await run(() => post('/firmware/cancel')); setTimeout(reload, 300); }}>{L('انصراف', 'Cancel')}</Btn>
          )}
        </div>
        <div class="tiny faint">
          {L('ایمنی: فقط فایل‌های رسمی همین برد · بررسی SHA-256 · آزمایش sysupgrade -T قبل از نوشتن · ارتقای مرحله‌ای (یک سری در هر بار) · تا وقتی «نصب» را نزنید چیزی روی حافظهٔ روتر نوشته نمی‌شود.',
            'Safety: official files for this board only · SHA-256 check · sysupgrade -T before writing · one series at a time · nothing is written to flash until you press Install.')}
          {data.memFree > 0 && data.memFree < 120 * 1024 * 1024 && <> {L('حافظهٔ آزاد کم است: ', 'Low free memory: ')}{num(data.memFree / 1048576)} MB</>}
        </div>
      </div>
    </Card>
  );
}
