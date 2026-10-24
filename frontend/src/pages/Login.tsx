import { useEffect, useState } from 'preact/hooks';
import { ShieldCheck } from 'lucide-preact';
import { authed, get, post } from '../api';
import { L } from '../i18n';
import { Btn, Card, Field, Password, toast } from '../ui';

export function Login() {
  const [pw, setPw] = useState('');
  const [busy, setBusy] = useState(false);
  const [noPw, setNoPw] = useState(false);
  useEffect(() => { get<{ noPassword?: boolean }>('/ping').then((d) => setNoPw(!!d.noPassword)).catch(() => {}); }, []);
  const submit = async (e: Event) => {
    e.preventDefault();
    setBusy(true);
    try {
      await post('/login', { password: pw });
      authed.value = true;
      location.reload();
    } catch (err: any) {
      toast(err.status === 401 ? L('رمز اشتباه است', 'Wrong password') : err.message, true);
    } finally {
      setBusy(false);
    }
  };
  return (
    <div class="login-wrap">
      <Card class="login">
        <form onSubmit={submit} class="stack">
          <div class="row" style="gap:14px">
            <div class="brand-logo"><ShieldCheck size={22} /></div>
            <div>
              <div class="brand-name">{L('سپر', 'Separ')}</div>
              <div class="brand-sub">{L('پنل مدیریت روتر', 'Router panel')}</div>
            </div>
          </div>
          <Field label={L('رمز روتر', 'Router password')} hint={L('همان رمز root روتر (SSH)', 'Same as the router root (SSH) password')}>
            <Password value={pw} onInput={setPw} autocomplete="current-password" />
          </Field>
          {noPw && (
            <div class="callout warn small"><div>
              <b>{L('روتر هنوز رمز ندارد', 'The router has no password yet')}</b>
              {L('کادر رمز را خالی بگذارید و وارد شوید؛ بعد از صفحهٔ سیستم یک رمز بگذارید.', 'Leave the password empty to log in, then set one on the System page.')}
            </div></div>
          )}
          <button type="submit" class="btn primary" disabled={busy || (!pw && !noPw)}>
            {busy ? <span class="spinner" /> : L('ورود', 'Log in')}
          </button>
        </form>
      </Card>
    </div>
  );
}
