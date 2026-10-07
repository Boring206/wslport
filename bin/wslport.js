#!/usr/bin/env node
'use strict';

// wslport 的啟動器：套件裡放的是編好的 Windows 執行檔，這支只負責挑對架構並把參數轉交過去。
// 在 WSL 裡安裝時，同一支 exe 會透過 WSL 的 Windows 互通（interop）執行。

const { spawnSync } = require('node:child_process');
const fs = require('node:fs');
const path = require('node:path');

function fail(message) {
  process.stderr.write(`wslport：${message}\n`);
  process.exit(2);
}

function isWSL() {
  if (process.env.WSL_DISTRO_NAME) return true;
  try {
    return /microsoft/i.test(fs.readFileSync('/proc/version', 'utf8'));
  } catch {
    return false;
  }
}

const onLinux = process.platform === 'linux';
if (process.platform !== 'win32' && !(onLinux && isWSL())) {
  fail('只支援 Windows 與 WSL。');
}

const arch = process.arch === 'arm64' ? 'arm64' : 'x64';
const exe = path.join(__dirname, `wslport-${arch}.exe`);
if (!fs.existsSync(exe)) {
  fail(`找不到執行檔 ${exe}\n從原始碼安裝的話，請先執行 npm run build。`);
}

if (onLinux) {
  // 套件若是在 Windows 上打包的，exe 不會帶執行權限。
  try {
    fs.chmodSync(exe, 0o755);
  } catch {
    // 用 sudo 安裝時改不了權限；下面執行失敗時會提示怎麼處理。
  }
}

const result = spawnSync(exe, process.argv.slice(2), { stdio: 'inherit' });
if (result.error) {
  const code = result.error.code;
  if (onLinux && code === 'EACCES') {
    fail(`沒有執行權限，請執行：sudo chmod +x "${exe}"`);
  }
  if (onLinux && (code === 'ENOEXEC' || code === 'ENOENT')) {
    fail(
      '這個 WSL 無法執行 Windows 程式（interop 沒有啟用）。\n' +
        '請確認 /etc/wsl.conf 沒有設定 [interop] enabled=false，\n' +
        '修改後在 Windows 執行 wsl --shutdown 再重新開啟。'
    );
  }
  fail(result.error.message);
}
process.exit(result.status === null ? 2 : result.status);
