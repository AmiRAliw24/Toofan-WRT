#!/usr/bin/env python3
"""Writes dist/manifest.json (read by the panel's updater) and
dist/release-notes.md (the newest CHANGELOG section plus install steps).

usage: release.py VERSION TARBALL OWNER/REPO
"""
import hashlib, json, sys

version, tarball, repo = sys.argv[1:4]
name = f"toofanwrt-{version}.tar.gz"
sha = hashlib.sha256(open(tarball, "rb").read()).hexdigest()

log = open("CHANGELOG.md", encoding="utf-8").read()
# The newest release is the first "## " section.
section = log.split("\n## ", 2)[1]
title, body = section.split("\n", 1)
summary = next((l.strip() for l in body.splitlines() if l.strip() and not l.startswith("#")), "")

manifest = {
    "version": version,
    "url": f"https://github.com/{repo}/releases/download/v{version}/{name}",
    "sha256": sha,
    "notes": summary,
}
with open("dist/manifest.json", "w", encoding="utf-8") as f:
    json.dump(manifest, f, ensure_ascii=False, indent=2)
    f.write("\n")

notes = f"""# {title.strip()}

{body.strip()}

---

### 📥 نصب

۱. فایل `{name}` را از پایین همین صفحه دانلود کنید.

۲. به روتر بفرستید و نصب کنید:
```powershell
scp -O "$env:USERPROFILE\\Downloads\\{name}" root@192.168.1.1:/tmp/
```
```sh
ssh root@192.168.1.1
cd /tmp && rm -rf /tmp/toofanwrt && tar xzf {name} && sh toofanwrt/install.sh
```
۳. `http://192.168.1.1` را باز کنید. 🔑 رمز ورود همان رمز root روتر است.

> 📌 به‌روزرسانی‌ها از **سیستم ← به‌روزرسانی پنل ← بررسی** مستقیم از همین صفحه نصب می‌شوند.

🔒 SHA-256: `{sha}`
"""
with open("dist/release-notes.md", "w", encoding="utf-8") as f:
    f.write(notes)
print(f"v{version}: {sha}")
