import { useEffect, useState } from 'preact/hooks';
import { Lightbulb, Save } from 'lucide-preact';
import { get, post } from '../api';
import { L, num } from '../i18n';
import { Btn, Card, CardHead, Field, Setting, Toggle, run } from '../ui';

interface LEDCfg { mode: string; color: string; brightness: number; nightOff: boolean; nightFrom: string; nightTo: string }
interface LEDResp { config: LEDCfg; supported: boolean; current: string }

const palette = ['#00b894', '#0984e3', '#6c5ce7', '#e84393', '#d63031', '#fdcb6e', '#ffffff', '#00cec9'];

export function LedCard() {
  const [d, setD] = useState<LEDResp | null>(null);
  const [f, setF] = useState<LEDCfg | null>(null);
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    let alive = true;
    const load = async () => {
      try {
        const r = await get<LEDResp>('/led');
        if (!alive) return;
        setD(r);
        setF((cur) => cur || r.config);
      } catch {}
    };
    load();
    const t = setInterval(load, 1000);
    return () => { alive = false; clearInterval(t); };
  }, []);
  if (!d || !f) return null;
  if (!d.supported) return null;
  const up = (p: Partial<LEDCfg>) => setF({ ...f, ...p });
  const modes = [
    { id: 'auto', label: L('هوشمند خودکار', 'Smart auto') },
    { id: 'color', label: L('رنگ دلخواه', 'Custom colour') },
    { id: 'ping', label: L('نمایش پینگ', 'Show ping') },
    { id: 'off', label: L('خاموش', 'Off') },
    { id: 'default', label: L('پیش‌فرض OpenWrt', 'OpenWrt default') },
  ];
  const legend = [
    ['#8c33ff', L('بنفش: تفکیک هوشمند ایران', 'Purple: smart Iran split')],
    ['#00ff40', L('سبز: اتصال کامل', 'Green: fully connected')],
    ['#00d9e6', L('فیروزه‌ای: اینترنت مستقیم', 'Cyan: direct internet')],
    ['#ff9900', L('زرد تپنده: در حال اتصال یا اتصال آزاد قطع', 'Pulsing yellow: reconnecting / tunnel down')],
    ['#ff0000', L('قرمز: قطع یا قطع اضطراری', 'Red: offline or kill switch')],
  ];
  return (
    <Card>
      <CardHead icon={<Lightbulb size={20} />} tone="warn" title={L('چراغ روتر', 'Router light')}
        sub={L('حلقهٔ نوری وضعیت شبکه را بدون باز کردن پنل نشان می‌دهد', 'The light ring shows network status without opening the panel')}
        action={<span title={L('رنگ فعلی', 'Current colour')} style={{ width: '34px', height: '34px', borderRadius: '50%', border: '3px solid var(--border)', background: d.current === '#000000' ? 'var(--bg-sunken)' : d.current, boxShadow: d.current === '#000000' ? 'none' : `0 0 18px ${d.current}` }} />} />
      <div class="card-body stack">
        <div class="row wrap" style="gap:6px">
          {modes.map((m) => <button class={'btn sm' + (f.mode === m.id ? ' primary' : '')} onClick={() => up({ mode: m.id })}>{m.label}</button>)}
        </div>
        {f.mode === 'auto' && (
          <div class="stack" style="gap:6px">
            {legend.map(([c, t]) => <div class="row small"><span class="dot" style={{ background: c, width: '12px', height: '12px' }} />{t}</div>)}
            <div class="tiny faint">{L('هنگام تست سرعت، چراغ تند می‌تپد و بعد کیفیت را با رنگ اعلام می‌کند.', 'During a speed test it pulses fast, then shows the result as a colour.')}</div>
          </div>
        )}
        {f.mode === 'ping' && <div class="small muted">{L('سبز زیر ۵۰ms، فیروزه‌ای تا ۱۲۰ms، زرد تا ۲۵۰ms، قرمز بدتر از آن', 'Green under 50 ms, cyan to 120 ms, yellow to 250 ms, red beyond')}</div>}
        {f.mode === 'color' && (
          <div class="row wrap" style="gap:8px">
            {palette.map((c) => (
              <button aria-label={c} onClick={() => up({ color: c })} style={{ width: '34px', height: '34px', borderRadius: '50%', background: c, cursor: 'pointer', border: f.color === c ? '3px solid var(--text)' : '2px solid var(--border)' }} />
            ))}
            <input type="color" value={f.color} onInput={(e) => up({ color: (e.target as HTMLInputElement).value })} style="width:44px;height:36px;border:0;background:none;cursor:pointer" />
          </div>
        )}
        {f.mode !== 'off' && f.mode !== 'default' && (
          <Field label={L(`روشنایی: ${num(f.brightness)}٪`, `Brightness: ${f.brightness}%`)}>
            <input type="range" min={5} max={100} value={f.brightness} onInput={(e) => up({ brightness: +(e.target as HTMLInputElement).value })} />
          </Field>
        )}
        {f.mode !== 'default' && (
          <>
            <Setting title={L('خاموشی شبانه', 'Night mode')} desc={L('در ساعت خواب خاموش و صبح خودکار روشن می‌شود', 'Turns off at bedtime and back on in the morning')}>
              <Toggle on={f.nightOff} onChange={(v) => up({ nightOff: v })} />
            </Setting>
            {f.nightOff && (
              <div class="grid g2" style="gap:12px">
                <Field label={L('خاموش از', 'Off from')}><input class="input ltr" type="time" value={f.nightFrom} onInput={(e) => up({ nightFrom: (e.target as HTMLInputElement).value })} /></Field>
                <Field label={L('روشن در', 'On at')}><input class="input ltr" type="time" value={f.nightTo} onInput={(e) => up({ nightTo: (e.target as HTMLInputElement).value })} /></Field>
              </div>
            )}
          </>
        )}
      </div>
      <div class="card-foot">
        <Btn kind="primary" busy={busy} onClick={async () => {
          setBusy(true);
          await run(() => post('/led', f), L('ذخیره شد', 'Saved'));
          setBusy(false);
        }}><Save size={17} />{L('ذخیره', 'Save')}</Btn>
      </div>
    </Card>
  );
}
