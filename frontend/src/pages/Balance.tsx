import { useEffect, useState } from 'preact/hooks';
import { Cable, Combine, Info, Plus, Wifi } from 'lucide-preact';
import { get, post } from '../api';
import { L, lang, num, rate } from '../i18n';
import { Badge, Btn, Card, CardHead, Seg, Setting, Skeleton, Toggle, run } from '../ui';
import { UplinkCard, type Uplink } from './Network';

interface Link {
  name: string; kind: 'cable' | 'wifi'; slot: number; device: string; up: boolean; healthy: boolean;
  ip: string; gateway: string; rttMs: number; weight: number; share: number; rxBps: number; txBps: number;
}
interface BalanceResp {
  status: { enabled: boolean; active: number; links: Link[]; rxBps: number; txBps: number };
  settings: { enabled: boolean; mode?: 'speed' | 'stable'; weights?: Record<string, number> };
  uplinks: Uplink[];
}

const linkName = (l: Link, ups: Uplink[]) => {
  if (l.kind === 'cable') return L('کابل WAN', 'WAN cable');
  const u = ups.find((x) => x.slot === l.slot);
  const n = l.slot === 1 ? L('وای‌فای ۱', 'Wi-Fi 1') : L('وای‌فای ۲', 'Wi-Fi 2');
  return u?.ssid ? `${n} · ${u.ssid}` : n;
};

function Speed({ bps }: { bps: number }) {
  const r = rate(bps);
  return <span class="mono">{r.v} <span class="faint tiny">{r.u}</span></span>;
}

/** BalanceCard: add several internet links together (speed aggregation). */
export function BalanceCard() {
  const [d, setD] = useState<BalanceResp | null>(null);
  const [add, setAdd] = useState(false);
  lang.value;
  const load = () => get<BalanceResp>('/balance').then(setD).catch(() => {});
  useEffect(() => {
    load();
    const t = setInterval(load, 3000);
    return () => clearInterval(t);
  }, []);
  if (!d) return <Card><Skeleton h={220} /></Card>;
  const st = d.status;
  const weights = d.settings.weights || {};
  const save = async (p: Partial<BalanceResp['settings']>) => {
    await run(() => post('/balance', { ...d.settings, ...p }), L('ذخیره شد', 'Saved'));
    load();
  };
  const present = st.links.filter((l) => l.up);
  const wifi1 = d.uplinks.find((u) => u.slot === 1);
  const wifi2 = d.uplinks.find((u) => u.slot === 2);
  const canAddSecond = !!wifi1?.configured && !wifi2?.configured;
  const working = st.enabled && st.active >= 2;

  return (
    <Card>
      <CardHead icon={<Combine size={20} />} tone="violet" title={L('تجمیع سرعت', 'Speed aggregation')}
        sub={L('چند اینترنت (کابل و تا دو وای‌فای) با هم استفاده می‌شوند و سرعتشان جمع می‌شود', 'Several internet links (cable and up to two Wi-Fi) are used together and their speeds add up')}
        action={<Toggle on={d.settings.enabled} onChange={(v) => save({ enabled: v })} />} />
      <div class="card-body stack" style="gap:12px">
        {d.settings.enabled && (
          <Setting title={L('روش تجمیع', 'Aggregation mode')}
            desc={(d.settings.mode || 'speed') === 'speed'
              ? L('هر اتصال نوبتی از اینترنت بعدی می‌رود؛ دانلود منیجر (IDM، ADM) و تست سرعت با چند اتصال، سرعت هر دو را با هم می‌گیرند. اگر سایتی (مثل بعضی بانک‌ها) مدام از حساب بیرونتان کرد، «پایدار» را بزنید.',
                'Each connection takes the next link in turn; download managers (IDM, ADM) and speed tests with several connections get both links’ speed. If a site (some banks) keeps logging you out, choose “Stable”.')
              : L('هر سایت از هر دستگاه همیشه از یک اینترنت می‌رود (برای سایت‌هایی که ورود را به IP گره می‌زنند)؛ سرعت فقط وقتی جمع می‌شود که چند سایت یا چند دستگاه هم‌زمان کار کنند.',
                'Each site from each device always uses one link (for sites that tie a login to the IP); speeds add up only across several sites or devices.')}>
            <Seg value={d.settings.mode || 'speed'} onChange={(m) => save({ mode: m })}
              items={[{ id: 'speed', label: L('حداکثر سرعت', 'Max speed') }, { id: 'stable', label: L('پایدار', 'Stable') }]} />
          </Setting>
        )}
        {d.settings.enabled && (
          working ? (
            <div class="callout ok small"><Combine size={18} /><div>
              <b>{L(`${num(st.active)} اینترنت با هم کار می‌کنند`, `${st.active} links working together`)}</b>
              {L('سرعت فعلی کل: ', 'Total right now: ')}<span class="ltr" style="display:inline-block">↓ <Speed bps={st.rxBps} /> · ↑ <Speed bps={st.txBps} /></span>
            </div></div>
          ) : (
            <div class="callout warn small"><Info size={18} /><div>
              <b>{present.length < 2 ? L('برای تجمیع دست‌کم دو اینترنت لازم است', 'Aggregation needs at least two links') : L('فقط یک اینترنت سالم است', 'Only one link is healthy')}</b>
              {present.length < 2
                ? L('کابل WAN را به یک مودم و وای‌فای را به مودم/هات‌اسپات دیگری وصل کنید (پایین همین صفحه).', 'Connect the WAN cable to one modem and Wi-Fi to another modem or hotspot (below on this page).')
                : L('همهٔ ترافیک فعلاً از اینترنت سالم می‌رود؛ وقتی بقیه برگردند خودکار اضافه می‌شوند.', 'All traffic uses the healthy link for now; the others join again automatically when they recover.')}
            </div></div>
          )
        )}

        <div class="stack" style="gap:0">
          {st.links.map((l) => {
            const configured = l.kind === 'cable' || d.uplinks.find((u) => u.slot === l.slot)?.configured;
            if (!configured && !l.up) return null;
            return (
              <div class="list-item" style="padding-inline:0;flex-wrap:wrap;row-gap:6px">
                <span class={'dot' + (l.healthy ? '' : l.up ? ' warn' : ' bad')} />
                {l.kind === 'cable' ? <Cable size={18} class="faint" /> : <Wifi size={18} class="faint" />}
                <div class="grow" style="min-width:140px">
                  <div style="font-weight:700"><bdi>{linkName(l, d.uplinks)}</bdi></div>
                  <div class="tiny faint">
                    {!l.up ? L('وصل نیست', 'not connected')
                      : !l.healthy ? L('وصل است ولی اینترنت ندارد', 'connected but no internet')
                      : <>{l.ip && <span class="mono">{l.ip}</span>} · <bdi>{num(l.rttMs)} ms</bdi>{working && l.share > 0 && <> · {L('سهم', 'share')} <bdi>{num(Math.round(l.share * 100))}%</bdi></>}</>}
                  </div>
                </div>
                {l.up && <div class="tiny ltr" style="text-align:start;min-width:110px">↓ <Speed bps={l.rxBps} /><br />↑ <Speed bps={l.txBps} /></div>}
                {d.settings.enabled && l.up && (
                  <select class="input" style="width:auto;padding-block:4px" title={L('وزن: سهم این اینترنت از اتصال‌های جدید', 'Weight: this link’s share of new connections')}
                    value={String(weights[l.name] || 1)}
                    onChange={(e) => save({ weights: { ...weights, [l.name]: Number((e.target as HTMLSelectElement).value) } })}>
                    {[1, 2, 3, 4, 5, 6, 7, 8, 9, 10].map((w) => <option value={w}>{L('وزن', 'weight')} {num(w)}</option>)}
                  </select>
                )}
              </div>
            );
          })}
        </div>

        {canAddSecond && !add && (
          <Btn onClick={() => setAdd(true)}><Plus size={16} />{L('افزودن وای‌فای دوم', 'Add a second Wi-Fi')}</Btn>
        )}
        {(add || wifi2?.configured) && (
          <UplinkCard slot={2} otherBand={wifi1?.band || ''} onChange={load} />
        )}

        <div class="callout info small"><Info size={18} /><div>
          <b>{L('تجمیع چطور کار می‌کند', 'How aggregation works')}</b>
          <ul style="margin:4px 0 0;padding-inline-start:18px">
            <li><b>{L('برای دانلود قوی: ', 'For fast downloads: ')}</b>{L('با دانلود منیجر (IDM در ویندوز، ADM در اندروید) و ۸ تا ۱۶ اتصال دانلود کنید؛ دو اینترنت ۲۰ مگ حدود ۴۰ مگ می‌دهند. دانلود مستقیم مرورگر فقط یک اتصال است و سرعت یک اینترنت را می‌گیرد (هیچ روتری بدون سرور واسط نمی‌تواند یک اتصال تکی را بین دو اینترنت پخش کند).',
              'Use a download manager (IDM on Windows, ADM on Android) with 8–16 connections; two 20 Mbit/s links give about 40. A plain browser download is one connection and gets one link’s speed (no router can split a single connection across two links without a server in between).')}</li>
            <li>{L('هر اتصال جدید به یکی از اینترنت‌ها (به نسبت وزن) سپرده می‌شود و تا آخر روی همان می‌ماند. پس وقتی چند دانلود، چند دستگاه یا اینترنت آزاد (که چند اتصال هم‌زمان باز می‌کند) کار می‌کنند، سرعت‌ها جمع می‌شوند.',
              'Each new connection is given to one link (by weight) and stays there, so several downloads, devices or the free internet (which opens many connections) add the speeds up.')}</li>
            <li>{L('هر ۱۰ ثانیه هر اینترنت آزمایش می‌شود؛ اگر یکی قطع شود کنار گذاشته می‌شود و بعد از برگشتن خودکار اضافه می‌شود.', 'Every link is tested every 10 s; one that drops is taken out and rejoins automatically when it recovers.')}</li>
            <li>{L('اگر سرور شما محدودیت تعداد IP دارد (Limit IP در پنل‌های x-ui)، با دو اینترنت دو IP می‌بیند. Mux را هم خاموش بگذارید تا اتصال‌ها پخش شوند.', 'If your server limits IPs per user (x-ui “Limit IP”), it will see two IPs. Keep Mux off so connections can spread.')}</li>
            <li>{L('هر رادیو (۲٫۴ و ۵ گیگ) فقط به یک مودم وصل می‌شود و وای‌فای خودش را روی کانال همان مودم می‌برد.', 'Each radio (2.4 and 5 GHz) joins one modem only, and moves its own Wi-Fi to that modem’s channel.')}</li>
          </ul>
        </div></div>
        {!d.settings.enabled && present.length >= 2 && (
          <Setting title={L('دو اینترنت وصل است', 'Two links are connected')} desc={L('با روشن کردن تجمیع، سرعت هر دو با هم استفاده می‌شود.', 'Turn aggregation on to use both at once.')}>
            <Badge tone="info">{num(present.length)}</Badge>
          </Setting>
        )}
      </div>
    </Card>
  );
}
