import { useEffect, useState } from 'preact/hooks';
import { EyeOff, Globe2, Info, KeyRound, Lock, ShieldBan } from 'lucide-preact';
import { post, usePoll } from '../api';
import { onRefresh } from '../app';
import { L, lang } from '../i18n';
import { Badge, Btn, Card, CardHead, Field, PageHead, Setting, Skeleton, Toggle, run } from '../ui';

interface Provider { id: string; name: string; nameFa: string; servers: string[] | null; doh?: string; domestic: boolean }
interface DnsCfg { provider: string; custom: string; mode: string; blockAds: boolean; logSites: boolean }
interface DnsResp { config: DnsCfg; providers: Provider[]; tunnel: boolean; isp: string[] | null; lockWan: boolean }

export function Dns() {
  const { data, reload } = usePoll<DnsResp>('/dns', 10000);
  const [custom, setCustom] = useState<string | null>(null);
  lang.value;
  useEffect(() => onRefresh(reload), [reload]);
  if (!data) return <Skeleton h={400} />;
  const c = data.config;
  const save = async (p: Partial<DnsCfg>) => {
    await run(() => post('/dns', { ...c, ...p }), L('ذخیره شد', 'Saved'));
    reload();
  };
  const chosen = data.providers.find((p) => p.id === c.provider);
  return (
    <div class="stack">
      <PageHead title={L('DNS و امنیت', 'DNS & security')} sub={L('انتخاب DNS، رمزنگاری آن و مسدودسازی تبلیغات', 'Choose your DNS, encrypt it and block ads')} />
      {data.tunnel ? (
        <div class="callout ok"><Lock size={20} /><div>
          <b>{L('DNS هوشمند (همراه اینترنت آزاد) فعال است', 'Smart DNS (with free internet) is active')}</b>
          {L('نام سایت‌های ایرانی را DNS اپراتور می‌دهد و بقیه رمزنگاری‌شده (DoH) از داخل تونل پرسیده می‌شود؛ پس اپراتور نمی‌تواند جواب‌ها را دست‌کاری کند.',
            'Iranian names are resolved by your ISP; everything else is asked encrypted (DoH) through the tunnel, so the ISP can’t tamper with answers.')}
        </div></div>
      ) : (
        <div class="callout info"><Info size={20} /><div>
          <b>{L('چرا ممکن است DNS انتخابی شما اعمال نشود؟', 'Why your choice may not take effect')}</b>
          {L('بیشتر اپراتورهای ایران درخواست‌های DNS معمولی (پورت ۵۳) را می‌ربایند و خودشان جواب می‌دهند. وقتی اینترنت آزاد روشن است این مشکل وجود ندارد.',
            'Most Iranian ISPs hijack plain DNS (port 53) and answer themselves. With the free internet on, this doesn’t apply.')}
        </div></div>
      )}
      <Card>
        <CardHead icon={<Globe2 size={20} />} title="DNS"
          sub={data.tunnel
            ? L('سرویس‌دهندهٔ انتخابی، داخل تونل و رمزنگاری‌شده استفاده می‌شود (سرویس‌های داخلی مثل شکن داخل تونل معنی ندارند و کلادفلر جایگزین می‌شود)', 'Your choice is used inside the tunnel, encrypted (domestic services like Shecan make no sense there; Cloudflare is used instead)')
            : L('تعیین می‌کند آدرس سایت‌ها چطور پیدا شوند', 'Decides how site names are looked up')}
          action={<Badge tone={data.tunnel ? 'ok' : undefined}>{data.tunnel ? 'DoH' : 'UDP 53'}</Badge>} />
        <div class="card-body">
          <div class="provider-grid">
            {data.providers.map((p) => (
              <button class={'provider' + (c.provider === p.id ? ' on' : '')} onClick={() => save({ provider: p.id })}>
                <div class="provider-name">{lang.value === 'fa' ? p.nameFa : p.name}</div>
                <div class="provider-ips">{p.id === 'isp' ? (data.isp || []).join(' ') || '—' : (p.servers || []).join(' ')}</div>
                {p.domestic && p.id !== 'isp' && <div class="tiny faint">{L('ضدتحریم', 'anti-sanction')}</div>}
              </button>
            ))}
            <button class={'provider' + (c.provider === 'custom' ? ' on' : '')} onClick={() => setCustom(c.custom || '')}>
              <div class="provider-name">{L('دلخواه', 'Custom')}</div>
              <div class="provider-ips">{c.provider === 'custom' ? c.custom : '…'}</div>
            </button>
          </div>
          {custom !== null && (() => {
            const [p1, p2 = ''] = custom.split(/[\s,]+/);
            const setPart = (i: number, v: string) => {
              const parts = [p1 || '', p2 || ''];
              parts[i] = v.trim();
              setCustom(parts.join(' ').trim() === '' ? '' : parts.join(' '));
            };
            return (
              <div class="stack" style="gap:10px;margin-top:14px">
                <div class="grid g2" style="gap:12px">
                  <Field label={L('DNS اصلی', 'Primary DNS')} hint={L('IP مثل 1.1.1.1 یا آدرس DoH', 'An IP like 1.1.1.1 or a DoH URL')}>
                    <input class="input ltr" value={p1 || ''} placeholder="1.1.1.1" onInput={(e) => setPart(0, (e.target as HTMLInputElement).value)} />
                  </Field>
                  <Field label={L('DNS دوم (اختیاری)', 'Secondary DNS (optional)')}>
                    <input class="input ltr" value={p2 || ''} placeholder="1.0.0.1" onInput={(e) => setPart(1, (e.target as HTMLInputElement).value)} />
                  </Field>
                </div>
                <div class="row">
                  <Btn kind="primary" disabled={!(p1 || '').trim()} onClick={async () => { await save({ provider: 'custom', custom: [p1, p2].filter(Boolean).join(' ') }); setCustom(null); }}>{L('ذخیره', 'Save')}</Btn>
                  <Btn onClick={() => setCustom(null)}>{L('انصراف', 'Cancel')}</Btn>
                </div>
              </div>
            );
          })()}
          {chosen && !data.tunnel && chosen.domestic && chosen.id !== 'isp' && (
            <div class="tiny muted" style="margin-top:12px">{L('DNSهای ضدتحریم فقط تحریم سایت‌ها را دور می‌زنند، نه فیلترینگ را.', 'Anti-sanction DNS only bypasses sanctions, not censorship.')}</div>
          )}
        </div>
      </Card>
      <Card>
        <CardHead icon={<ShieldBan size={20} />} tone="danger" title={L('فیلترینگ', 'Filtering')} />
        <div class="card-body" style="padding-block:0">
          <Setting title={L('مسدودسازی تبلیغات', 'Block ads')} desc={L('تبلیغات و ردیاب‌های رایج سایت‌ها و برنامه‌های ایرانی مسدود می‌شوند (وقتی اینترنت آزاد روشن است)', 'Common ads and trackers on Iranian sites/apps are blocked (while the free internet is on)')}>
            <Toggle on={c.blockAds} onChange={(v) => save({ blockAds: v })} />
          </Setting>
        </div>
      </Card>
      <Card>
        <CardHead icon={<KeyRound size={20} />} tone="warn" title={L('دسترسی', 'Access')} />
        <div class="card-body" style="padding-block:0">
          <Setting title={L('قفل پنل و SSH از سمت اینترنت', 'Lock panel and SSH from the internet')}
            desc={L('اتصال به پنل، LuCI و SSH فقط از داخل شبکهٔ خانه ممکن است، حتی اگر کسی پورت را روی مودم باز کرده باشد', 'The panel, LuCI and SSH are reachable only from your home network, even if a port is forwarded on the modem')}>
            <Toggle on={data.lockWan} onChange={async (v) => { await run(() => post('/security', { lockWan: v }), L('ذخیره شد', 'Saved')); reload(); }} />
          </Setting>
        </div>
      </Card>
      <Card>
        <CardHead icon={<EyeOff size={20} />} tone="info" title={L('حریم خصوصی', 'Privacy')} />
        <div class="card-body" style="padding-block:0">
          <Setting title={L('ثبت سایت‌های بازشده', 'Log visited sites')} desc={L('به‌طور پیش‌فرض خاموش است. اگر روشن شود فقط در حافظهٔ موقت می‌ماند و با خاموش شدن روتر پاک می‌شود.', 'Off by default. When on, it stays in RAM only and is erased on reboot.')}>
            <Toggle on={c.logSites} onChange={(v) => save({ logSites: v })} />
          </Setting>
        </div>
      </Card>
    </div>
  );
}
