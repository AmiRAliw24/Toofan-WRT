import { signal } from '@preact/signals';
import { useEffect, useErrorBoundary, useState } from 'preact/hooks';
import type { ComponentChildren } from 'preact';
import {
  Bell, Check, ChevronsLeft, ChevronsRight, Globe, Home as HomeIcon, Languages, LogOut, Menu, Palette, RefreshCw,
  Settings, Shield, Smartphone, Tornado, Wifi, Lock, Gamepad2,
} from 'lucide-preact';
import { authed, get, post, type Overview } from './api';
import { L, lang, setLang, num, ago, digits } from './i18n';
import { Btn, ConfirmHost, Modal, Toasts } from './ui';
import { Home } from './pages/Home';
import { Internet } from './pages/Internet';
import { Proxy } from './pages/Proxy';
import { Devices } from './pages/Devices';
import { WifiPage } from './pages/Wifi';
import { Dns } from './pages/Dns';
import { Gaming } from './pages/Gaming';
import { System } from './pages/System';
import { Login } from './pages/Login';

export type Route = 'home' | 'internet' | 'proxy' | 'devices' | 'wifi' | 'gaming' | 'dns' | 'system';

function readRoute(): Route {
  const r = location.hash.replace(/^#\/?/, '').split('?')[0] as Route;
  return ['home', 'internet', 'proxy', 'devices', 'wifi', 'gaming', 'dns', 'system'].includes(r) ? r : 'home';
}

export const route = signal<Route>(readRoute());
export function go(r: Route) {
  location.hash = '/' + r;
}

/** Shared overview, polled once for the whole app. */
export const overview = signal<Overview | null>(null);
// Remember when each overview arrived, for the clock's skew check.
function stamp(o: Overview): Overview {
  (o as any).__fetchedAt = Date.now();
  return o;
}
export const noPassword = signal(false);
let refreshers: (() => void)[] = [];
export function onRefresh(f: () => void) {
  refreshers.push(f);
  return () => (refreshers = refreshers.filter((x) => x !== f));
}

// ---- theme ----
type Theme = 'light' | 'dawn' | 'dark' | 'hacker';
const themes: { id: Theme; name: () => string; desc: () => string; swatch: string[] }[] = [
  { id: 'light', name: () => L('روشن', 'Light'), desc: () => L('فیروزه‌ای و تمیز', 'Clean teal'), swatch: ['#f2f6f6', '#0d8a7f', '#6366f1'] },
  { id: 'dawn', name: () => L('سپیده‌دم', 'Dawn'), desc: () => L('کاغذی گرم با نیلی', 'Warm paper, indigo'), swatch: ['#f6f1e8', '#5b45e0', '#d4760a'] },
  { id: 'dark', name: () => L('تیره', 'Dark'), desc: () => L('تیرهٔ عمیق با درخشش ملایم', 'Deep dark with a soft glow'), swatch: ['#091213', '#2cc3b1', '#7376f2'] },
  { id: 'hacker', name: () => L('هکری', 'Hacker'), desc: () => L('ترمینال سبز فسفری', 'Phosphor terminal'), swatch: ['#030805', '#39ff7a', '#2f8fe0'] },
];
function initialTheme(): Theme {
  try {
    const t = localStorage.getItem('theme') as Theme;
    if (themes.some((x) => x.id === t)) return t;
  } catch {}
  return matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
}
const theme = signal<Theme>(initialTheme());
function applyTheme() {
  document.documentElement.dataset.theme = theme.value;
}
function setTheme(t: Theme) {
  theme.value = t;
  try { localStorage.setItem('theme', t); } catch {}
  applyTheme();
}
applyTheme();

// ---- sidebar: full or icon rail (desktop) ----
const rail = signal(((): boolean => { try { return localStorage.getItem('sidebar') === 'rail'; } catch { return false; } })());
function toggleRail() {
  rail.value = !rail.value;
  try { localStorage.setItem('sidebar', rail.value ? 'rail' : 'full'); } catch {}
}

function ThemePicker() {
  const [open, setOpen] = useState(false);
  useEffect(() => {
    if (!open) return;
    const close = (e: Event) => {
      if (e instanceof KeyboardEvent && e.key !== 'Escape') return;
      if (e.type === 'pointerdown' && (e.target as Element).closest?.('.theme-pop, .theme-btn')) return;
      setOpen(false);
    };
    window.addEventListener('pointerdown', close);
    window.addEventListener('keydown', close);
    return () => { window.removeEventListener('pointerdown', close); window.removeEventListener('keydown', close); };
  }, [open]);
  return (
    <div class="theme-wrap">
      <Btn icon class="theme-btn" aria-label={L('تم', 'Theme')} title={L('تم', 'Theme')} aria-expanded={open} onClick={() => setOpen(!open)}>
        <Palette size={18} />
      </Btn>
      {open && (
        <div class="theme-pop" role="menu">
          <div class="theme-pop-title">{L('تم پنل', 'Panel theme')}</div>
          {themes.map((t) => (
            <button role="menuitemradio" aria-checked={theme.value === t.id} class={'theme-opt' + (theme.value === t.id ? ' active' : '')}
              onClick={() => { setTheme(t.id); setOpen(false); }}>
              <span class="theme-swatch" style={{ background: t.swatch[0] }}>
                <i style={{ background: t.swatch[1] }} /><i style={{ background: t.swatch[2] }} />
              </span>
              <span class="grow">
                <span class="theme-name">{t.name()}</span>
                <span class="theme-desc">{t.desc()}</span>
              </span>
              {theme.value === t.id && <Check size={16} />}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

/** Clock shows the router's own time (its timezone, its clock), ticking
 *  every second; a dot warns when the router clock is off. */
function Clock() {
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, []);
  const c = overview.value?.clock;
  if (!c) return null;
  const skew = c.unixMs - (overview.value as any).__fetchedAt;
  const router = new Date(now + (Number.isFinite(skew) ? skew : 0) + c.offsetMin * 60000);
  const p2 = (n: number) => String(n).padStart(2, '0');
  const fa = lang.value === 'fa';
  const d = (s: string) => (fa ? digits(s) : s);
  const date = router.toLocaleDateString(fa ? 'fa-IR-u-ca-persian' : 'en-GB', { timeZone: 'UTC', weekday: 'short', day: 'numeric', month: 'short' });
  const off = Math.abs(skew) > 120000;
  return (
    <div class={'clock' + (off ? ' off' : '')} title={off
      ? L('ساعت روتر با ساعت این دستگاه بیش از ۲ دقیقه فرق دارد؛ زمان‌بندی‌ها ممکن است درست اجرا نشوند.', 'The router clock differs from this device by over 2 minutes; schedules may run at the wrong time.')
      : L('ساعت روتر', 'Router time')}>
      <span class="clock-time ltr">
        {d(p2(router.getUTCHours()))}<span class="clock-sep">:</span>{d(p2(router.getUTCMinutes()))}
        <span class="clock-sec"><span class="clock-sep">:</span>{d(p2(router.getUTCSeconds()))}</span>
      </span>
      <span class="clock-date">{date}</span>
      {off && <span class="clock-warn" aria-hidden="true" />}
    </div>
  );
}

interface NavDef {
  id: Route;
  icon: ComponentChildren;
  label: () => string;
  sub: () => string;
}

const navs: NavDef[] = [
  { id: 'home', icon: <HomeIcon size={19} />, label: () => L('خانه', 'Home'), sub: () => L('نمای کلی و وضعیت', 'Overview and status') },
  { id: 'internet', icon: <Globe size={19} />, label: () => L('اینترنت', 'Internet'), sub: () => L('کیفیت خط و تست سرعت', 'Line quality and speed') },
  { id: 'proxy', icon: <Shield size={19} />, label: () => L('اینترنت آزاد', 'Free internet'), sub: () => L('سرورها، تفکیک ایران، محافظت', 'Servers, Iran split, protection') },
  { id: 'devices', icon: <Smartphone size={19} />, label: () => L('دستگاه‌ها', 'Devices'), sub: () => L('سرعت، محدودیت و زمان‌بندی', 'Speed, limits and schedules') },
  { id: 'wifi', icon: <Wifi size={19} />, label: () => L('وای‌فای', 'Wi-Fi'), sub: () => L('۲٫۴ و ۵ گیگاهرتز و مهمان', '2.4 / 5 GHz and guest') },
  { id: 'gaming', icon: <Gamepad2 size={19} />, label: () => L('بازی و کیفیت اتصال', 'Gaming'), sub: () => L('NAT، UPnP، تأخیر و اولویت', 'NAT, UPnP, latency, priority') },
  { id: 'dns', icon: <Lock size={19} />, label: () => L('DNS و امنیت', 'DNS & security'), sub: () => L('DNS امن و ضدتبلیغ', 'Secure DNS, ad blocking') },
  { id: 'system', icon: <Settings size={19} />, label: () => L('سیستم', 'System'), sub: () => L('سخت‌افزار، پشتیبان و رمز', 'Hardware, backup, password') },
];

function proxyDot(o: Overview | null): string | null {
  if (!o) return null;
  switch (o.proxy.mode) {
    case 'connected': return '';
    case 'connecting':
    case 'degraded': return 'warn';
    case 'off': return null;
    default: return 'bad';
  }
}

function NavItem(p: { n: NavDef; active: boolean; onClick: () => void }) {
  const o = overview.value;
  let extra: ComponentChildren = null;
  if (p.n.id === 'devices' && o) extra = <span class="nav-badge">{num(o.devices.online)}</span>;
  if (p.n.id === 'internet' && o) extra = <span class={'nav-dot' + (o.wan.up ? '' : ' bad')} />;
  if (p.n.id === 'proxy') {
    const d = proxyDot(o);
    if (d !== null) extra = <span class={'nav-dot' + (d ? ' ' + d : '')} />;
  }
  return (
    <button class={'nav-item' + (p.active ? ' active' : '')} onClick={p.onClick} aria-current={p.active ? 'page' : undefined}
      title={p.n.label()} aria-label={p.n.label()}>
      <span class="nav-icon">{p.n.icon}</span>
      <span class="grow">
        <div class="nav-label">{p.n.label()}</div>
        <div class="nav-sub">{p.n.sub()}</div>
      </span>
      {extra}
    </button>
  );
}

/** PageBoundary keeps a crash in one page from freezing the whole panel. */
function PageBoundary(p: { children: ComponentChildren }) {
  const [error, reset] = useErrorBoundary((e) => console.error(e));
  if (error) {
    return (
      <div class="card card-pad stack" style="align-items:flex-start">
        <div class="card-title">{L('این صفحه با خطا روبه‌رو شد', 'This page hit an error')}</div>
        <div class="small muted mono">{String((error as Error).message || error)}</div>
        <div class="row">
          <Btn kind="primary" onClick={reset}>{L('تلاش دوباره', 'Try again')}</Btn>
          <Btn onClick={() => location.reload()}>{L('بارگذاری مجدد', 'Reload')}</Btn>
        </div>
      </div>
    );
  }
  return <>{p.children}</>;
}

function Events(p: { onClose: () => void }) {
  const evs = [...(overview.value?.proxy.events || [])].reverse();
  return (
    <Modal title={L('رویدادها', 'Events')} onClose={p.onClose}>
      {evs.length === 0 && <div class="muted">{L('هنوز رویدادی ثبت نشده است.', 'No events yet.')}</div>}
      {evs.map((e) => (
        <div class="row" style="align-items:flex-start">
          <span class={'dot' + (e.kind === 'error' ? ' bad' : e.kind === 'warn' ? ' warn' : '')} style="margin-top:9px" />
          <div class="grow">
            <div class="ltr small" style="text-align:start">{e.msg}</div>
            <div class="tiny faint">{ago(e.at)}</div>
          </div>
        </div>
      ))}
    </Modal>
  );
}

export function App() {
  const [menu, setMenu] = useState(false);
  const [showEvents, setShowEvents] = useState(false);
  const r = route.value;
  lang.value; // re-render on language change

  useEffect(() => {
    const onHash = () => {
      route.value = readRoute();
      setMenu(false);
      window.scrollTo(0, 0);
    };
    window.addEventListener('hashchange', onHash);
    get<{ noPassword?: boolean }>('/ping').then((d) => (noPassword.value = !!d.noPassword)).catch(() => {});
    // Probe the session.
    get<Overview>('/overview')
      .then((o) => {
        overview.value = stamp(o);
        authed.value = true;
      })
      .catch(() => {});
    return () => window.removeEventListener('hashchange', onHash);
  }, []);

  useEffect(() => {
    if (!authed.value) return;
    const t = setInterval(() => {
      if (document.hidden) return;
      get<Overview>('/overview').then((o) => (overview.value = stamp(o))).catch(() => {});
    }, 3000);
    return () => clearInterval(t);
  }, [authed.value]);

  if (authed.value === null) return <div class="login-wrap"><span class="spinner" /></div>;
  if (!authed.value) return (<><Login /><Toasts /></>);

  const page = {
    home: <Home />,
    internet: <Internet />,
    proxy: <Proxy />,
    devices: <Devices />,
    wifi: <WifiPage />,
    gaming: <Gaming />,
    dns: <Dns />,
    system: <System />,
  }[r];
  const cur = navs.find((n) => n.id === r)!;
  const warnEvents = (overview.value?.proxy.events || []).filter((e) => e.kind !== 'info' && Date.now() / 1000 - e.at < 3600).length;

  return (
    <div class={'app' + (rail.value ? ' rail' : '')}>
      {menu && <div class="scrim" onClick={() => setMenu(false)} />}
      <aside class={'sidebar' + (menu ? ' open' : '')}>
        <button class="rail-toggle" onClick={toggleRail}
          aria-label={rail.value ? L('باز کردن منو', 'Expand menu') : L('جمع کردن منو', 'Collapse menu')}
          title={rail.value ? L('باز کردن منو', 'Expand menu') : L('جمع کردن منو', 'Collapse menu')}>
          {(lang.value === 'fa') !== rail.value ? <ChevronsRight size={16} /> : <ChevronsLeft size={16} />}
        </button>
        <div class="brand">
          <div class="brand-logo"><Tornado size={22} /></div>
          <div>
            <div class="brand-name">ToofanWrt</div>
            <div class="brand-sub">{overview.value?.system.model || L('پنل مدیریت روتر', 'Router panel')}</div>
          </div>
        </div>
        <nav class="nav">
          {navs.map((n) => (
            <NavItem key={n.id} n={n} active={n.id === r} onClick={() => go(n.id)} />
          ))}
        </nav>
        <div class="side-foot">
          <Btn class="grow" onClick={() => {
            const l = lang.value === 'fa' ? 'en' : 'fa';
            setLang(l);
            post('/settings', { lang: l }).catch(() => {});
          }}>
            <Languages size={17} /> {lang.value === 'fa' ? 'English' : 'فارسی'}
          </Btn>
          <ThemePicker />
          <Btn icon aria-label={L('خروج', 'Log out')} onClick={async () => {
            await post('/logout').catch(() => {});
            authed.value = false;
          }}>
            <LogOut size={18} />
          </Btn>
        </div>
      </aside>
      <div class="main">
        <header class="topbar">
          <Btn kind="ghost" icon class="mobile-only" onClick={() => setMenu(true)} aria-label={L('منو', 'Menu')}>
            <Menu size={20} />
          </Btn>
          <div class="topbar-title grow">{cur.label()}</div>
          <Clock />
          <Btn kind="ghost" icon aria-label={L('رویدادها', 'Events')} onClick={() => setShowEvents(true)} style="position:relative">
            <Bell size={19} />
            {warnEvents > 0 && <span class="dot bad" style="position:absolute;top:9px;inset-inline-end:9px" />}
          </Btn>
          <Btn kind="ghost" icon aria-label={L('بازخوانی', 'Refresh')} onClick={() => {
            get<Overview>('/overview').then((o) => (overview.value = stamp(o))).catch(() => {});
            refreshers.forEach((f) => f());
          }}>
            <RefreshCw size={18} />
          </Btn>
        </header>
        <main class="content">
          {noPassword.value && (
            <div class="callout danger" style="margin-bottom:18px">
              <div><b>{L('روتر رمز ندارد!', 'The router has no password!')}</b>
                {L('هر کسی در شبکه می‌تواند وارد پنل شود. از صفحهٔ سیستم یک رمز بگذارید.', 'Anyone on your network can open this panel. Set a password on the System page.')}{' '}
                <a href="#/system">{L('رفتن به سیستم', 'Go to System')}</a></div>
            </div>
          )}
          <PageBoundary key={r}>{page}</PageBoundary>
        </main>
      </div>
      <nav class="mobile-bar">
        {(['home', 'proxy', 'devices', 'wifi'] as Route[]).map((id) => {
          const n = navs.find((x) => x.id === id)!;
          return (
            <button class={r === id ? 'active' : ''} onClick={() => go(id)}>
              {n.icon}
              {n.label()}
            </button>
          );
        })}
        <button onClick={() => setMenu(true)}>
          <Menu size={19} />
          {L('بیشتر', 'More')}
        </button>
      </nav>
      {showEvents && <Events onClose={() => setShowEvents(false)} />}
      <Toasts />
      <ConfirmHost />
    </div>
  );
}
