import { useEffect, useState } from 'preact/hooks';
import { ArchiveRestore, CheckCircle2, CloudDownload, Download, ExternalLink, FileJson, PackageOpen, RefreshCw, Trash2, Upload, Wrench, XCircle } from 'lucide-preact';
import { get, post } from '../api';
import { L, ago } from '../i18n';
import { Btn, Card, CardHead, Field, confirmDialog, run, toast } from '../ui';

interface UpdResult { ok: boolean; from: string; to: string; error?: string; at: number }
interface UpdStatus { state: string; error?: string; target?: string; current: string; last?: UpdResult }
interface Manifest { version: string; url: string; sha256: string; notes: string }

function pickFile(accept: string): Promise<File | null> {
  return new Promise((resolve) => {
    const inp = document.createElement('input');
    inp.type = 'file';
    inp.accept = accept;
    inp.onchange = () => resolve(inp.files?.[0] || null);
    inp.click();
  });
}

/** upload sends a file as raw bytes with progress reporting. */
function upload(path: string, file: File, onProgress: (p: number) => void): Promise<any> {
  return new Promise((resolve, reject) => {
    const x = new XMLHttpRequest();
    x.open('POST', '/api' + path);
    x.setRequestHeader('Content-Type', 'application/octet-stream');
    x.setRequestHeader('X-Separ-Upload', '1');
    x.upload.onprogress = (e) => e.lengthComputable && onProgress(e.loaded / e.total);
    x.onload = () => {
      let d: any = {};
      try { d = JSON.parse(x.responseText); } catch {}
      x.status >= 200 && x.status < 300 ? resolve(d) : reject(new Error(d.error || x.statusText));
    };
    x.onerror = () => reject(new Error(L('ارسال فایل ناموفق بود', 'Upload failed')));
    x.send(file);
  });
}

/** waitForVersion polls until the panel answers with a different version. */
async function waitForVersion(old: string, timeoutMs = 150000): Promise<string | null> {
  const end = Date.now() + timeoutMs;
  await new Promise((r) => setTimeout(r, 4000));
  while (Date.now() < end) {
    try {
      const r = await fetch('/api/ping', { cache: 'no-store' });
      const d = await r.json();
      if (d.version && d.version !== old) return d.version;
    } catch {}
    await new Promise((r) => setTimeout(r, 2500));
  }
  return null;
}

export function UpdateCard() {
  const [st, setSt] = useState<UpdStatus | null>(null);
  const [source, setSource] = useState('');
  const [showSource, setShowSource] = useState(false);
  const [manifest, setManifest] = useState<Manifest | null>(null);
  const [progress, setProgress] = useState<number | null>(null);
  const [phase, setPhase] = useState<'' | 'upload' | 'install' | 'check'>('');
  const load = async () => {
    try {
      const d = await get<{ status: UpdStatus; source: string }>('/update');
      setSt(d.status);
      setSource(d.source);
    } catch {}
  };
  useEffect(() => { load(); }, []);

  const finish = async (old: string) => {
    setPhase('install');
    const v = await waitForVersion(old);
    if (v) {
      toast(L(`به‌روزرسانی انجام شد: ${v}`, `Updated to ${v}`));
      setTimeout(() => location.reload(), 1200);
    } else {
      setPhase('');
      await load();
      toast(L('نسخهٔ جدید بالا نیامد و نسخهٔ قبلی برگردانده شد', 'The new version did not start; the previous one was restored'), true);
    }
  };

  const fromFile = async () => {
    const f = await pickFile('.tar.gz,.tgz,application/gzip');
    if (!f || !st) return;
    if (!(await confirmDialog(L(`فایل «${f.name}» نصب شود؟ پنل چند ثانیه در دسترس نیست. اگر نسخهٔ جدید بالا نیاید، نسخهٔ فعلی خودکار برمی‌گردد.`, `Install “${f.name}”? The panel is briefly unavailable. If the new version fails to start, the current one is restored automatically.`)))) return;
    setPhase('upload');
    setProgress(0);
    try {
      await upload('/update/upload', f, setProgress);
      setProgress(null);
      await finish(st.current);
    } catch (e: any) {
      setProgress(null);
      setPhase('');
      toast(e.message, true);
    }
  };

  const check = async () => {
    setPhase('check');
    const r = await run(() => post<{ manifest: Manifest; newer: boolean }>('/update/check'));
    setPhase('');
    if (!r) return;
    if (r.newer) setManifest(r.manifest);
    else toast(L('آخرین نسخه نصب است', 'You are on the latest version'));
  };

  const busy = phase !== '';
  return (
    <Card>
      <CardHead icon={<PackageOpen size={20} />} title={L('به‌روزرسانی پنل', 'Panel update')}
        sub={L('تنظیمات حفظ می‌شود؛ اگر نسخهٔ جدید درست بالا نیاید، نسخهٔ قبلی خودکار برمی‌گردد', 'Settings are kept; if the new version fails to start, the previous one comes back automatically')} />
      <div class="card-body stack" style="gap:12px">
        <div class="kv" style="padding-top:0"><span class="k">{L('نسخهٔ فعلی', 'Current version')}</span><span class="v mono">{st?.current || '—'}</span></div>
        {st?.last && (
          <div class={'callout small ' + (st.last.ok ? 'ok' : 'danger')}>
            {st.last.ok ? <CheckCircle2 size={18} /> : <XCircle size={18} />}
            <div>
              <b>{st.last.ok ? L('آخرین به‌روزرسانی موفق بود', 'Last update succeeded') : L('آخرین به‌روزرسانی برگردانده شد', 'Last update was rolled back')}</b>
              <span class="mono">{st.last.from} → {st.last.to}</span> · {ago(st.last.at)}
            </div>
          </div>
        )}
        {phase === 'upload' && progress !== null && (
          <div>
            <div class="small muted">{L('ارسال فایل…', 'Uploading…')} {Math.round(progress * 100)}%</div>
            <div class="meter"><div style={{ width: progress * 100 + '%' }} /></div>
          </div>
        )}
        {phase === 'install' && (
          <div class="callout info small"><span class="spinner" /><div>{L('در حال نصب و راه‌اندازی نسخهٔ جدید؛ این صفحه را نبندید…', 'Installing and starting the new version; keep this page open…')}</div></div>
        )}
        {manifest && (
          <div class="callout ok small"><CloudDownload size={18} /><div>
            <b>{L('نسخهٔ جدید: ', 'New version: ')}<span class="mono">{manifest.version}</span></b>
            {manifest.notes && <div style="white-space:pre-wrap">{manifest.notes}</div>}
            <Btn sm kind="primary" style="margin-top:8px" disabled={busy} onClick={async () => {
              const old = st?.current || '';
              const ok = await run(() => post('/update/install'));
              if (ok) { setManifest(null); finish(old); }
            }}>{L('نصب', 'Install')}</Btn>
          </div></div>
        )}
        {showSource && (
          <div class="row" style="align-items:flex-end">
            <div class="grow">
              <Field label={L('منبع به‌روزرسانی (manifest)', 'Update source (manifest)')} hint={L('آدرس فایل JSON نسخه‌ها', 'URL of the release JSON')}>
                <input class="input ltr" value={source} placeholder="https://…/separ/latest.json" onInput={(e) => setSource((e.target as HTMLInputElement).value)} />
              </Field>
            </div>
            <Btn onClick={async () => { await run(() => post('/update/source', { url: source }), L('ذخیره شد', 'Saved')); setShowSource(false); }}>{L('ذخیره', 'Save')}</Btn>
          </div>
        )}
      </div>
      <div class="card-foot">
        <Btn kind="primary" busy={phase === 'upload'} disabled={busy} onClick={fromFile}><Upload size={17} />{L('نصب از فایل', 'Install from file')}</Btn>
        {source ? (
          <Btn busy={phase === 'check'} disabled={busy} onClick={check}><RefreshCw size={17} />{L('بررسی نسخهٔ جدید', 'Check for updates')}</Btn>
        ) : null}
        <Btn kind="ghost" onClick={() => setShowSource(!showSource)}>{L('منبع آنلاین', 'Online source')}</Btn>
      </div>
    </Card>
  );
}

export function BackupCard() {
  const [busy, setBusy] = useState<'' | 'restore' | 'import'>('');
  const [progress, setProgress] = useState(0);
  return (
    <Card>
      <CardHead icon={<ArchiveRestore size={20} />} tone="info" title={L('پشتیبان و بازگردانی', 'Backup & restore')}
        sub={L('فایل پشتیبان رمزها و لینک سرورها را دارد؛ آن را جای امن نگه دارید', 'Backups contain passwords and server links; keep them somewhere safe')} />
      <div class="card-body stack" style="gap:14px">
        <div>
          <div class="setting-title">{L('کل تنظیمات روتر', 'All router settings')}</div>
          <div class="setting-desc">{L('شبکه، وای‌فای، فایروال و تنظیمات پنل؛ برای برگرداندن، روتر راه‌اندازی مجدد می‌شود', 'Network, Wi-Fi, firewall and panel settings; restoring reboots the router')}</div>
          <div class="row wrap" style="margin-top:8px">
            <Btn onClick={() => { location.href = '/api/system/backup'; }}><Download size={17} />{L('دانلود پشتیبان', 'Download backup')}</Btn>
            <Btn kind="danger" busy={busy === 'restore'} onClick={async () => {
              const f = await pickFile('.tar.gz,.tgz,application/gzip');
              if (!f) return;
              if (!(await confirmDialog(L('همهٔ تنظیمات فعلی روتر با این فایل جایگزین و روتر راه‌اندازی مجدد می‌شود. ادامه می‌دهید؟', 'All current router settings will be replaced and the router will reboot. Continue?'), true))) return;
              setBusy('restore');
              try {
                await upload('/system/restore', f, setProgress);
                toast(L('بازگردانی شد؛ روتر در حال راه‌اندازی مجدد است', 'Restored; the router is rebooting'));
              } catch (e: any) {
                toast(e.message, true);
              }
              setBusy('');
            }}><ArchiveRestore size={17} />{busy === 'restore' ? `${Math.round(progress * 100)}%` : L('بازگردانی از فایل', 'Restore from file')}</Btn>
          </div>
        </div>
        <div style="border-top:1px solid var(--border);padding-top:14px">
          <div class="setting-title">{L('فقط تنظیمات پنل', 'Panel settings only')}</div>
          <div class="setting-desc">{L('سرورها، اشتراک‌ها، نام و محدودیت دستگاه‌ها، زمان‌بندی‌ها؛ برای انتقال به روتر دیگر', 'Servers, subscriptions, device names and limits, schedules; to move to another router')}</div>
          <div class="row wrap" style="margin-top:8px">
            <Btn onClick={() => { location.href = '/api/system/export'; }}><FileJson size={17} />{L('خروجی گرفتن', 'Export')}</Btn>
            <Btn busy={busy === 'import'} onClick={async () => {
              const f = await pickFile('.json,application/json');
              if (!f) return;
              let data: unknown;
              try { data = JSON.parse(await f.text()); } catch { toast(L('فایل JSON معتبر نیست', 'Not a valid JSON file'), true); return; }
              if (!(await confirmDialog(L('تنظیمات فعلی پنل با این فایل جایگزین شود؟', 'Replace the current panel settings with this file?'), true))) return;
              setBusy('import');
              await run(() => post('/system/import', data), L('تنظیمات وارد شد', 'Settings imported'));
              setBusy('');
            }}><Upload size={17} />{L('وارد کردن', 'Import')}</Btn>
          </div>
        </div>
      </div>
    </Card>
  );
}

interface LuciStatus { installed: boolean; running: boolean; port: number; busy?: string; error?: string }

/** LuciCard: OpenWrt's own interface. Separ removes it so there is only one
 *  interface; it can be put back, or (while installed) opened temporarily. */
export function LuciCard() {
  const [st, setSt] = useState<LuciStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const load = () => get<LuciStatus>('/luci').then(setSt).catch(() => {});
  useEffect(() => { load(); }, []);
  // Removing or reinstalling runs on the router in the background.
  useEffect(() => {
    if (!st?.busy) return;
    const t = setInterval(load, 1500);
    return () => clearInterval(t);
  }, [st?.busy]);
  if (!st) return null;
  const url = `${location.protocol}//${location.hostname}:${st.port}/`;
  const act = async (path: string) => {
    setBusy(true);
    const r = await run(() => post<LuciStatus>(path));
    if (r) setSt(r);
    setBusy(false);
  };

  if (st.busy) {
    return (
      <Card>
        <CardHead icon={<Wrench size={20} />} title={L('رابط LuCI', 'LuCI interface')}
          sub={st.busy === 'removing' ? L('در حال حذف LuCI…', 'Removing LuCI…') : L('در حال دانلود و نصب LuCI؛ ممکن است چند دقیقه طول بکشد…', 'Downloading and installing LuCI; this can take a few minutes…')}
          action={<span class="spinner" />} />
      </Card>
    );
  }

  if (!st.installed) {
    return (
      <Card>
        <CardHead icon={<CheckCircle2 size={20} />} tone="ok" title={L('رابط LuCI حذف شده است', 'LuCI is removed')}
          sub={L('سپر تنها رابط مدیریت روتر است. اگر روزی لازم شد، LuCI را می‌توانید دوباره نصب کنید (به اینترنت نیاز دارد).', 'Separ is the router’s only interface. If you ever need it, LuCI can be installed again (needs internet).')} />
        {st.error && <div class="card-body"><div class="callout danger small">{st.error}</div></div>}
        <div class="card-foot">
          <Btn busy={busy} onClick={async () => {
            if (await confirmDialog(L('LuCI از اینترنت دانلود و نصب می‌شود ولی خاموش می‌ماند تا خودتان روشنش کنید. ادامه می‌دهید؟', 'LuCI will be downloaded and installed, but stays off until you turn it on. Continue?'))) act('/luci/install');
          }}><CloudDownload size={17} />{L('نصب دوبارهٔ LuCI', 'Reinstall LuCI')}</Btn>
        </div>
      </Card>
    );
  }

  return (
    <Card>
      <CardHead icon={<Wrench size={20} />} title={L('رابط LuCI', 'LuCI interface')}
        sub={L('رابط پیش‌فرض OpenWrt هنوز روی روتر نصب است. سپر همهٔ کارها را انجام می‌دهد؛ می‌توانید LuCI را کامل حذف کنید تا فقط یک رابط بماند.', 'OpenWrt’s default interface is still installed. Separ does everything; remove LuCI so only one interface is left.')}
        action={<span class={'badge' + (st.running ? ' warn' : '')}>{st.running ? L('روشن', 'On') : L('خاموش', 'Off')}</span>} />
      {st.error && <div class="card-body"><div class="callout danger small">{st.error}</div></div>}
      <div class="card-foot">
        <Btn kind="danger" busy={busy} onClick={async () => {
          if (await confirmDialog(L('LuCI به‌طور کامل از روتر حذف می‌شود و سپر تنها رابط مدیریت می‌ماند. هر وقت خواستید از همین‌جا دوباره نصبش کنید. ادامه می‌دهید؟', 'LuCI will be removed and Separ stays the only interface. You can reinstall it here any time. Continue?'), true)) act('/luci/remove');
        }}><Trash2 size={17} />{L('حذف کامل LuCI', 'Remove LuCI completely')}</Btn>
        {st.running ? (
          <>
            <a class="btn" href={url} target="_blank" rel="noopener"><ExternalLink size={17} />{L('باز کردن LuCI', 'Open LuCI')}</a>
            <Btn busy={busy} onClick={async () => {
              setBusy(true);
              const r = await run(() => post<LuciStatus>('/luci', { enabled: false }), L('LuCI خاموش شد', 'LuCI turned off'));
              if (r) setSt(r);
              setBusy(false);
            }}>{L('خاموش کردن', 'Turn off')}</Btn>
          </>
        ) : (
          <Btn busy={busy} onClick={async () => {
            setBusy(true);
            const r = await run(() => post<LuciStatus>('/luci', { enabled: true }));
            setBusy(false);
            if (r) {
              setSt(r);
              window.open(url, '_blank', 'noopener');
            }
          }}><Wrench size={17} />{L('روشن کردن موقت', 'Turn on temporarily')}</Btn>
        )}
      </div>
    </Card>
  );
}
