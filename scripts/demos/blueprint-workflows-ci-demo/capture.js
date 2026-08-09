// capture.js — screenshots of the Argo Workflows UI for the user guide.
//
//   node capture.js <https-base> <out-dir> <namespace> [step ...]
//
// Steps: list | submit:<wf.yaml> | wf:<name> | wf-logs:<name> | wf-yaml:<name>
//        cm:<configmap>
//
// WHY THIS LIVES WITH THE GUIDE. The screenshots ARE the guide — a reader follows
// the pictures. So the thing that produces them is versioned next to it; a capture
// script in someone's home directory cannot be re-run by the next person who needs
// to refresh a stale image.
//
// SECRETS. These runs carry a real IBM Cloud API key, a Harbor password and a
// BIG-IP password. The Argo UI will happily render a step's environment, so every
// frame is scrubbed in the DOM before the shutter: any text matching a registered
// secret is replaced, and password-ish key/value pairs are masked by pattern even
// when their value was not registered. Pass secrets via SHOT_SECRETS (newline
// separated) — never on argv, which shows up in `ps`.
// REQUIRES puppeteer, which is not vendored here (a headless Chromium is ~150 MB
// and has no business in this repo). Point NODE_PATH at an install:
//
//   NODE_PATH=$(npm root -g) node capture.js …
//
if (!process.env.NODE_PATH) {
  // A bare `require('puppeteer')` failure reads as a broken script; say what it is.
  try { require.resolve('puppeteer'); }
  catch { console.error('puppeteer not resolvable — set NODE_PATH to an install, e.g. NODE_PATH=$(npm root -g)'); process.exit(2); }
}
const puppeteer = require('puppeteer');
const fs = require('fs');
const path = require('path');

const [, , base0, outDir, ns, ...steps] = process.argv;
const base = (base0 || '').replace(/\/$/, '');
if (!base || !outDir || !ns) {
  console.error('usage: node capture.js <https-base> <out-dir> <namespace> [step ...]');
  process.exit(2);
}
fs.mkdirSync(outDir, { recursive: true });
const SECRETS = (process.env.SHOT_SECRETS || '').split('\n').map(s => s.trim()).filter(s => s.length > 7);

const sleep = ms => new Promise(r => setTimeout(r, ms));

(async () => {
  const browser = await puppeteer.launch({
    headless: 'new', ignoreHTTPSErrors: true, acceptInsecureCerts: true,
    args: ['--no-sandbox', '--disable-gpu', '--disable-dev-shm-usage', '--ignore-certificate-errors',
           // Chrome silently rewrites http:// to https:// otherwise, which makes a
           // localhost tunnel look like a dead server.
           '--disable-features=HttpsUpgrades,HttpsFirstBalancedMode,HttpsFirstModeV2'],
  });
  const page = await browser.newPage();
  await page.setViewport({ width: 1600, height: 1000, deviceScaleFactor: 2 });

  // Argo's first-run survey/banner would otherwise sit over the first screenshot.
  await page.evaluateOnNewDocument(() => {
    try {
      localStorage.setItem('foreground', 'false');
      localStorage.setItem('newProductBanner', 'false');
      localStorage.setItem('surveyDismissed', 'true');
    } catch (e) {}
  });

  const settle = async () => {
    // Argo v4 shows a "Tell us what you want to use Argo for" survey modal on first
    // visit. Pre-seeding localStorage does NOT reliably suppress it (the keys differ
    // between versions), and it covers the whole DAG — the one thing the guide needs
    // to show. So dismiss it structurally: click its ✕, then remove any leftover
    // modal and its backdrop by content match.
    for (const sel of ['i.fa-times', '.fa-times', 'button[aria-label="Close"]']) {
      for (const el of await page.$$(sel)) { await el.click().catch(() => {}); }
    }
    await page.evaluate(() => {
      const kill = (el) => { if (el && el.parentElement) el.remove(); };
      // The survey modal, by its copy — and ONLY if it is actually present.
      let sawModal = false;
      document.querySelectorAll('div,section').forEach(el => {
        const t = el.textContent || '';
        if (/Tell us what you want to use Argo for/i.test(t) && t.length < 600) {
          sawModal = true;
          let n = el;
          for (let i = 0; i < 8 && n; i++, n = n.parentElement) {
            const cs = getComputedStyle(n);
            if (cs.position === 'fixed' || parseInt(cs.zIndex || '0', 10) > 100) { kill(n); return; }
          }
          kill(el);
        }
      });
      // Its greyed backdrop — but ONLY when the modal was there. A width-and-position
      // heuristic on its own also matches Argo's main content wrapper, which blanked
      // five screenshots to an empty grey rectangle before this guard existed. The
      // backdrop must additionally be textless: a real overlay has no content.
      if (sawModal) {
        document.querySelectorAll('div').forEach(el => {
          const cs = getComputedStyle(el);
          const empty = (el.textContent || '').trim().length === 0;
          if (empty && cs.position === 'fixed' && parseInt(cs.zIndex || '0', 10) > 50 &&
              /rgba?\(0, 0, 0/.test(cs.backgroundColor) && el.clientWidth > window.innerWidth * 0.8) kill(el);
        });
      }
      // The transient "Failed to connect to api/v1/workflow-events…" toast. It fires
      // whenever the event stream reconnects — including the first paint after a
      // namespace appears — and says nothing about the workflow. Its markup differs
      // between the list and detail views, so match on the TEXT and climb to the
      // banner: the highest ancestor that still contains only this message.
      {
        const PHRASE = 'workflow-events';
        const holders = Array.from(document.querySelectorAll('div,span,p,li'))
          .filter(el => (el.textContent || '').includes(PHRASE));
        // Deepest first, so the climb starts inside the banner rather than at <body>.
        holders.sort((a, b) => b.compareDocumentPosition(a) & 2 ? 1 : -1);
        const deepest = holders[holders.length - 1];
        if (deepest) {
          // Climb to the banner — but bound it by SIZE, not only by text length.
          // A DAG page is mostly SVG, which contributes almost nothing to
          // textContent, so a text-length bound never trips and the climb walks all
          // the way to the content container. Removing that renders the entire page
          // blank: it produced five identical 23KB grey rectangles, and no amount of
          // waiting fixed them because the cause was not timing.
          let n = deepest;
          while (n.parentElement) {
            const p2 = n.parentElement;
            if (p2 === document.body || p2.tagName === 'MAIN') break;
            if (!(p2.textContent || '').includes(PHRASE)) break;
            if ((p2.textContent || '').length >= 900) break;
            if (p2.clientHeight > window.innerHeight * 0.4) break;   // too big to be a toast
            if (p2.clientWidth > window.innerWidth * 0.98) break;
            n = p2;
          }
          // Final sanity: never remove something that fills the page.
          if (n.clientHeight <= window.innerHeight * 0.4) kill(n);
        }
      }
      document.querySelectorAll('[class*="survey"], .foreground').forEach(kill);
    }).catch(() => {});
    await sleep(1200);
  };

  const scrub = async (secrets) => {
    await page.evaluate((secs) => {
      const MASK = '***REDACTED***';
      const walk = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
      const hits = [];
      while (walk.nextNode()) hits.push(walk.currentNode);
      for (const n of hits) {
        let v = n.nodeValue;
        if (!v) continue;
        let out = v;
        for (const s of secs) if (s && out.includes(s)) out = out.split(s).join(MASK);
        // Belt and braces: mask anything that LOOKS like a credential even when the
        // value was never registered — a rotated key would otherwise slip through.
        out = out.replace(/((?:api[_-]?key|password|passwd|secret|token)"?\s*[:=]\s*"?)([^\s",}]{8,})/gi,
                          (_, k) => k + MASK);
        if (out !== v) n.nodeValue = out;
      }
      document.querySelectorAll('input[type=password], input[name*=pass i], input[name*=key i]')
        .forEach(i => { i.value = MASK; });
    }, secrets).catch(() => {});
  };

  // A screenshot of an unpainted page is a uniform rectangle, and it compresses to a
  // tiny PNG — every blank capture in this directory came out at exactly the same
  // byte size. That is a reliable signal, and cheaper than probing the DOM for
  // whichever element happens to be the content on each view. So: shoot, and if the
  // result is suspiciously small, wait and shoot again.
  const BLANK_BYTES = 40000;
  const shot = async (name, attempt = 0) => {
    await settle();
    await scrub(SECRETS);
    const file = path.join(outDir, name.endsWith('.png') ? name : name + '.png');
    await page.screenshot({ path: file, fullPage: false });
    const size = fs.statSync(file).size;
    if (size < BLANK_BYTES && attempt < 3) {
      console.log(`  ${path.basename(file)} came out at ${size}B — likely unpainted, retrying`);
      await sleep(4000);
      return shot(name, attempt + 1);
    }
    if (size < BLANK_BYTES) console.error(`  WARNING ${path.basename(file)} is ${size}B — probably blank`);
    console.log('captured', file, `(${size}B)`);
  };

  const go = async (url) => {
    await page.goto(url, { waitUntil: 'networkidle2', timeout: 90000 }).catch(() => {});
    await sleep(3500);
  };

  for (const step of steps) {
    const [kind, argRaw] = step.split(':');
    const arg = argRaw || '';
    if (kind === 'list') {
      await go(`${base}/workflows/${ns}`);
      await shot(arg || 'workflows-list');
    } else if (kind === 'wf') {
      await go(`${base}/workflows/${ns}/${arg}`);
      await shot(`wf-${arg}`);
    } else if (kind === 'wf-logs') {
      await go(`${base}/workflows/${ns}/${arg}?tab=workflow&nodePanelSection=logs`);
      await shot(`wf-${arg}-logs`);
    } else if (kind === 'wf-yaml') {
      await go(`${base}/workflows/${ns}/${arg}?tab=manifest`);
      await shot(`wf-${arg}-manifest`);
    } else if (kind === 'submit') {
      await go(`${base}/workflows/${ns}?new=%7B%7D`);
      await shot(`submit-${path.basename(arg, '.yaml') || 'form'}`);
    } else {
      console.error('unknown step', step);
    }
  }
  await browser.close();
})().catch(e => { console.error('ERR', (e && e.message || e).toString().split('\n')[0]); process.exit(1); });
