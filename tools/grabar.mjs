// tools/grabar.mjs: capturas y GIF del panel para el README, con datos inventados (nada de una cuenta real).
// Arma la página del panel igual que el exe (ui\ con el CSS y el JS adentro), le da un estado de demo y la maneja
// con Chrome o Edge sin ventana, por CDP. Necesita ffmpeg en el PATH para el GIF y el video.
//
//   node tools/grabar.mjs            capturas (docs/*.png), GIF (docs/pestanas.gif) y video (docs/pestanas.webm)
//   node tools/grabar.mjs --html     sólo arma tools/.demo/demo.html para mirarlo en el navegador
import fs from 'node:fs';
import path from 'node:path';
import { spawn, execFileSync } from 'node:child_process';
import { fileURLToPath, pathToFileURL } from 'node:url';

const raiz = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const tmp = path.join(raiz, 'tools', '.demo');
const docs = path.join(raiz, 'docs');
fs.mkdirSync(tmp, { recursive: true });
fs.mkdirSync(docs, { recursive: true });

// ---- datos de demo -----------------------------------------------------------------------------

const H = 3600e3, D = 24 * H, ahora = Date.now();
const hoy0 = new Date(new Date().setHours(0, 0, 0, 0)).getTime();
const desde = (n) => hoy0 - (n - 1) * D;
const serie = (n, base, amp, fase) => Array.from({ length: n }, (_, i) => Math.max(0, Math.round(base + amp * Math.sin(i * 1.7 + fase) * Math.cos(i * 0.6))));
const m = (nombre, pct, renueva, extra = {}) => ({ nombre, pct, renueva, ...extra });
const d = (grupo, nombre, valor, nota = '', extra = {}) => ({ grupo, nombre, valor, nota, ...extra });
const cuenta = 'Ana Ruiz · ana@ejemplo.com';

const ias = [
  { id: 'claude', nombre: 'Claude', plan: 'Max 5x', cuenta, filas: [
    m('sesión · 5 h', 62, ahora + 2 * H + 10 * 60e3, { nota: 'el que más aprieta ahora' }),
    m('semana', 38, ahora + 4 * D + 5 * H),
    m('semana · Opus', 21, ahora + 4 * D + 5 * H),
    m('rutinas diarias', 9, ahora + 14 * H),
    d('créditos', 'reinicios guardados', '1 disponible', 'el primero vence el 31 oct 2026'),
    d('reparto de la semana', 'Claude Code', '86 %'),
    d('reparto de la semana', 'Chats', '11 %'),
    d('reparto de la semana', 'Cowork', '3 %'),
    d('cuenta', 'suscripción', 'activa · desde el 2 mar 2026'),
    d('cuenta', 'facturación', 'suscripción con tarjeta'),
    d('cuenta', 'nivel de límites', 'Max 5x'),
    d('cuenta', 'créditos extra', 'apagados'),
    d('Claude Code en esta PC', 'hoy', '184 M tokens', '412 respuestas · entrada 9,8 k · salida 1,1 M (razonamiento 640 k) · caché leída 176 M · caché escrita 6,9 M'),
    d('Claude Code en esta PC', '7 días', '1,2 G tokens', '2.904 respuestas · entrada 61 k · salida 7,4 M (razonamiento 4,1 M) · caché leída 1,1 G · caché escrita 38 M'),
    { grupo: 'Claude Code en esta PC', nombre: 'tokens por día', serie: [212e6, 96e6, 41e6, 158e6, 231e6, 309e6, 184e6], desde: desde(7) },
    d('por modelo · 7 días', 'Opus 5.5', '980 M tokens', '2.311 respuestas · salida 5,9 M'),
    d('por modelo · 7 días', 'Sonnet 5.5', '190 M tokens', '480 respuestas · salida 1,2 M'),
    d('por modelo · 7 días', 'Haiku 5.5', '41 M tokens', '113 respuestas · salida 310 k'),
  ] },
  { id: 'codex', nombre: 'Codex', plan: 'Plus', cuenta, filas: [
    m('5 h', 34, ahora + 3 * H + 20 * 60e3),
    m('semana', 51, ahora + 2 * D + 7 * H),
    m('GPT-5.3-Codex-Spark · 5 h', 12, ahora + 3 * H + 20 * 60e3, { grupo: 'por modelo' }),
    m('GPT-5.3-Codex-Spark · semana', 4, ahora + 2 * D + 7 * H, { grupo: 'por modelo' }),
    d('créditos', 'saldo', '1.250', 'alcanza para ~180 mensajes locales · ~45 en la nube'),
    d('créditos', 'reinicios guardados', '0 disponibles', '2 ganados en total'),
    { grupo: 'estadísticas de la cuenta', nombre: 'tokens por día · 30 días', serie: serie(30, 14e6, 13e6, 0.4), desde: desde(30), valor: '421 M tokens', nota: 'en todas las superficies (CLI, app y web), según OpenAI' },
    d('estadísticas de la cuenta', 'tokens de por vida', '2,3 G', 'pico de un día: 88 M'),
    d('estadísticas de la cuenta', 'racha', '9 días seguidos', 'la más larga: 21 días'),
    d('estadísticas de la cuenta', 'hilos', '612', 'el turno más largo duró 1 h 48 min'),
    d('estadísticas de la cuenta', 'razonamiento más usado', 'high', '58 % de los turnos'),
    d('Codex en esta PC', 'hoy', '31 M tokens', '96 pedidos · entrada 30 M (caché 27 M) · salida 410 k (razonamiento 300 k)'),
    { grupo: 'Codex en esta PC', nombre: 'tokens por día', serie: [19e6, 44e6, 8e6, 0, 27e6, 52e6, 31e6], desde: desde(7), nota: 'cada sesión cuenta el día en que empezó' },
    d('por modelo · 7 días', 'gpt-5.5-codex', '181 M tokens', '688 pedidos · salida 2,1 M'),
  ] },
  { id: 'agy', nombre: 'Antigravity', plan: 'Google AI Pro', cuenta: 'ana@ejemplo.com', filas: [
    m('semana', 38, ahora + 13 * H, { grupo: 'Gemini Flash y Pro' }),
    m('5 h', 12, ahora + 4 * H + 40 * 60e3, { grupo: 'Gemini Flash y Pro' }),
    m('semana', 100, ahora + D + 3 * H, { grupo: 'Claude y GPT' }),
    m('5 h', 0, ahora + 5 * H, { grupo: 'Claude y GPT', apagado: true, nota: 'no aplica ahora' }),
    m('semana', 71, ahora + 2 * D + 9 * H, { grupo: 'Gemini 4 Argon', nota: 'según Agy, a las 10:42' }),
    d('modelos', 'Gemini · 13 modelos', 'queda 62 %', '2.5 Pro, 3 Flash, 3.1 Pro (High) (se va), 3.5 Flash (High), 3.6 Flash (Medium), 3.8 Flash (High)…'),
    d('modelos', 'Claude · 6 modelos', 'agotado', 'Opus 5.5 (High) (nuevo), Opus 5.5 (Medium) (nuevo), Sonnet 5.5 (High) (nuevo)…', { renueva: ahora + D + 3 * H }),
    d('modelos', 'GPT-OSS · 1 modelo', 'agotado', '120B (Medium)', { renueva: ahora + D + 3 * H }),
  ] },
  { id: 'grok', nombre: 'Grok', plan: 'SuperGrok', cuenta, filas: [
    m('créditos · semana', 44, ahora + D + 14 * H, { nota: 'desde el 15 oct 2026' }),
    d('reparto por producto', 'Grok Build', '30 %'),
    d('reparto por producto', 'Grok Chat', '14 %'),
    d('créditos', 'a demanda', 'apagado', 'cuando se terminan los créditos no se cobra extra'),
    d('créditos', 'saldo prepago', 'US$ 12,00', 'recarga con la tarjeta guardada'),
  ] },
  { id: 'kimi', nombre: 'Kimi', plan: 'Tier1 · pago por uso', cuenta: '', filas: [
    { nombre: 'saldo disponible', valor: 'US$ 4,20', nota: 'efectivo US$ 4,20 · cupones US$ 0,00', card: true },
    m('cuota de tokens', 22, 0, { cuota: true, valor: '116 k de 525 k' }),
    d('límites de la cuenta', 'pedidos por minuto', '200'),
    d('límites de la cuenta', 'tokens por minuto', '2 M'),
    d('límites de la cuenta', 'tokens por día', 'sin tope'),
    d('límites de la cuenta', 'estado', 'activa'),
  ] },
  { id: 'deepseek', nombre: 'DeepSeek', plan: 'pago por uso', nota: 'El uso por día y por modelo sólo está en platform.deepseek.com (con la sesión web).', filas: [
    { nombre: 'saldo', valor: 'US$ 7,85', nota: 'cargado US$ 7,50 · regalado US$ 0,35', card: true },
  ] },
  { id: 'qwen', nombre: 'Qwen', plan: 'pago por uso', nota: 'DashScope no publica el saldo con la API key: esto es lo que anota Qwen Code en esta PC.', filas: [
    { nombre: 'este mes', valor: '12 M tokens', nota: '214 pedidos', card: true },
    { nombre: 'hoy', valor: '840 k tokens', nota: '17 pedidos', card: true },
    { grupo: 'este mes', nombre: 'tokens por día', serie: serie(new Date().getDate(), 500e3, 480e3, 1.1), desde: hoy0 - (new Date().getDate() - 1) * D },
    d('este mes', 'entrada', '11 M', 'de caché 8,9 M'),
    d('este mes', 'salida', '420 k', 'razonamiento 160 k'),
    d('este mes', 'tiempo de respuesta', '6,4 s en promedio'),
    d('por modelo · este mes', 'qwen3.8-max', '12 M tokens', '214 pedidos'),
  ] },
  { id: 'you', nombre: 'You.com', plan: 'créditos', nota: 'El historial de uso sólo está en el panel web de you.com/platform.', filas: [
    { nombre: 'saldo', valor: 'US$ 18,40', nota: 'créditos de la API (búsqueda, research)', card: true },
  ] },
  { id: 'mistral', nombre: 'Mistral', plan: 'Free', nota: 'Mistral no publica cuánto usaste con la API key: el detalle está en admin.mistral.ai (con la sesión web).', filas: [
    d('cuenta', 'plan', 'Free · de la API', 'según Mistral Vibe'),
  ] },
  { id: 'copilot', nombre: 'Copilot', plan: 'Individual', cuenta: 'ana-ruiz', filas: [
    m('pedidos premium', 27, new Date(new Date().getFullYear(), new Date().getMonth() + 1, 1).getTime(), { valor: 'quedan 219 de 300' }),
    d('cuotas', 'excedentes', 'no permitidos'),
    d('cuotas', 'chat', 'sin límite'),
    d('cuotas', 'autocompletado', 'sin límite'),
    d('acceso', 'Copilot CLI', 'sí'),
    d('acceso', 'MCP', 'sí'),
  ] },
  { id: 'zai', nombre: 'Z.ai', plan: 'Pro', filas: [
    m('tokens · 5 h', 18, ahora + 2 * H, { valor: '540 de 3.000' }),
    m('herramientas MCP · 1 mes', 4, ahora + 11 * D, { valor: '40 de 1.000' }),
  ] },
].map((x) => ({ cuando: ahora - 40e3, ...x }));

const vista = { ias, ultima: ahora - 40e3, faltan: 0 };

// ---- la página ---------------------------------------------------------------------------------

const ui = (f) => fs.readFileSync(path.join(raiz, 'ui', f), 'utf8');
const demo = `<script>window.ready=()=>Promise.resolve(${JSON.stringify(vista)});window.act=()=>{};window.win=()=>{};</script>`;
const html = ui('index.html')
  .replace('<!--css-->', () => `<style>${ui('app.css')}</style>`)
  .replace('<!--js-->', () => `${demo}<script>${ui('app.js')}</script>`);
const pagina = path.join(tmp, 'demo.html');
fs.writeFileSync(pagina, html);
if (process.argv.includes('--html')) {
  console.log(pagina);
  process.exit(0);
}

// ---- navegador sin ventana, por CDP --------------------------------------------------------------

const navegador = ['C:/Program Files/Google/Chrome/Application/chrome.exe', 'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe']
  .find((p) => fs.existsSync(p));
const puerto = 9347;
const proc = spawn(navegador, ['--headless=new', `--remote-debugging-port=${puerto}`, `--user-data-dir=${path.join(tmp, 'perfil')}`,
  '--hide-scrollbars', '--force-device-scale-factor=1.5', '--window-size=400,560', '--no-first-run', 'about:blank'], { stdio: 'ignore' });
const espera = (ms) => new Promise((r) => setTimeout(r, ms));
let lista;
for (let i = 0; i < 50 && !lista; i++) {
  await espera(200);
  lista = await fetch(`http://127.0.0.1:${puerto}/json`).then((r) => r.json()).catch(() => null);
}
const ws = new WebSocket(lista.find((t) => t.type === 'page').webSocketDebuggerUrl);
await new Promise((r) => { ws.onopen = r; });
let n = 0;
const pend = new Map();
const cuadros = [];
ws.onmessage = (e) => {
  const msg = JSON.parse(e.data);
  if (msg.id && pend.has(msg.id)) {
    pend.get(msg.id)(msg.result);
    pend.delete(msg.id);
  } else if (msg.method === 'Page.screencastFrame') {
    cuadros.push({ t: msg.params.metadata.timestamp, data: msg.params.data });
    ws.send(JSON.stringify({ id: ++n, method: 'Page.screencastFrameAck', params: { sessionId: msg.params.sessionId } }));
  }
};
const cdp = (method, params = {}) => new Promise((r) => {
  const id = ++n;
  pend.set(id, r);
  ws.send(JSON.stringify({ id, method, params }));
});
const evalua = async (js) => (await cdp('Runtime.evaluate', { expression: js, awaitPromise: true, returnByValue: true }))?.result?.value;
const tamano = (alto) => cdp('Emulation.setDeviceMetricsOverride', { width: 400, height: alto, deviceScaleFactor: 1.5, mobile: false });
const pestana = (id) => evalua(`document.querySelector('.tab[data-tab="${id}"]').click()`);

await cdp('Page.enable');
await tamano(560);
await cdp('Page.navigate', { url: pathToFileURL(pagina).href });
await espera(1500);
const ids = await evalua(`[...document.querySelectorAll('.tab')].map(t => t.dataset.tab)`);

// capturas: cada pestaña entera (la ventana se estira hasta que entra todo)
for (const id of ids) {
  await tamano(560);
  await pestana(id);
  await espera(950);
  const alto = await evalua(`document.getElementById('page').scrollHeight + 38 + 33 + 34 + 2`);
  await tamano(Math.max(560, alto));
  await espera(300);
  const img = await cdp('Page.captureScreenshot', { format: 'png' });
  fs.writeFileSync(path.join(docs, `${id}.png`), Buffer.from(img.data, 'base64'));
  console.log('captura', id, alto);
}

// recorrido: el panel de verdad (400×560) pasando por todas las pestañas
await tamano(560);
await pestana('todas');
await espera(800);
await cdp('Page.startScreencast', { format: 'png', everyNthFrame: 1 });
await espera(2200);
for (const id of ids.slice(1)) {
  await pestana(id);
  await espera(id === 'claude' || id === 'agy' || id === 'codex' ? 2300 : 1500);
}
await pestana('todas');
await espera(1200);
await cdp('Page.stopScreencast');
ws.close();
proc.kill();

// ---- GIF y video ---------------------------------------------------------------------------------

const cdir = path.join(tmp, 'cuadros');
fs.rmSync(cdir, { recursive: true, force: true });
fs.mkdirSync(cdir);
const lineas = [];
cuadros.forEach((c, i) => {
  const f = path.join(cdir, `${String(i).padStart(5, '0')}.png`);
  fs.writeFileSync(f, Buffer.from(c.data, 'base64'));
  const dur = i + 1 < cuadros.length ? Math.max(0.02, cuadros[i + 1].t - c.t) : 1.2;
  lineas.push(`file '${f.replaceAll('\\', '/')}'`, `duration ${dur.toFixed(3)}`);
});
lineas.push(lineas[lineas.length - 2]); // ffmpeg concat: el último cuadro va dos veces para que respete su duración
fs.writeFileSync(path.join(tmp, 'cuadros.txt'), lineas.join('\n'));
const ff = (args) => execFileSync('ffmpeg', ['-hide_banner', '-loglevel', 'error', '-y', '-f', 'concat', '-safe', '0', '-i', path.join(tmp, 'cuadros.txt'), ...args], { stdio: 'inherit' });
ff(['-vf', 'fps=15,split[a][b];[a]palettegen=max_colors=128:stats_mode=diff[p];[b][p]paletteuse=dither=bayer:bayer_scale=4:diff_mode=rectangle',
  '-loop', '0', path.join(docs, 'pestanas.gif')]);
ff(['-vf', 'fps=30,format=yuv420p', '-c:v', 'libvpx-vp9', '-b:v', '0', '-crf', '32', '-row-mt', '1', path.join(docs, 'pestanas.webm')]);
for (const f of ['pestanas.gif', 'pestanas.webm']) console.log(f, Math.round(fs.statSync(path.join(docs, f)).size / 1024), 'KB');
console.log(cuadros.length, 'cuadros');
