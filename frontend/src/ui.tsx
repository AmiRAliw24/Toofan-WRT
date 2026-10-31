import { signal } from '@preact/signals';
import type { ComponentChildren, JSX } from 'preact';
import { useEffect, useMemo, useState } from 'preact/hooks';
import { Eye, EyeOff, X } from 'lucide-preact';
import { L, lang, num, dateLocale } from './i18n';

// ---- toasts ----
interface Toast { id: number; text: string; error?: boolean }
const toasts = signal<Toast[]>([]);
let toastId = 0;
export function toast(text: string, error = false) {
  const id = ++toastId;
  toasts.value = [...toasts.value, { id, text, error }];
  setTimeout(() => (toasts.value = toasts.value.filter((t) => t.id !== id)), error ? 5000 : 2800);
}
export function Toasts() {
  return (
    <div class="toasts" role="status" aria-live="polite">
      {toasts.value.map((t) => (
        <div key={t.id} class={'toast' + (t.error ? ' error' : '')}>{t.text}</div>
      ))}
    </div>
  );
}

/** run wraps an async action with success/error toasts. */
export async function run<T>(fn: () => Promise<T>, ok?: string): Promise<T | undefined> {
  try {
    const r = await fn();
    if (ok) toast(ok);
    return r;
  } catch (e: any) {
    toast(e?.message || L('خطا', 'Error'), true);
    return undefined;
  }
}

// ---- layout primitives ----
export function Card(p: { children: ComponentChildren; class?: string; pad?: boolean }) {
  return <section class={'card' + (p.pad ? ' card-pad' : '') + (p.class ? ' ' + p.class : '')}>{p.children}</section>;
}

export function CardHead(p: {
  icon: ComponentChildren;
  title: ComponentChildren;
  sub?: ComponentChildren;
  tone?: 'ok' | 'warn' | 'danger' | 'violet' | 'info';
  action?: ComponentChildren;
}) {
  return (
    <div class="card-head">
      <div class={'head-icon' + (p.tone ? ' ' + p.tone : '')}>{p.icon}</div>
      <div class="grow">
        <div class="card-title">{p.title}</div>
        {p.sub && <div class="card-sub">{p.sub}</div>}
      </div>
      {p.action}
    </div>
  );
}

export function PageHead(p: { title: string; sub?: string; action?: ComponentChildren }) {
  return (
    <div class="page-head row between wrap">
      <div>
        <h1 class="page-title">{p.title}</h1>
        {p.sub && <p class="page-sub">{p.sub}</p>}
      </div>
      {p.action}
    </div>
  );
}

export function Toggle(p: { on: boolean; onChange: (v: boolean) => void; disabled?: boolean; label?: string }) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={p.on}
      aria-label={p.label}
      class={'toggle' + (p.on ? ' on' : '')}
      disabled={p.disabled}
      onClick={() => p.onChange(!p.on)}
    >
      <span class="track" />
      <span class="knob" />
    </button>
  );
}

export function Setting(p: { title: ComponentChildren; desc?: ComponentChildren; children: ComponentChildren }) {
  return (
    <div class="setting">
      <div class="grow">
        <div class="setting-title">{p.title}</div>
        {p.desc && <div class="setting-desc">{p.desc}</div>}
      </div>
      {p.children}
    </div>
  );
}

export function Btn(
  p: JSX.ButtonHTMLAttributes<HTMLButtonElement> & {
    kind?: 'primary' | 'danger' | 'danger-solid' | 'ghost';
    sm?: boolean;
    icon?: boolean;
    busy?: boolean;
  },
) {
  const { kind, sm, icon, busy, children, class: cls, disabled, ...rest } = p;
  const c = ['btn', kind, sm && 'sm', icon && 'icon', cls].filter(Boolean).join(' ');
  return (
    <button type="button" class={c} disabled={disabled || busy} {...rest}>
      {busy ? <span class="spinner" /> : children}
    </button>
  );
}

export function Tabs<T extends string>(p: {
  value: T;
  onChange: (v: T) => void;
  items: { id: T; label: string; icon?: ComponentChildren }[];
}) {
  return (
    <div class="tabs" role="tablist">
      {p.items.map((it) => (
        <button
          key={it.id}
          role="tab"
          aria-selected={p.value === it.id}
          class={'tab' + (p.value === it.id ? ' active' : '')}
          onClick={() => p.onChange(it.id)}
        >
          {it.icon}
          {it.label}
        </button>
      ))}
    </div>
  );
}

export function Seg<T extends string>(p: { value: T; onChange: (v: T) => void; items: { id: T; label: string }[] }) {
  return (
    <div class="seg">
      {p.items.map((it) => (
        <button key={it.id} class={p.value === it.id ? 'active' : ''} onClick={() => p.onChange(it.id)}>
          {it.label}
        </button>
      ))}
    </div>
  );
}

export function Badge(p: { tone?: 'ok' | 'warn' | 'danger' | 'info' | 'violet'; children: ComponentChildren; title?: string }) {
  return (
    <span class={'badge' + (p.tone ? ' ' + p.tone : '')} title={p.title}>
      {p.children}
    </span>
  );
}

export function Modal(p: { title: ComponentChildren; onClose: () => void; children: ComponentChildren; foot?: ComponentChildren; wide?: boolean }) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && p.onClose();
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);
  return (
    <div class="overlay" onClick={(e) => e.target === e.currentTarget && p.onClose()}>
      <div class="modal" role="dialog" aria-modal="true" style={p.wide ? 'max-width:720px' : undefined}>
        <div class="modal-head">
          <div class="card-title grow">{p.title}</div>
          <Btn kind="ghost" sm icon onClick={p.onClose} aria-label={L('بستن', 'Close')}>
            <X size={18} />
          </Btn>
        </div>
        <div class="modal-body">{p.children}</div>
        {p.foot && <div class="modal-foot">{p.foot}</div>}
      </div>
    </div>
  );
}

// ---- confirm dialog ----
const confirmState = signal<{ text: string; danger?: boolean; resolve: (v: boolean) => void } | null>(null);
export function confirmDialog(text: string, danger = false): Promise<boolean> {
  return new Promise((resolve) => (confirmState.value = { text, danger, resolve }));
}
export function ConfirmHost() {
  const s = confirmState.value;
  if (!s) return null;
  const close = (v: boolean) => {
    s.resolve(v);
    confirmState.value = null;
  };
  return (
    <Modal
      title={L('مطمئن هستید؟', 'Are you sure?')}
      onClose={() => close(false)}
      foot={
        <>
          <Btn onClick={() => close(false)}>{L('انصراف', 'Cancel')}</Btn>
          <Btn kind={s.danger ? 'danger-solid' : 'primary'} onClick={() => close(true)}>
            {L('تأیید', 'Confirm')}
          </Btn>
        </>
      }
    >
      <p style="margin:0">{s.text}</p>
    </Modal>
  );
}

export function Field(p: { label: string; hint?: ComponentChildren; children: ComponentChildren }) {
  return (
    <div class="field">
      <label>{p.label}</label>
      {p.children}
      {p.hint && <div class="hint">{p.hint}</div>}
    </div>
  );
}

export function Password(p: { value: string; onInput: (v: string) => void; placeholder?: string; autocomplete?: string }) {
  const [show, setShow] = useState(false);
  return (
    <div class="input-wrap">
      <input
        class="input ltr"
        type={show ? 'text' : 'password'}
        value={p.value}
        placeholder={p.placeholder}
        autoComplete={p.autocomplete || 'new-password'}
        onInput={(e) => p.onInput((e.target as HTMLInputElement).value)}
      />
      <Btn kind="ghost" icon onClick={() => setShow(!show)} aria-label={L('نمایش رمز', 'Show password')}>
        {show ? <EyeOff size={18} /> : <Eye size={18} />}
      </Btn>
    </div>
  );
}

export function Spinner() {
  return <span class="spinner" />;
}

export function Empty(p: { icon: ComponentChildren; text: string; children?: ComponentChildren }) {
  return (
    <div class="empty">
      {p.icon}
      <div>{p.text}</div>
      {p.children}
    </div>
  );
}

export function Skeleton(p: { h?: number }) {
  return <div class="skeleton" style={{ height: (p.h || 120) + 'px' }} />;
}

export function Meter(p: { value: number; color?: string }) {
  const v = Math.max(0, Math.min(100, p.value));
  return (
    <div class="meter">
      <div style={{ width: v + '%', background: p.color }} />
    </div>
  );
}

export function latClass(ms: number) {
  if (ms < 0) return 'bad';
  if (ms < 250) return 'good';
  if (ms < 600) return 'mid';
  return 'bad';
}

/** niceTicks returns 3 round values spanning [min, max]. */
function niceTicks(min: number, max: number): number[] {
  if (max <= min) max = min + 1;
  const raw = (max - min) / 2;
  const mag = Math.pow(10, Math.floor(Math.log10(raw)));
  const step = [1, 2, 2.5, 5, 10].map((m) => m * mag).find((s) => s >= raw) || raw;
  const lo = Math.floor(min / step) * step;
  return [lo, lo + step, lo + 2 * step];
}

/**
 * LineChart: a latency series over time. 2px line, faint area, recessive
 * grid with three round ticks, emphasised last point, and a hover / touch
 * crosshair with a tooltip. Negative values are failures, drawn as gaps
 * with a red tick on the baseline.
 */
export function LineChart(p: { points: { t: number; v: number }[]; color?: string; height?: number; loading?: boolean; unit?: string }) {
  const h = p.height || 130;
  const w = 640;
  const padL = 34;
  const pts = p.points;
  const [hover, setHover] = useState<number | null>(null);
  const geo = useMemo(() => {
    const ok = pts.filter((x) => x.v >= 0).map((x) => x.v);
    const ticks = niceTicks(ok.length ? Math.min(...ok) : 0, ok.length ? Math.max(...ok) : 1);
    const mn = ticks[0];
    const mx = Math.max(ticks[2], ...ok);
    const span = Math.max(1, mx - mn);
    const step = pts.length > 1 ? (w - padL) / (pts.length - 1) : 0;
    const X = (i: number) => padL + i * step;
    const Y = (v: number) => 6 + (1 - (v - mn) / span) * (h - 14);
    let d = '';
    let a = '';
    let open = false;
    let last = -1;
    pts.forEach((pt, i) => {
      if (pt.v < 0) {
        if (open) a += `L${X(last)},${h - 8}Z`;
        open = false;
        return;
      }
      if (!open) {
        d += `M${X(i)},${Y(pt.v)}`;
        a += `M${X(i)},${h - 8}L${X(i)},${Y(pt.v)}`;
        open = true;
      } else {
        d += `L${X(i)},${Y(pt.v)}`;
        a += `L${X(i)},${Y(pt.v)}`;
      }
      last = i;
    });
    if (open) a += `L${X(last)},${h - 8}Z`;
    const fails = pts.map((pt, i) => (pt.v < 0 ? X(i) : -1)).filter((x) => x >= 0);
    return { d, a, ticks, X, Y, fails, last, step };
  }, [pts, h]);
  const color = p.color || 'var(--chart-1)';
  if (pts.length === 0) {
    if (p.loading) return <div class="skeleton" style={{ height: h + 'px' }} />;
    return (
      <div class="empty small" style={{ height: h + 'px', padding: '0', display: 'grid', placeItems: 'center' }}>
        {L('در حال جمع‌آوری داده؛ هر دقیقه یک نقطه اضافه می‌شود', 'Collecting data; a point is added every minute')}
      </div>
    );
  }
  const onMove = (e: PointerEvent) => {
    const el = e.currentTarget as SVGSVGElement;
    const r = el.getBoundingClientRect();
    const x = ((e.clientX - r.left) / r.width) * w;
    const i = Math.round((x - padL) / Math.max(geo.step, 1e-6));
    setHover(Math.max(0, Math.min(pts.length - 1, i)));
  };
  const hp = hover !== null ? pts[hover] : null;
  const fmtTime = (t: number) => new Date(t * 1000).toLocaleString(dateLocale(), { hour: '2-digit', minute: '2-digit', day: pts[pts.length - 1].t - pts[0].t > 86400 * 1.5 ? 'numeric' : undefined, month: pts[pts.length - 1].t - pts[0].t > 86400 * 1.5 ? 'short' : undefined });
  const lastPt = geo.last >= 0 ? pts[geo.last] : null;
  return (
    <div class="chart-wrap">
      <svg class="chart" viewBox={`0 0 ${w} ${h}`} preserveAspectRatio="none" style={{ height: h + 'px' }} role="img"
        aria-label={lastPt ? `${num(lastPt.v)} ${p.unit || 'ms'}` : ''}
        onPointerMove={onMove} onPointerDown={onMove} onPointerLeave={() => setHover(null)}>
        {geo.ticks.map((t) => (
          <g>
            <line x1={padL} x2={w} y1={geo.Y(t)} y2={geo.Y(t)} stroke="var(--chart-grid)" stroke-width="1" vector-effect="non-scaling-stroke" />
          </g>
        ))}
        <path d={geo.a} fill={color} opacity="0.12" />
        <path d={geo.d} fill="none" stroke={color} stroke-width="2" vector-effect="non-scaling-stroke" stroke-linejoin="round" stroke-linecap="round" />
        {geo.fails.map((x) => (
          <line x1={x} x2={x} y1={h - 8} y2={h} stroke="var(--danger-strong)" stroke-width="2" vector-effect="non-scaling-stroke" />
        ))}
        {hover !== null && (
          <line x1={geo.X(hover)} x2={geo.X(hover)} y1={0} y2={h - 8} stroke="var(--text-3)" stroke-width="1" stroke-dasharray="3 3" vector-effect="non-scaling-stroke" />
        )}
      </svg>
      {/* y tick labels and endpoint dot are HTML so they don't stretch with the SVG */}
      {geo.ticks.map((t) => (
        <span class="tiny faint" style={{ position: 'absolute', left: '0', top: (geo.Y(t) / h) * 100 + '%', transform: 'translateY(-50%)', direction: 'ltr', fontVariantNumeric: 'tabular-nums', lineHeight: 1 }}>{num(t)}</span>
      ))}
      {lastPt && (
        <span style={{ position: 'absolute', left: `calc(${(geo.X(geo.last) / w) * 100}% - 4px)`, top: `calc(${(geo.Y(lastPt.v) / h) * 100}% - 4px)`, width: '8px', height: '8px', borderRadius: '50%', background: color, boxShadow: '0 0 0 2px var(--bg-elev)' }} />
      )}
      {hp && (
        <div class="chart-tip" style={{ left: `${(geo.X(hover!) / w) * 100}%` }}>
          {hp.v >= 0 ? `${num(hp.v)} ${p.unit || 'ms'}` : L('بدون پاسخ', 'no response')} · {fmtTime(hp.t)}
        </div>
      )}
      <div class="chart-axis" style={{ paddingLeft: padL / w * 100 + '%' }}>
        <span>{fmtTime(pts[0].t)}</span>
        <span>{fmtTime(pts[Math.floor(pts.length / 2)].t)}</span>
        <span>{fmtTime(pts[pts.length - 1].t)}</span>
      </div>
    </div>
  );
}

/** Gauge is a half-circle meter for the speed test. */
export function Gauge(p: { value: number; max: number; label: string; unit: string }) {
  const r = 80;
  const c = Math.PI * r;
  const frac = Math.max(0, Math.min(1, p.value / p.max));
  return (
    <svg viewBox="0 0 200 120" width="220" height="132" role="img" aria-label={`${p.value} ${p.unit}`}>
      <path d="M20 100 A80 80 0 0 1 180 100" fill="none" stroke="var(--bg-sunken)" stroke-width="14" stroke-linecap="round" />
      <path
        d="M20 100 A80 80 0 0 1 180 100"
        fill="none"
        stroke="var(--primary)"
        stroke-width="14"
        stroke-linecap="round"
        stroke-dasharray={`${c * frac} ${c}`}
        style="transition: stroke-dasharray .4s"
      />
      <text x="100" y="86" text-anchor="middle" font-size="30" font-weight="800" fill="var(--text)">
        {num(p.value, p.value < 10 ? 1 : 0)}
      </text>
      <text x="100" y="108" text-anchor="middle" font-size="12" fill="var(--text-3)">
        {p.unit} · {p.label}
      </text>
    </svg>
  );
}
