package flpstatus

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// Serve runs the status web server on addr (e.g. ":80") with NO auth — the FLP is
// a private endpoint and this is read-only proxy status. Routes:
//
//	GET /            → the mobile-first status page
//	GET /api/status  → the Status snapshot as JSON
//	GET /api/logs    → Server-Sent Events stream of the f5-license-proxy log
func Serve(addr string, b Backend) error {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(Gather(r.Context(), b).JSON())
	})

	mux.HandleFunc("/api/logs", func(w http.ResponseWriter, r *http.Request) {
		fl, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		b.StreamProxyLog(ctx, sseWriter{w, fl})
	})

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprintln(w, "ok") })

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(indexHTML))
	})

	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	return srv.ListenAndServe()
}

type sseWriter struct {
	w  http.ResponseWriter
	fl http.Flusher
}

func (s sseWriter) WriteLine(line string) {
	fmt.Fprintf(s.w, "data: %s\n\n", line)
	s.fl.Flush()
}

// indexHTML is a self-contained, mobile-first page. It uses the Tailwind Play CDN
// for the utility classes; for a fully air-gapped operator device (no Internet on
// the viewing device) vendor a tailwind.min.css and swap the <script> for a
// <link>. No auth, no external calls beyond the CDN stylesheet.
const indexHTML = `<!doctype html>
<html lang="en" class="h-full">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="light dark">
<title>F5 License Proxy — status</title>
<script src="https://cdn.tailwindcss.com"></script>
</head>
<body class="h-full bg-slate-100 dark:bg-slate-900 text-slate-800 dark:text-slate-100">
<div class="max-w-xl mx-auto p-4 space-y-4">
  <header class="flex items-center justify-between">
    <h1 class="text-lg font-semibold">F5 License Proxy</h1>
    <span id="mode" class="text-xs px-2 py-1 rounded-full bg-slate-200 dark:bg-slate-700"></span>
  </header>

  <section class="grid grid-cols-2 gap-3">
    <div id="card-listener" class="rounded-2xl bg-white dark:bg-slate-800 shadow p-3">
      <div class="flex items-center gap-2"><span id="dot-listener" class="dot"></span><span class="font-medium">Listener :8443</span></div>
      <div id="detail-listener" class="text-xs text-slate-500 mt-1"></div>
    </div>
    <div id="card-teem" class="rounded-2xl bg-white dark:bg-slate-800 shadow p-3">
      <div class="flex items-center gap-2"><span id="dot-teem" class="dot"></span><span class="font-medium">F5 / TEEM</span></div>
      <div id="detail-teem" class="text-xs text-slate-500 mt-1"></div>
    </div>
  </section>

  <section class="rounded-2xl bg-white dark:bg-slate-800 shadow p-3">
    <h2 class="text-sm font-semibold mb-2">Dependent services</h2>
    <ul id="services" class="space-y-2"></ul>
  </section>

  <section class="rounded-2xl bg-white dark:bg-slate-800 shadow p-3">
    <h2 class="text-sm font-semibold mb-2">CNEInstance / bnk.flp.external</h2>
    <div class="text-xs space-y-1">
      <div><span class="text-slate-500">endpoint</span> <code id="cne-ep" class="break-all"></code></div>
      <div><span class="text-slate-500">mode</span> <code id="cne-mode"></code></div>
      <button id="copy-ca" class="mt-1 text-xs px-2 py-1 rounded bg-sky-600 text-white">Copy root CA (base64)</button>
    </div>
  </section>

  <section class="rounded-2xl bg-slate-900 text-slate-100 shadow p-3">
    <h2 class="text-sm font-semibold mb-2">f5-license-proxy log</h2>
    <pre id="log" class="text-[11px] leading-tight overflow-x-auto max-h-72 overflow-y-auto whitespace-pre-wrap"></pre>
  </section>

  <p id="ts" class="text-center text-[11px] text-slate-400"></p>
</div>

<style>
  .dot{width:.7rem;height:.7rem;border-radius:9999px;display:inline-block;background:#94a3b8}
  .up{background:#22c55e}.down{background:#ef4444}.pending{background:#f59e0b}.unknown{background:#94a3b8}
</style>
<script>
let caB64 = "";
function dot(el, ind){ el.className = "dot " + (ind||"unknown"); }
async function refresh(){
  try{
    const s = await (await fetch("/api/status",{cache:"no-store"})).json();
    document.getElementById("mode").textContent = s.deployment;
    dot(document.getElementById("dot-listener"), s.listener.indicator);
    document.getElementById("detail-listener").textContent = (s.listener.http_code? "HTTP "+s.listener.http_code : "no response");
    dot(document.getElementById("dot-teem"), s.teem.indicator);
    document.getElementById("detail-teem").textContent = s.teem.detail||"";
    const ul = document.getElementById("services"); ul.innerHTML="";
    (s.services||[]).forEach(sv=>{
      const li=document.createElement("li"); li.className="flex items-center gap-2";
      li.innerHTML='<span class="dot '+(sv.indicator||"unknown")+'"></span><span class="font-medium">'+sv.name+'</span><span class="text-xs text-slate-500 ml-auto truncate">'+(sv.detail||"")+'</span>';
      ul.appendChild(li);
    });
    document.getElementById("cne-ep").textContent = s.cne.endpoint||"";
    document.getElementById("cne-mode").textContent = s.cne.mode||"";
    caB64 = s.cne.root_ca_b64||"";
    document.getElementById("ts").textContent = "updated " + (s.checked_at||"");
  }catch(e){ document.getElementById("ts").textContent = "status unreachable"; }
}
document.getElementById("copy-ca").onclick = ()=>{ if(caB64) navigator.clipboard?.writeText(caB64); };
const log = document.getElementById("log");
try{
  const es = new EventSource("/api/logs");
  es.onmessage = (ev)=>{ log.textContent += ev.data+"\n"; log.scrollTop = log.scrollHeight;
    if(log.textContent.length>60000) log.textContent = log.textContent.slice(-40000); };
}catch(e){}
refresh(); setInterval(refresh, 3000);
</script>
</body>
</html>`
