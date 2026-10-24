import { useEffect, useState } from 'preact/hooks';
import {
  Check, FileUp, Gauge, Link2, ListChecks, Plus, Power, RefreshCw, ScrollText, Server as ServerIcon, ShieldCheck,
  ShieldHalf, Split, Star, Trash2, Wand2, Zap, Globe2, Users, Timer, Ban,
} from 'lucide-preact';
import { del, get, post, usePoll, type ProxyConfig, type ProxyStatus, type Server } from '../api';
import { onRefresh, overview } from '../app';
import { L, ago, lang, num } from '../i18n';
import { Badge, Btn, Card, CardHead, Empty, Field, PageHead, Password, Seg, Setting, Skeleton, Tabs, Toggle, confirmDialog, latClass, run, toast } from '../ui';
import { modeInfo } from './Home';

type Tab = 'servers' | 'protection' | 'log';

interface ProxyResp {
  config: ProxyConfig;
  status: ProxyStatus;
}

function transportLabel(s: Server) {
  const parts = [s.protocol];
  if (s.network && s.network !== 'raw') parts.push(s.network);
  if (s.security && s.security !== 'none') parts.push(s.security);
  return parts.join(' · ');
}

function Latency({ s, testing }: { s: Server; testing: boolean }) {
  if (s.protocol === 'openvpn' && !s.testedAt) return <span class="lat faint" title={L('فقط وقتی فعال است سنجیده می‌شود', 'Measured only while active')}>—</span>;
  if (testing) return <span class="lat faint"><span class="spinner" style="width:14px;height:14px" /></span>;
  if (!s.testedAt) return <span class="lat faint">—</span>;
  if (s.latencyMs < 0) return <span class={'lat bad'} title={s.testError}>{L('خطا', 'fail')}</span>;
  return <span class={'lat ' + latClass(s.latencyMs)}>{num(s.latencyMs)} ms</span>;
}

function StatusCard({ d, reload }: { d: ProxyResp; reload: () => void }) {
  const st = d.status;
  const wanUp = overview.value?.wan.up ?? true;
  const m = modeInfo(st, wanUp);
  const on = d.config.enabled;
  const [busy, setBusy] = useState(false);
  const toggle = async () => {
    if (!on && d.config.servers.length === 0) {
      toast(L('اول یک سرور یا لینک اشتراک اضافه کنید', 'Add a server or subscription first'), true);
      return;
    }
    setBusy(true);
    await run(() => post('/proxy/settings', { enabled: !on }));
    setBusy(false);
    reload();
  };
  return (
    <Card class={'hero' + (m.tone === 'bad' ? ' bad' : m.tone === 'warn' ? ' warn' : '')}>
      <div class="hero-main">
        <div class={'status-ring ' + (m.tone === 'ok' ? '' : m.tone)}>{m.icon}</div>
        <div class="grow">
          <h2 class="hero-title">{on ? m.title : L('فیلترشکن خاموش است', 'Proxy is off')}</h2>
          <div class="muted small row wrap" style="gap:6px">
            {st.server ? (
              <>
                <bdi>{st.server.name}</bdi>
                <Badge>{transportLabel(st.server)}</Badge>
                {st.latencyMs > 0 && <Badge tone="ok">{num(st.latencyMs)} ms</Badge>}
                {st.since > 0 && st.mode === 'connected' && <span class="faint">· {L('از', 'since')} {ago(st.since)}</span>}
              </>
            ) : (
              <span>{L('همهٔ دستگاه‌ها مستقیم به اینترنت وصل‌اند', 'All devices go direct')}</span>
            )}
          </div>
          {on && st.lastError && st.mode !== 'connected' && <div class="small" style="color:var(--danger-strong);margin-top:4px">{st.lastError}</div>}
        </div>
        <Btn kind={on ? undefined : 'primary'} busy={busy} onClick={toggle}>
          <Power size={18} /> {on ? L('قطع اتصال', 'Disconnect') : L('اتصال', 'Connect')}
        </Btn>
      </div>
    </Card>
  );
}

function readFile(accept: string): Promise<string> {
  return new Promise((resolve, reject) => {
    const inp = document.createElement('input');
    inp.type = 'file';
    inp.accept = accept;
    inp.onchange = () => {
      const f = inp.files?.[0];
      if (!f) return reject(new Error('no file'));
      if (f.size > 512 * 1024) return reject(new Error(L('فایل خیلی بزرگ است', 'File too large')));
      f.text().then(resolve, reject);
    };
    inp.click();
  });
}

type AddKind = 'links' | 'wg' | 'ovpn';

function AddCard({ onAdded }: { onAdded: () => void }) {
  const [kind, setKind] = useState<AddKind>('links');
  const [text, setText] = useState('');
  const [ovpn, setOvpn] = useState({ name: '', config: '', username: '', password: '' });
  const [busy, setBusy] = useState(false);
  const done = () => {
    onAdded();
    setTimeout(onAdded, 3000);
  };
  const addLinks = async () => {
    setBusy(true);
    try {
      const r = await post<{ added?: number; bad?: string[]; subscriptions?: number }>('/proxy/servers', { text });
      if (r.subscriptions !== undefined) {
        toast(L(`${num(r.subscriptions)} اشتراک اضافه شد؛ در حال دریافت سرورها…`, `${r.subscriptions} subscription(s) added; fetching servers…`));
      } else {
        toast(L(`${num(r.added || 0)} سرور اضافه شد`, `${r.added} server(s) added`) + (r.bad?.length ? L(` · ${num(r.bad.length)} لینک نامعتبر`, ` · ${r.bad.length} invalid`) : ''));
      }
      setText('');
      done();
    } catch (e: any) {
      toast(e.message, true);
    } finally {
      setBusy(false);
    }
  };
  const addOvpn = async () => {
    setBusy(true);
    const ok = await run(() => post('/proxy/openvpn', ovpn), L('پروفایل OpenVPN اضافه شد', 'OpenVPN profile added'));
    setBusy(false);
    if (ok) {
      setOvpn({ name: '', config: '', username: '', password: '' });
      done();
    }
  };
  const loadFile = async (accept: string, set: (t: string) => void) => {
    try {
      set(await readFile(accept));
    } catch (e: any) {
      if (e.message !== 'no file') toast(e.message, true);
    }
  };
  return (
    <Card>
      <CardHead icon={<Plus size={20} />} title={L('افزودن سرور', 'Add a server')}
        sub={L('همهٔ نوع‌ها در یک فهرست قرار می‌گیرند و نگهبان بینشان جابه‌جا می‌شود', 'All types share one list, and the watchdog fails over between them')}
        action={<Seg value={kind} onChange={setKind} items={[
          { id: 'links', label: L('لینک / اشتراک', 'Links') },
          { id: 'wg', label: 'WireGuard' },
          { id: 'ovpn', label: 'OpenVPN' },
        ]} />} />
      {kind === 'links' && (
        <div class="card-body stack" style="gap:12px">
          <div class="small muted">{L('لینک‌های vless، vmess، trojan، ss و wireguard یا آدرس اشتراک را بچسبانید؛ چند خط با هم هم می‌شود', 'Paste vless/vmess/trojan/ss/wireguard links or a subscription URL; multiple lines are fine')}</div>
          <textarea class="textarea" rows={4} placeholder="vless://…  /  https://panel.example.com/sub/…" value={text}
            onInput={(e) => setText((e.target as HTMLTextAreaElement).value)} spellcheck={false} />
          <div class="row">
            <Btn kind="primary" busy={busy} disabled={!text.trim()} onClick={addLinks}><Plus size={18} />{L('افزودن', 'Add')}</Btn>
            <Btn onClick={async () => {
              try {
                setText(await navigator.clipboard.readText());
              } catch {
                toast(L('دسترسی به کلیپ‌بورد ممکن نیست؛ دستی بچسبانید', 'Clipboard not available; paste manually'), true);
              }
            }}><Link2 size={17} />{L('چسباندن', 'Paste')}</Btn>
          </div>
        </div>
      )}
      {kind === 'wg' && (
        <div class="card-body stack" style="gap:12px">
          <div class="small muted">{L('محتوای فایل .conf وایرگارد (یا WARP) را بچسبانید یا فایل را انتخاب کنید. وایرگارد داخل همان موتور اجرا می‌شود، پس تفکیک ایران و قطع اضطراری برایش هم کار می‌کند.', 'Paste a WireGuard (or WARP) .conf or pick the file. It runs inside the same engine, so Iran split and the kill switch apply too.')}</div>
          <textarea class="textarea" rows={7} placeholder={'[Interface]\nPrivateKey = …\nAddress = …\n\n[Peer]\nPublicKey = …\nEndpoint = host:51820'} value={text}
            onInput={(e) => setText((e.target as HTMLTextAreaElement).value)} spellcheck={false} />
          <div class="row">
            <Btn kind="primary" busy={busy} disabled={!text.includes('[Interface]')} onClick={addLinks}><Plus size={18} />{L('افزودن', 'Add')}</Btn>
            <Btn onClick={() => loadFile('.conf,text/plain', setText)}><FileUp size={17} />{L('انتخاب فایل', 'Choose file')}</Btn>
          </div>
        </div>
      )}
      {kind === 'ovpn' && (
        <div class="card-body stack" style="gap:12px">
          <div class="small muted">{L('فایل .ovpn را انتخاب کنید. تونل OpenVPN بدون دست زدن به مسیرهای روتر بالا می‌آید و ترافیک از همان مسیر هوشمند (تفکیک ایران، DNS امن، قطع اضطراری) به آن فرستاده می‌شود.', 'Pick an .ovpn file. The tunnel comes up without touching the router’s routes, and traffic is sent into it through the same smart path (Iran split, secure DNS, kill switch).')}</div>
          <div class="row wrap">
            <Btn onClick={() => loadFile('.ovpn,.conf', (t) => setOvpn({ ...ovpn, config: t }))}><FileUp size={17} />{ovpn.config ? L('فایل انتخاب شد ✓', 'File loaded ✓') : L('انتخاب فایل .ovpn', 'Choose .ovpn file')}</Btn>
          </div>
          <div class="grid g3" style="gap:12px">
            <Field label={L('نام (اختیاری)', 'Name (optional)')}><input class="input" value={ovpn.name} onInput={(e) => setOvpn({ ...ovpn, name: (e.target as HTMLInputElement).value })} /></Field>
            <Field label={L('نام کاربری', 'Username')}><input class="input ltr" value={ovpn.username} autoComplete="off" onInput={(e) => setOvpn({ ...ovpn, username: (e.target as HTMLInputElement).value })} /></Field>
            <Field label={L('رمز', 'Password')}><Password value={ovpn.password} onInput={(v) => setOvpn({ ...ovpn, password: v })} /></Field>
          </div>
          <div class="row"><Btn kind="primary" busy={busy} disabled={!ovpn.config} onClick={addOvpn}><Plus size={18} />{L('افزودن', 'Add')}</Btn></div>
          <div class="tiny faint">{L('سرورهای OpenVPN فقط وقتی فعال هستند سنجیده می‌شوند و در جابه‌جایی خودکار شرکت نمی‌کنند.', 'OpenVPN servers are measured only while active and are not used for automatic failover.')}</div>
        </div>
      )}
    </Card>
  );
}

function Subscriptions({ d, reload }: { d: ProxyResp; reload: () => void }) {
  const [busy, setBusy] = useState<string | null>(null);
  if (d.config.subscriptions.length === 0) return null;
  return (
    <Card>
      <CardHead icon={<Link2 size={20} />} title={L('اشتراک‌ها', 'Subscriptions')}
        sub={d.config.subAutoUpdate ? L('هر ۶ ساعت خودکار به‌روز می‌شوند', 'Auto-updated every 6 hours') : L('به‌روزرسانی خودکار خاموش است', 'Auto-update is off')}
        action={<Btn sm busy={busy === '*'} onClick={async () => {
          setBusy('*');
          for (const s of d.config.subscriptions) await run(() => post(`/proxy/subs/${s.id}/update`));
          setBusy(null);
          reload();
        }}><RefreshCw size={15} />{L('به‌روزرسانی همه', 'Update all')}</Btn>} />
      {d.config.subscriptions.map((s) => (
        <div class="list-item">
          <div class="grow" style="min-width:0">
            <div class="row" style="gap:8px">
              <b class="truncate">{s.name}</b>
              <Badge tone={s.error ? 'danger' : 'ok'}>{L(`${num(s.count)} سرور`, `${s.count} servers`)}</Badge>
            </div>
            <div class="tiny faint truncate ltr" style="text-align:start">{s.url}</div>
            {s.error ? <div class="tiny" style="color:var(--danger-strong)">{s.error}</div>
              : <div class="tiny faint">{L('آخرین به‌روزرسانی: ', 'Updated: ')}{ago(s.updatedAt || 0)}</div>}
          </div>
          <Btn sm icon busy={busy === s.id} aria-label={L('به‌روزرسانی', 'Update')} onClick={async () => {
            setBusy(s.id);
            await run(() => post(`/proxy/subs/${s.id}/update`), L('به‌روز شد', 'Updated'));
            setBusy(null);
            reload();
          }}><RefreshCw size={15} /></Btn>
          <Btn sm icon kind="danger" aria-label={L('حذف', 'Delete')} onClick={async () => {
            if (await confirmDialog(L('این اشتراک و سرورهایش حذف شوند؟', 'Delete this subscription and its servers?'), true)) {
              await run(() => del(`/proxy/subs/${s.id}`), L('حذف شد', 'Deleted'));
              reload();
            }
          }}><Trash2 size={15} /></Btn>
        </div>
      ))}
    </Card>
  );
}

function ServerList({ d, reload, setData }: { d: ProxyResp; reload: () => void; setData: (d: ProxyResp) => void }) {
  const [testing, setTesting] = useState<Set<string>>(new Set());
  const [filter, setFilter] = useState('');
  const servers = d.config.servers;
  const subName = (id?: string) => d.config.subscriptions.find((s) => s.id === id)?.name;
  const active = d.status.activeId || d.config.activeId;
  const pref = d.config.preferredId;

  const testAll = async (ids?: string[]) => {
    const set = new Set(ids || servers.map((s) => s.id));
    setTesting(set);
    try {
      await post('/proxy/test', ids ? { ids } : {});
    } catch (e: any) {
      toast(e.message, true);
    }
    setTesting(new Set());
    reload();
  };

  const select = async (id: string) => {
    setData({ ...d, config: { ...d.config, activeId: id, preferredId: id }, status: { ...d.status, activeId: id } });
    await run(() => post('/proxy/select', { id }), L('سرور انتخاب شد', 'Server selected'));
    reload();
  };

  const shown = servers.filter((s) => !filter || (s.name + s.address).toLowerCase().includes(filter.toLowerCase()));
  const sorted = [...shown].sort((a, b) => {
    const la = a.latencyMs > 0 ? a.latencyMs : 1e9;
    const lb = b.latencyMs > 0 ? b.latencyMs : 1e9;
    return la - lb;
  });
  const failed = servers.filter((s) => s.latencyMs < 0 && !s.subId).length;

  return (
    <Card>
      <CardHead icon={<ServerIcon size={20} />} title={L('سرورها', 'Servers')}
        sub={servers.length ? L(`${num(servers.length)} سرور · مرتب بر اساس سرعت`, `${servers.length} servers · sorted by latency`) : undefined}
        action={servers.length > 0 && (
          <Btn sm kind="primary" busy={testing.size > 0} onClick={() => testAll()}><Gauge size={15} />{L('آزمایش همه', 'Test all')}</Btn>
        )} />
      {servers.length === 0 ? (
        <Empty icon={<ServerIcon size={34} />} text={L('هنوز سروری اضافه نشده است', 'No servers yet')} />
      ) : (
        <>
          {servers.length > 6 && (
            <div class="card-body" style="padding-bottom:12px">
              <input class="input" placeholder={L('جستجوی سرور…', 'Search servers…')} value={filter} onInput={(e) => setFilter((e.target as HTMLInputElement).value)} />
            </div>
          )}
          <div class={'srv' + (!pref ? ' active' : '')} onClick={() => post('/proxy/select', { id: '' }).then(reload)}>
            <span class="srv-radio" />
            <Wand2 size={18} class="faint" />
            <div class="grow">
              <b>{L('خودکار', 'Automatic')}</b>
              <div class="tiny faint">{L('نگهبان هر وقت لازم شد سریع‌ترین سرور سالم را انتخاب می‌کند', 'The watchdog picks the fastest working server when needed')}</div>
            </div>
          </div>
          {sorted.map((s) => (
            <div class={'srv' + (s.id === pref ? ' active' : '')} onClick={() => select(s.id)}>
              <span class="srv-radio" />
              <div class="grow" style="min-width:0">
                <div class="row" style="gap:6px">
                  <b class="truncate"><bdi>{s.name}</bdi></b>
                  {s.id === active && d.status.mode === 'connected' && <Badge tone="ok"><Check size={12} />{L('وصل', 'live')}</Badge>}
                  {s.id === pref && <Star size={14} style="color:var(--warn)" fill="currentColor" />}
                </div>
                <div class="tiny faint row wrap" style="gap:6px">
                  <span class="ltr">{transportLabel(s)}</span>
                  {subName(s.subId) && <span>· {subName(s.subId)}</span>}
                </div>
              </div>
              <Latency s={s} testing={testing.has(s.id)} />
              <Btn sm icon kind="ghost" aria-label={L('آزمایش', 'Test')} onClick={(e: Event) => { e.stopPropagation(); testAll([s.id]); }}>
                <Zap size={15} />
              </Btn>
              {!s.subId && (
                <Btn sm icon kind="ghost" aria-label={L('حذف', 'Delete')} onClick={async (e: Event) => {
                  e.stopPropagation();
                  if (await confirmDialog(L(`«${s.name}» حذف شود؟`, `Delete “${s.name}”?`), true)) {
                    await run(() => del(`/proxy/servers/${s.id}`));
                    reload();
                  }
                }}><Trash2 size={15} /></Btn>
              )}
            </div>
          ))}
          {failed > 0 && (
            <div class="card-foot">
              <Btn sm kind="danger" onClick={async () => {
                if (await confirmDialog(L(`${num(failed)} سرور خراب حذف شوند؟`, `Delete ${failed} failing servers?`), true)) {
                  await run(() => post('/proxy/servers/delete', { failed: true }), L('حذف شد', 'Deleted'));
                  reload();
                }
              }}><Trash2 size={15} />{L('حذف سرورهای خراب', 'Remove failing servers')}</Btn>
            </div>
          )}
        </>
      )}
    </Card>
  );
}

function Protection({ d, reload }: { d: ProxyResp; reload: () => void }) {
  const c = d.config;
  const set = async (k: keyof ProxyConfig, v: boolean) => {
    await run(() => post('/proxy/settings', { [k]: v }), L('ذخیره شد', 'Saved'));
    reload();
  };
  return (
    <div class="stack">
      <Card>
        <CardHead icon={<Split size={20} />} tone="violet" title={L('تفکیک ترافیک ایران', 'Iran traffic split')}
          sub={L('سایت‌ها و برنامه‌های ایرانی مستقیم می‌روند، نه از تونل؛ سریع‌تر و بدون هزینهٔ اضافهٔ حجم', 'Iranian sites go direct, not through the tunnel: faster and no wasted quota')} />
        <div class="card-body" style="padding-block:0">
          <Setting title={L('تفکیک فعال باشد', 'Enable split')}
            desc={L('بیش از ۲۰۰۰ محدودهٔ IP ایران در خود هستهٔ روتر کنار گذاشته می‌شوند و دامنه‌های ایرانی هم شناسایی می‌شوند', '2000+ Iranian IP ranges bypass at kernel level, plus Iranian domains are detected')}>
            <Toggle on={c.splitIran} onChange={(v) => set('splitIran', v)} />
          </Setting>
        </div>
      </Card>
      <Card>
        <CardHead icon={<ShieldHalf size={20} />} tone="danger" title={L('محافظت در برابر نشت', 'Leak protection')}
          sub={L('اگر تونل قطع شد، چه اتفاقی بیفتد', 'What happens when the tunnel goes down')} />
        <div class="card-body" style="padding-block:0">
          <Setting title={L('قطع اضطراری (Kill Switch)', 'Kill switch')}
            desc={c.killSwitch
              ? L('روشن: اگر هیچ سروری جواب ندهد، ترافیک خارجی مسدود می‌شود (سایت‌های ایرانی کار می‌کنند)', 'On: if no server works, foreign traffic is blocked (Iranian sites keep working)')
              : L('خاموش: اگر هیچ سروری جواب ندهد، اینترنت مستقیم ادامه پیدا می‌کند', 'Off: if no server works, internet continues directly')}>
            <Toggle on={c.killSwitch} onChange={(v) => set('killSwitch', v)} />
          </Setting>
          <Setting title={L('بستن IPv6 دستگاه‌ها', 'Block client IPv6')}
            desc={L('دستگاه‌ها فقط با IPv4 بیرون می‌روند تا ترافیک از کنار تونل نشت نکند', 'Clients use IPv4 only so traffic can’t leak around the tunnel')}>
            <Toggle on={c.blockIPv6} onChange={(v) => set('blockIPv6', v)} />
          </Setting>
          <Setting title={L('اینترنت آزاد برای شبکهٔ مهمان', 'Free internet for guests')}
            desc={L('مهمان‌ها هم از تونل استفاده کنند (پیش‌فرض خاموش)', 'Guest network also uses the tunnel (off by default)')}>
            <Toggle on={c.guestProxy} onChange={(v) => set('guestProxy', v)} />
          </Setting>
        </div>
      </Card>
      <Card>
        <CardHead icon={<Timer size={20} />} tone="info" title={L('نگهبان اتصال', 'Connection watchdog')}
          sub={L('هر ۱۵ ثانیه با ترافیک واقعی سلامت تونل را می‌سنجد و خراب‌ها را خودش ترمیم می‌کند', 'Checks the tunnel with real traffic every 15 s and repairs it automatically')} />
        <div class="card-body" style="padding-block:0">
          <Setting title={L('انتخاب خودکار سریع‌ترین سرور', 'Auto-pick the fastest server')}
            desc={L('هر ۱۵ دقیقه می‌سنجد و فقط وقتی سروری به‌وضوح سریع‌تر باشد جابه‌جا می‌شود', 'Re-measures every 15 min and only switches for a clear win')}>
            <Toggle on={c.autoBest} onChange={(v) => set('autoBest', v)} />
          </Setting>
          <Setting title={L('به‌روزرسانی خودکار اشتراک‌ها', 'Auto-update subscriptions')} desc={L('هر ۶ ساعت', 'Every 6 hours')}>
            <Toggle on={c.subAutoUpdate} onChange={(v) => set('subAutoUpdate', v)} />
          </Setting>
        </div>
        <div class="card-body small muted" style="border-top:1px solid var(--border)">
          <div class="row" style="align-items:flex-start;gap:8px"><ListChecks size={16} style="margin-top:3px;flex-shrink:0" />
            <span>{L('اولین خطا: همان سرور دوباره وصل می‌شود. خطای دوم: همهٔ سرورها آزمایش و سریع‌ترین جایگزین می‌شود. سرور دلخواه شما هر ۳ دقیقه دوباره امتحان می‌شود و وقتی سالم شد برمی‌گردد. اگر موتور پشت سر هم از کار بیفتد، به حالت امن می‌رود.',
              'First failure: reconnect the same server. Second: test all and switch to the fastest. Your preferred server is retried every 3 minutes and restored when healthy. If the engine keeps crashing, safe mode kicks in.')}</span>
          </div>
        </div>
      </Card>
    </div>
  );
}

function Log({ d }: { d: ProxyResp }) {
  const [logs, setLogs] = useState('');
  useEffect(() => {
    get<string>('/proxy/logs').then(setLogs).catch(() => {});
  }, []);
  const evs = [...d.status.events].reverse();
  return (
    <div class="stack">
      <Card>
        <CardHead icon={<ScrollText size={20} />} title={L('رویدادهای نگهبان', 'Watchdog events')} />
        <div class="card-body">
          {evs.length === 0 && <div class="muted">{L('رویدادی نیست', 'No events')}</div>}
          {evs.map((e) => (
            <div class="row" style="align-items:flex-start;padding:6px 0">
              <span class={'dot' + (e.kind === 'error' ? ' bad' : e.kind === 'warn' ? ' warn' : '')} style="margin-top:9px" />
              <span class="ltr small grow" style="text-align:start">{e.msg}</span>
              <span class="tiny faint">{ago(e.at)}</span>
            </div>
          ))}
        </div>
      </Card>
      <Card>
        <CardHead icon={<ScrollText size={20} />} title={L('گزارش موتور Xray', 'Xray engine log')} />
        <div class="card-body"><pre class="log">{logs || '—'}</pre></div>
      </Card>
    </div>
  );
}

export function Proxy() {
  const [tab, setTab] = useState<Tab>('servers');
  const { data, reload, setData } = usePoll<ProxyResp>('/proxy', 4000);
  lang.value;
  useEffect(() => onRefresh(reload), [reload]);
  return (
    <div class="stack">
      <PageHead title={L('اینترنت آزاد', 'Free internet')} sub={L('سرور را یک بار روی روتر وارد کنید تا همهٔ دستگاه‌های خانه بدون هیچ برنامه‌ای از آن استفاده کنند', 'Set up your server once on the router and every device at home uses it without any app')} />
      {!data ? <Skeleton h={140} /> : <StatusCard d={data} reload={reload} />}
      <Tabs value={tab} onChange={setTab} items={[
        { id: 'servers', label: L('سرورها', 'Servers'), icon: <Globe2 size={17} /> },
        { id: 'protection', label: L('محافظت', 'Protection'), icon: <ShieldCheck size={17} /> },
        { id: 'log', label: L('گزارش', 'Log'), icon: <ScrollText size={17} /> },
      ]} />
      {data && tab === 'servers' && (
        <>
          <AddCard onAdded={reload} />
          <Subscriptions d={data} reload={reload} />
          <ServerList d={data} reload={reload} setData={setData} />
        </>
      )}
      {data && tab === 'protection' && <Protection d={data} reload={reload} />}
      {data && tab === 'log' && <Log d={data} />}
      <div class="tiny faint row" style="justify-content:center;gap:6px"><Users size={13} /><Ban size={13} />
        {L('دستگاه‌هایی که باید مستقیم بروند (مثل تلویزیون) را از صفحهٔ دستگاه‌ها تنظیم کنید', 'Set devices that must go direct (e.g. a TV) on the Devices page')}
      </div>
    </div>
  );
}
