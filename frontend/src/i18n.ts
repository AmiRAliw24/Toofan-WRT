import { signal } from '@preact/signals';

export type Lang = 'fa' | 'en';

function initialLang(): Lang {
  try {
    const v = localStorage.getItem('lang');
    if (v === 'en' || v === 'fa') return v;
  } catch {}
  return 'fa';
}

export const lang = signal<Lang>(initialLang());

export function setLang(l: Lang) {
  lang.value = l;
  try { localStorage.setItem('lang', l); } catch {}
  applyDir();
}

export function applyDir() {
  document.documentElement.lang = lang.value;
  document.documentElement.dir = lang.value === 'fa' ? 'rtl' : 'ltr';
  document.title = lang.value === 'fa' ? 'ToofanWrt · پنل روتر' : 'ToofanWrt · Router panel';
}

/** L picks the string for the current language. */
export function L(fa: string, en: string): string {
  return lang.value === 'fa' ? fa : en;
}

export function num(n: number, digits = 0): string {
  if (!isFinite(n)) return '—';
  return n.toLocaleString(lang.value === 'fa' ? 'fa-IR' : 'en-US', {
    maximumFractionDigits: digits,
    minimumFractionDigits: digits,
  });
}

export function pct(n: number, d = 0): string {
  return num(n, d) + (lang.value === 'fa' ? '٪' : '%');
}

/** Persian digits for things like IP-less labels; IPs stay Latin. */
export function digits(s: string | number): string {
  const str = String(s);
  if (lang.value !== 'fa') return str;
  return str.replace(/\d/g, (d) => '۰۱۲۳۴۵۶۷۸۹'[+d]);
}

export function rate(bytesPerSec: number): { v: string; u: string } {
  const b = Math.max(0, bytesPerSec || 0);
  if (b < 1000) return { v: num(b), u: 'B/s' };
  if (b < 1e6) return { v: num(b / 1000, 1), u: 'KB/s' };
  return { v: num(b / 1e6, 1), u: 'MB/s' };
}

export function bytes(n: number): string {
  const b = Math.max(0, n || 0);
  if (b < 1e3) return `${num(b)} B`;
  if (b < 1e6) return `${num(b / 1e3, 1)} KB`;
  if (b < 1e9) return `${num(b / 1e6, 1)} MB`;
  return `${num(b / 1e9, 2)} GB`;
}

export function duration(sec: number): string {
  const d = Math.floor(sec / 86400);
  const h = Math.floor((sec % 86400) / 3600);
  const m = Math.floor((sec % 3600) / 60);
  if (d > 0) return L(`${num(d)} روز ${num(h)} ساعت`, `${d}d ${h}h`);
  if (h > 0) return L(`${num(h)} ساعت ${num(m)} دقیقه`, `${h}h ${m}m`);
  return L(`${num(m)} دقیقه`, `${m}m`);
}

export function ago(unix: number): string {
  if (!unix) return '—';
  const s = Math.max(0, Date.now() / 1000 - unix);
  if (s < 60) return L('همین الان', 'just now');
  if (s < 3600) return L(`${num(Math.floor(s / 60))} دقیقه پیش`, `${Math.floor(s / 60)}m ago`);
  if (s < 86400) return L(`${num(Math.floor(s / 3600))} ساعت پیش`, `${Math.floor(s / 3600)}h ago`);
  return L(`${num(Math.floor(s / 86400))} روز پیش`, `${Math.floor(s / 86400)}d ago`);
}

export function clock(unix: number): string {
  return new Date(unix * 1000).toLocaleTimeString(lang.value === 'fa' ? 'fa-IR' : 'en-GB', {
    hour: '2-digit',
    minute: '2-digit',
  });
}

export const dayNames = () =>
  lang.value === 'fa'
    ? ['یک', 'دو', 'سه', 'چهار', 'پنج', 'جمعه', 'شنبه']
    : ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];

/** Week order for the picker: Saturday first in Persian, Monday in English. */
export const weekOrder = () => (lang.value === 'fa' ? [6, 0, 1, 2, 3, 4, 5] : [1, 2, 3, 4, 5, 6, 0]);
