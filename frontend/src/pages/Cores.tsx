import { useEffect, useState } from 'preact/hooks';
import { Cpu, Database, Download, ExternalLink, RotateCcw, ShieldCheck } from 'lucide-preact';
import { get, post, usePoll } from '../api';
import { L, ago, bytes, num } from '../i18n';
import { Badge, Btn, Card, CardHead, Meter, Setting, Skeleton, Toggle, confirmDialog, run } from '../ui';

interface Engine { path: string; version: string }
interface CoreJob { running: boolean; tag?: string; stage?: string; done?: number; total?: number; error?: string; at?: number }
interface CoresResp { main: Engine; compat: Engine; prev?: string; asset: string; job: CoreJob; min: string; pinned: boolean }
interface Release { tag: string; version: string; published: number; prerelease: boolean; url: string }

const newerThanCompat = (v: string) => {
  const [a, b, c] = v.split('.').map(Number);
  return a > 26 || (a === 26 && (b > 3 || (b === 3 && c > 27)));
};

function stageText(j: CoreJob) {
  switch (j.stage) {
    case 'download': return L('در حال دانلود از GitHub…', 'Downloading from GitHub…');
    case 'verify': return L('بررسی امضای فایل (SHA-256)…', 'Checking the SHA-256…');
    case 'test': return L('آزمایش اجرای نسخهٔ جدید روی روتر…', 'Test-running the new version…');
    case 'switch': return L('جایگزینی و راه‌اندازی دوبارهٔ موتور…', 'Switching and restarting the engine…');
    default: return L('در حال کار…', 'Working…');
  }
}

/** XrayCoreCard: pick, update or roll back the Xray version (like 3x-ui). */
export function XrayCoreCard() {
  const { data, reload } = usePoll<CoresResp>('/xray', 0);
  const [rels, setRels] = useState<Release[] | null>(null);
  const [relErr, setRelErr] = useState('');
  const [pick, setPick] = useState('');
  useEffect(() => {
    get<Release[]>('/xray/releases').then((r) => { setRels(r); }).catch((e) => setRelErr(e.message));
  }, []);
  // Poll quickly while an install runs.
  useEffect(() => {
    if (!data?.job.running) return;
    const t = setInterval(reload, 1000);
    return () => clearInterval(t);
  }, [data?.job.running]);
  if (!data) return <Skeleton h={220} />;
  const latest = rels?.find((r) => !r.prerelease);
  const current = data.main.version;
  const sel = pick || latest?.tag || '';
  const selVer = sel.replace(/^v/, '');
  const job = data.job;
  const install = async (tag: string) => {
    const v = tag.replace(/^v/, '');
    const msg = v === current
      ? L(`نسخهٔ ${v} دوباره نصب شود؟`, `Reinstall ${v}?`)
      : L(`Xray ${v} نصب شود؟ اتصال چند ثانیه قطع و وصل می‌شود.`, `Install Xray ${v}? The connection drops for a few seconds.`);
    if (!(await confirmDialog(msg))) return;
    await run(() => post('/xray/install', { tag }));
    reload();
  };
  return (
    <Card>
      <CardHead icon={<Cpu size={20} />} tone="info" title={L('هستهٔ Xray', 'Xray core')}
        sub={L('نسخهٔ موتور اینترنت آزاد را انتخاب یا به‌روز کنید؛ مستقیم از GitHub رسمی XTLS', 'Pick or update the free-internet engine; straight from the official XTLS GitHub')}
        action={latest && current && latest.version !== current && !job.running
          ? <Btn sm kind="primary" onClick={() => install(latest.tag)}><Download size={15} />{L(`به‌روزرسانی به ${latest.version}`, `Update to ${latest.version}`)}</Btn>
          : undefined} />
      <div class="card-body stack">
        <div class="core-now">
          <div>
            <div class="tiny muted">{L('نسخهٔ فعلی', 'Current version')}</div>
            <div class="core-ver" dir="ltr">{current || '—'}</div>
          </div>
          <div class="row wrap" style="gap:6px">
            {latest && current === latest.version && <Badge tone="ok">{L('آخرین نسخه', 'Latest')}</Badge>}
            {latest && current && current !== latest.version && <Badge tone="warn">{L(`نسخهٔ جدید: ${latest.version}`, `New: ${latest.version}`)}</Badge>}
            {data.pinned && <Badge>{L('انتخاب دستی', 'Chosen manually')}</Badge>}
          </div>
        </div>
        {data.compat.version && (
          <div class="small muted">
            {L(`موتور سازگار ${data.compat.version}: فقط برای سرورهای VLESS/Trojan بدون TLS، وقتی نسخهٔ اصلی آن‌ها را نپذیرد.`,
              `Compat engine ${data.compat.version}: only for VLESS/Trojan servers without TLS when the main version refuses them.`)}
          </div>
        )}

        {job.running ? (
          <div class="stack" style="gap:8px">
            <div class="row small"><span class="spinner" style="width:16px;height:16px" />{stageText(job)} <span class="ltr faint">{job.tag}</span></div>
            {job.stage === 'download' && (job.total || 0) > 0 && (
              <><Meter value={((job.done || 0) / (job.total || 1)) * 100} />
                <div class="tiny faint ltr">{bytes(job.done || 0)} / {bytes(job.total || 0)}</div></>
            )}
          </div>
        ) : job.at ? (
          job.stage === 'done'
            ? <div class="callout ok small"><ShieldCheck size={18} /><div>{L(`Xray ${job.tag?.replace(/^v/, '')} نصب شد و موتور با آن کار می‌کند.`, `Xray ${job.tag?.replace(/^v/, '')} is installed and running.`)}</div></div>
            : <div class="callout danger small"><div>{job.error}</div></div>
        ) : null}

        <div class="core-pick">
          <select class="select ltr" value={sel} disabled={!rels || job.running} onChange={(e) => setPick((e.target as HTMLSelectElement).value)}>
            {!rels && <option>{relErr ? L('فهرست نسخه‌ها دریافت نشد', 'Could not load versions') : L('در حال دریافت فهرست…', 'Loading versions…')}</option>}
            {rels?.map((r) => (
              <option value={r.tag}>
                {r.version}{r.prerelease ? ' (pre-release)' : ''}{r.version === current ? ' ✓' : ''} — {new Date(r.published * 1000).toISOString().slice(0, 10)}
              </option>
            ))}
          </select>
          <Btn kind="primary" disabled={!rels || !sel || job.running} onClick={() => install(sel)}>
            <Download size={16} />{selVer === current ? L('نصب دوباره', 'Reinstall') : L('نصب این نسخه', 'Install this version')}
          </Btn>
        </div>
        {relErr && <div class="tiny" style="color:var(--danger-strong)">{relErr}</div>}
        {selVer && newerThanCompat(selVer) && (
          <div class="tiny faint">{L('این نسخه VLESS/Trojan بدون TLS را نمی‌پذیرد؛ چنین سرورهایی خودکار روی موتور سازگار اجرا می‌شوند.', 'This version refuses VLESS/Trojan without TLS; such servers run on the compat engine automatically.')}</div>
        )}
        <div class="tiny faint">{L(`نسخه‌های ${data.min} به بعد نمایش داده می‌شوند. فایل ${data.asset} با امضای SHA-256 رسمی بررسی می‌شود و اگر موتور با نسخهٔ جدید بالا نیاید، خودکار نسخهٔ قبل برمی‌گردد.`,
          `Versions ${data.min} and newer are listed. ${data.asset} is checked against the official SHA-256, and if the engine won't start with it the previous version comes back.`)}</div>
      </div>
      {data.prev && !job.running && (
        <div class="card-foot">
          <Btn onClick={async () => {
            if (await confirmDialog(L(`برگشت به Xray ${data.prev}؟`, `Go back to Xray ${data.prev}?`))) {
              await run(() => post('/xray/rollback'), L('نسخهٔ قبل برگشت', 'Previous version restored'));
              reload();
            }
          }}><RotateCcw size={16} />{L(`برگشت به ${data.prev}`, `Back to ${data.prev}`)}</Btn>
        </div>
      )}
    </Card>
  );
}

interface GeoMeta { source: string; tag: string; updatedAt: number; files?: Record<string, { size: number; sha256: string }> }
interface GeoResp { source: string; sourceUrl: string; downloaded: boolean; meta: GeoMeta; job: CoreJob; autoUpdate: boolean; assetDir: string }

// "202610311033" -> "2026-10-31 10:33"
const tagDate = (t: string) => (/^\d{12}$/.test(t) ? `${t.slice(0, 4)}-${t.slice(4, 6)}-${t.slice(6, 8)} ${t.slice(8, 10)}:${t.slice(10, 12)}` : t);

/** GeoCard: geosite/geoip from chocolate4u/Iran-v2ray-rules, updatable. */
export function GeoCard() {
  const { data, reload } = usePoll<GeoResp>('/geo', 0);
  useEffect(() => {
    if (!data?.job.running) return;
    const t = setInterval(reload, 1000);
    return () => clearInterval(t);
  }, [data?.job.running]);
  if (!data) return <Skeleton h={220} />;
  const job = data.job;
  const m = data.meta;
  const size = (f: string) => m.files?.[f]?.size || 0;
  const uses = [
    { k: 'geosite:ir', t: L('سایت‌های ایرانی (مستقیم)', 'Iranian sites (direct)') },
    { k: 'geoip:ir', t: L('IPهای ایران (مستقیم)', 'Iranian IPs (direct)') },
    { k: 'category-ads-all', t: L('تبلیغات و ردیاب‌ها', 'Ads and trackers') },
    { k: 'malware · phishing', t: L('بدافزار و فیشینگ', 'Malware and phishing') },
  ];
  return (
    <Card>
      <CardHead icon={<Database size={20} />} tone="violet" title={L('فایل‌های جئو (Geosite / GeoIP)', 'Geo files (Geosite / GeoIP)')}
        sub={<>{L('منبع: ', 'Source: ')}<a href={data.sourceUrl} target="_blank" rel="noopener" class="ltr">{data.source} <ExternalLink size={12} /></a>{L(' — هر روز به‌روز می‌شود', ' — updated daily')}</>}
        action={!job.running && (
          <Btn sm kind="primary" onClick={async () => { await run(() => post('/geo/update')); reload(); }}>
            <Download size={15} />{L('به‌روزرسانی از منبع', 'Update from source')}
          </Btn>
        )} />
      <div class="card-body stack">
        {data.downloaded ? (
          <div class="core-now">
            <div>
              <div class="tiny muted">{L('نسخهٔ فایل‌ها', 'Files version')}</div>
              <div class="core-ver" dir="ltr" style="font-size:20px">{tagDate(m.tag) || '—'}</div>
              <div class="tiny faint">{m.updatedAt ? L(`دریافت ${ago(m.updatedAt)}`, `Fetched ${ago(m.updatedAt)}`) : ''}</div>
            </div>
            <div class="stack" style="gap:4px;align-items:flex-end">
              <span class="tiny muted ltr">geosite.dat · {bytes(size('geosite.dat'))}</span>
              <span class="tiny muted ltr">geoip.dat · {bytes(size('geoip.dat'))}</span>
            </div>
          </div>
        ) : (
          <div class="callout warn small"><div>{L('هنوز دانلود نشده؛ تا آن موقع فایل‌های بستهٔ OpenWrt (کم‌حجم‌تر و قدیمی‌تر) استفاده می‌شوند.', 'Not downloaded yet; the smaller, older OpenWrt package files are used until then.')}</div></div>
        )}

        {job.running && (
          <div class="stack" style="gap:8px">
            <div class="row small"><span class="spinner" style="width:16px;height:16px" />
              {job.stage?.startsWith('download') ? L(`دانلود ${job.stage.split(' ')[1]}…`, `Downloading ${job.stage.split(' ')[1]}…`) : L('بررسی و اعمال…', 'Verifying and applying…')}</div>
            {(job.total || 0) > 0 && <Meter value={((job.done || 0) / (job.total || 1)) * 100} />}
          </div>
        )}
        {!job.running && job.at && job.stage === 'failed' && <div class="callout danger small"><div>{job.error}</div></div>}

        <div class="geo-uses">
          {uses.map((u) => <div class="geo-use"><span class="geo-key ltr">{u.k}</span><span>{u.t}</span></div>)}
        </div>
        <Setting title={L('به‌روزرسانی خودکار هفتگی', 'Weekly auto-update')} desc={L('هر هفته جدیدترین نسخه دریافت و با SHA-256 منبع بررسی می‌شود؛ اگر Xray با فایل‌های جدید بالا نیاید، فایل‌های قبلی برمی‌گردند.', 'The newest release is fetched weekly and checked against the source SHA-256; if Xray won’t start with it, the previous files return.')}>
          <Toggle on={data.autoUpdate} onChange={async (v) => { await run(() => post('/geo/settings', { autoUpdate: v }), L('ذخیره شد', 'Saved')); reload(); }} />
        </Setting>
      </div>
      {data.downloaded && !job.running && (
        <div class="card-foot">
          <Btn onClick={async () => {
            if (await confirmDialog(L('فایل‌های قبلی جئو برگردند؟', 'Restore the previous geo files?'))) {
              await run(() => post('/geo/rollback'), L('فایل‌های قبلی برگشت', 'Previous files restored'));
              reload();
            }
          }}><RotateCcw size={16} />{L('برگشت به نسخهٔ قبل', 'Back to the previous files')}</Btn>
        </div>
      )}
    </Card>
  );
}

export function CoresTab() {
  return (
    <div class="stack">
      <XrayCoreCard />
      <GeoCard />
    </div>
  );
}
