// tools/foto.mjs: saca una foto de una página o un SVG después de que terminan sus animaciones (Chrome o Edge sin
// ventana, por CDP). Uso: node tools/foto.mjs <archivo> <salida.png> <ancho> <alto> [espera ms]
import fs from 'node:fs';
import path from 'node:path';
import { spawn } from 'node:child_process';
import { pathToFileURL, fileURLToPath } from 'node:url';

const [archivo, salida, ancho, alto, ms = '3200'] = process.argv.slice(2);
const tmp = path.join(path.dirname(fileURLToPath(import.meta.url)), '.demo');
const navegador = ['C:/Program Files/Google/Chrome/Application/chrome.exe', 'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe']
  .find((p) => fs.existsSync(p));
const puerto = 9348;
const proc = spawn(navegador, ['--headless=new', `--remote-debugging-port=${puerto}`, `--user-data-dir=${path.join(tmp, 'perfil-foto')}`,
  '--hide-scrollbars', '--no-first-run', 'about:blank'], { stdio: 'ignore' });
const espera = (t) => new Promise((r) => setTimeout(r, t));
let lista;
for (let i = 0; i < 50 && !lista; i++) {
  await espera(200);
  lista = await fetch(`http://127.0.0.1:${puerto}/json`).then((r) => r.json()).catch(() => null);
}
const ws = new WebSocket(lista.find((t) => t.type === 'page').webSocketDebuggerUrl);
await new Promise((r) => { ws.onopen = r; });
let n = 0;
const pend = new Map();
ws.onmessage = (e) => {
  const m = JSON.parse(e.data);
  if (m.id && pend.has(m.id)) { pend.get(m.id)(m.result); pend.delete(m.id); }
};
const cdp = (method, params = {}) => new Promise((r) => { const id = ++n; pend.set(id, r); ws.send(JSON.stringify({ id, method, params })); });
await cdp('Emulation.setDeviceMetricsOverride', { width: +ancho, height: +alto, deviceScaleFactor: 1, mobile: false });
await cdp('Emulation.setDefaultBackgroundColorOverride', { color: { r: 0, g: 0, b: 0, a: 0 } });
await cdp('Page.navigate', { url: pathToFileURL(path.resolve(archivo)).href });
await espera(+ms);
const img = await cdp('Page.captureScreenshot', { format: 'png' });
fs.writeFileSync(salida, Buffer.from(img.data, 'base64'));
ws.close();
proc.kill();
console.log(salida);
