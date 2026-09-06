/**
 * Responsive regression check.
 *
 * Loads every route in a running build at phone, tablet and desktop widths and
 * reports any element that extends past the viewport - the defect that makes a
 * page need horizontal scrolling on a phone. Exits non-zero if any route
 * overflows, so it can gate a change.
 *
 * Usage:
 *   corepack yarn build && corepack yarn start -p 3000 &
 *   node scripts/responsive-check.mjs http://127.0.0.1:3000
 *
 * It drives Chromium over the DevTools protocol with Node's built-in
 * WebSocket, so it needs no extra dependency. Device metrics are set through
 * Emulation.setDeviceMetricsOverride rather than --window-size: a small
 * --window-size is silently clamped by the browser, which makes a phone-width
 * screenshot a lie (it renders wide and crops, so everything looks broken
 * whether it is or not).
 */
import { spawn } from 'node:child_process';
import { existsSync } from 'node:fs';

const BASE = process.argv[2] || 'http://127.0.0.1:3000';

const ROUTES = [
  '/',
  '/how-it-works',
  '/download',
  '/products/marketplace',
  '/products/inference',
  '/products/consensus',
  '/products/token',
  '/products/console',
  '/products/cli',
  '/docs',
  '/docs/introduction',
  '/docs/installation',
  '/docs/quickstart',
  '/docs/architecture',
  '/docs/compute-marketplace',
  '/docs/configuration',
  '/docs/cli',
  '/docs/matrix-protocol',
  '/docs/soul-protocol',
  '/docs/guides/agent-development',
  '/docs/guides/network-setup',
];

const VIEWPORTS = [
  { name: 'phone', width: 390, height: 844, mobile: true },
  { name: 'tablet', width: 768, height: 1024, mobile: true },
  { name: 'desktop', width: 1440, height: 900, mobile: false },
];

/** Decorative elements are allowed to spill: they are clipped and not content. */
const IGNORE = [/blur-3xl/, /pointer-events-none/];

const CHROME_CANDIDATES = [
  process.env.CHROME_PATH,
  '/opt/pw-browsers/chromium-1194/chrome-linux/chrome',
  '/usr/bin/chromium',
  '/usr/bin/google-chrome',
].filter(Boolean);

const chrome = CHROME_CANDIDATES.find((p) => existsSync(p));
if (!chrome) {
  console.error('No Chromium found. Set CHROME_PATH.');
  process.exit(2);
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const port = 9200 + Math.floor(Math.random() * 700);
const proc = spawn(chrome, [
  '--headless=new',
  '--disable-gpu',
  '--no-sandbox',
  '--hide-scrollbars',
  // Keep the browser off the network for anything but the page under test.
  '--disable-background-networking',
  '--disable-component-update',
  '--disable-sync',
  '--no-first-run',
  '--no-default-browser-check',
  `--remote-debugging-port=${port}`,
  'about:blank',
], { stdio: 'ignore' });

async function debuggerUrl() {
  for (let i = 0; i < 80; i++) {
    try {
      const res = await fetch(`http://127.0.0.1:${port}/json/version`);
      return (await res.json()).webSocketDebuggerUrl;
    } catch {
      await sleep(250);
    }
  }
  throw new Error('Chromium DevTools endpoint never came up');
}

const ws = new WebSocket(await debuggerUrl());
await new Promise((r) => ws.addEventListener('open', r, { once: true }));

let seq = 0;
const pending = new Map();
ws.addEventListener('message', (ev) => {
  const msg = JSON.parse(ev.data);
  if (!msg.id || !pending.has(msg.id)) return;
  const { resolve, reject } = pending.get(msg.id);
  pending.delete(msg.id);
  if (msg.error) {
    reject(new Error(JSON.stringify(msg.error)));
  } else {
    resolve(msg.result);
  }
});
const send = (method, params = {}, sessionId) =>
  new Promise((resolve, reject) => {
    const id = ++seq;
    pending.set(id, { resolve, reject });
    ws.send(JSON.stringify({ id, method, params, sessionId }));
  });

const { targetId } = await send('Target.createTarget', { url: 'about:blank' });
const { sessionId } = await send('Target.attachToTarget', { targetId, flatten: true });
await send('Page.enable', {}, sessionId);

const MEASURE = `(() => {
  const vw = window.innerWidth;
  const out = { viewport: vw, scrollWidth: document.documentElement.scrollWidth, offenders: [] };
  for (const el of document.querySelectorAll('body *')) {
    const r = el.getBoundingClientRect();
    if (r.width === 0 && r.height === 0) continue;
    const over = Math.round(r.right - vw);
    if (over > 2) {
      // An element wider than the viewport is fine when an ancestor scrolls it:
      // that is what a code block does. Only unscrollable overflow is a defect.
      let scrolled = false;
      for (let p = el.parentElement; p; p = p.parentElement) {
        const ox = getComputedStyle(p).overflowX;
        if (ox === 'auto' || ox === 'scroll' || ox === 'hidden') { scrolled = true; break; }
      }
      if (scrolled) continue;
      out.offenders.push({
        tag: el.tagName.toLowerCase(),
        cls: String(el.className || '').slice(0, 100),
        width: Math.round(r.width),
        over,
        text: (el.textContent || '').trim().slice(0, 40),
      });
    }
  }
  out.offenders.sort((a, b) => b.over - a.over);
  out.offenders = out.offenders.slice(0, 8);
  return JSON.stringify(out);
})()`;

let failures = 0;
for (const vp of VIEWPORTS) {
  await send('Emulation.setDeviceMetricsOverride', {
    width: vp.width,
    height: vp.height,
    deviceScaleFactor: 1,
    mobile: vp.mobile,
  }, sessionId);

  for (const route of ROUTES) {
    await send('Page.navigate', { url: BASE + route }, sessionId);
    await sleep(700);
    const { result } = await send('Runtime.evaluate', {
      expression: MEASURE,
      returnByValue: true,
    }, sessionId);
    const data = JSON.parse(result.value);
    const real = data.offenders.filter((o) => !IGNORE.some((re) => re.test(o.cls)));
    const scrollOverflow = data.scrollWidth - data.viewport > 2;

    if (real.length === 0 && !scrollOverflow) continue;
    failures++;
    console.log(`FAIL ${vp.name} (${vp.width}px) ${route}`);
    if (scrollOverflow) {
      console.log(`  document scrolls to ${data.scrollWidth}px in a ${data.viewport}px viewport`);
    }
    for (const o of real) {
      console.log(`  +${o.over}px  <${o.tag} class="${o.cls}">  ${JSON.stringify(o.text)}`);
    }
  }
}

ws.close();
proc.kill('SIGKILL');

if (failures > 0) {
  console.log(`\n${failures} route/viewport combination(s) overflow.`);
  process.exit(1);
}
console.log(`No overflow across ${ROUTES.length} routes x ${VIEWPORTS.length} viewports.`);
