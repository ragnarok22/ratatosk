package inspector

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
)

var fallbackPorts = []int{4040, 5050, 6060, 7070, 8080}

// StartServer starts the inspector web UI on the first available port.
// It returns the bound address (e.g. "127.0.0.1:4040") or an error if
// no port could be bound.
func StartServer(logger *Logger) (string, error) {
	var ln net.Listener
	var err error
	for _, port := range fallbackPorts {
		addr := fmt.Sprintf("127.0.0.1:%d", port)
		ln, err = net.Listen("tcp", addr)
		if err == nil {
			break
		}
	}
	if ln == nil {
		return "", fmt.Errorf("inspector: failed to bind on any port: %w", err)
	}

	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/logs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(logger.Entries())
	})

	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, inspectorHTML)
	})

	go http.Serve(ln, mux)

	return ln.Addr().String(), nil
}

const inspectorHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Ratatosk Inspector</title>
<style>
  * { margin: 0; padding: 0; box-sizing: border-box; }
  body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, sans-serif; background: #0f1117; color: #c9d1d9; }
  header { background: #161b22; padding: 14px 24px; border-bottom: 1px solid #30363d; display: flex; align-items: center; gap: 12px; }
  header h1 { font-size: 16px; font-weight: 600; color: #e6edf3; }
  header span { font-size: 12px; color: #8b949e; }
  .empty { text-align: center; padding: 60px 20px; color: #8b949e; }
  table { width: 100%; border-collapse: collapse; }
  thead th { position: sticky; top: 0; background: #161b22; text-align: left; padding: 10px 16px; font-size: 12px; font-weight: 600; color: #8b949e; text-transform: uppercase; letter-spacing: 0.5px; border-bottom: 1px solid #30363d; }
  tbody tr { cursor: pointer; border-bottom: 1px solid #21262d; transition: background 0.15s; }
  tbody tr:hover { background: #1c2128; }
  tbody td { padding: 10px 16px; font-size: 13px; font-family: "SFMono-Regular", Consolas, "Liberation Mono", Menlo, monospace; }
  .method { font-weight: 700; }
  .status-2 { color: #3fb950; }
  .status-3 { color: #58a6ff; }
  .status-4 { color: #d29922; }
  .status-5 { color: #f85149; }
  .detail { display: none; background: #161b22; }
  .detail td { padding: 0; }
  .detail-inner { padding: 16px 24px; display: grid; grid-template-columns: 1fr 1fr; gap: 20px; }
  .detail-inner h3 { font-size: 12px; font-weight: 600; color: #8b949e; text-transform: uppercase; margin-bottom: 8px; grid-column: span 1; }
  .detail-inner pre { background: #0d1117; border: 1px solid #30363d; border-radius: 6px; padding: 12px; font-size: 12px; white-space: pre-wrap; word-break: break-all; max-height: 300px; overflow: auto; }
  .badge { display: inline-block; padding: 2px 8px; border-radius: 12px; font-size: 11px; font-weight: 600; }
  .badge-get { background: #1f6feb33; color: #58a6ff; }
  .badge-post { background: #3fb95033; color: #3fb950; }
  .badge-put { background: #d2992233; color: #d29922; }
  .badge-delete { background: #f8514933; color: #f85149; }
  .badge-patch { background: #a371f733; color: #a371f7; }
  .badge-default { background: #30363d; color: #8b949e; }
  #count { background: #30363d; color: #c9d1d9; padding: 2px 8px; border-radius: 10px; font-size: 12px; font-weight: 600; }
</style>
</head>
<body>
<header>
  <h1>Ratatosk Inspector</h1>
  <span id="count">0 requests</span>
</header>
<div id="content">
  <div class="empty" id="empty">Waiting for requests&hellip;</div>
  <table id="table" style="display:none">
    <thead>
      <tr>
        <th>#</th>
        <th>Method</th>
        <th>Path</th>
        <th>Status</th>
        <th>Duration</th>
        <th>Time</th>
      </tr>
    </thead>
    <tbody id="tbody"></tbody>
  </table>
</div>
<script>
function methodBadge(m) {
  const cls = {GET:'badge-get',POST:'badge-post',PUT:'badge-put',DELETE:'badge-delete',PATCH:'badge-patch'}[m] || 'badge-default';
  return '<span class="badge '+cls+'">'+m+'</span>';
}
function statusClass(s) { return 'status-'+String(s).charAt(0); }
function fmtDuration(ns) {
  const ms = ns / 1e6;
  return ms < 1000 ? ms.toFixed(1)+' ms' : (ms/1000).toFixed(2)+' s';
}
function fmtTime(ts) {
  try { return new Date(ts).toLocaleTimeString(); } catch(e) { return ts; }
}
function fmtHeaders(h) {
  if (!h || Object.keys(h).length === 0) return '(none)';
  return Object.entries(h).map(([k,v]) => k+': '+v).join('\n');
}
function escapeHtml(s) {
  const d = document.createElement('div'); d.textContent = s || ''; return d.innerHTML;
}
function fmtRespBody(l) {
  if (!l.resp_body) return '<pre>(empty)</pre>';
  if (l.resp_body_binary) {
    const ct = (l.resp_headers && l.resp_headers['Content-Type']) || '';
    if (ct.startsWith('image/')) {
      return '<img src="data:'+escapeHtml(ct)+';base64,'+l.resp_body+'" style="max-width:100%;border-radius:6px;margin-top:4px">';
    }
    return '<pre>(binary ' + l.resp_body.length + ' bytes base64)</pre>';
  }
  return '<pre>'+escapeHtml(l.resp_body)+'</pre>';
}

const tbody = document.getElementById('tbody');
const table = document.getElementById('table');
const empty = document.getElementById('empty');
const count = document.getElementById('count');
let expanded = null;

function render(logs) {
  count.textContent = logs.length + ' request' + (logs.length !== 1 ? 's' : '');
  if (logs.length === 0) { table.style.display='none'; empty.style.display=''; return; }
  table.style.display=''; empty.style.display='none';

  tbody.innerHTML = '';
  for (let i = logs.length - 1; i >= 0; i--) {
    const l = logs[i];
    const tr = document.createElement('tr');
    tr.innerHTML =
      '<td>'+l.id+'</td>'+
      '<td class="method">'+methodBadge(l.method)+'</td>'+
      '<td>'+escapeHtml(l.path)+'</td>'+
      '<td class="'+statusClass(l.resp_status)+'">'+l.resp_status+'</td>'+
      '<td>'+fmtDuration(l.duration)+'</td>'+
      '<td>'+fmtTime(l.timestamp)+'</td>';
    tr.onclick = function() { toggle(l.id); };
    tbody.appendChild(tr);

    const detail = document.createElement('tr');
    detail.className = 'detail';
    detail.id = 'detail-'+l.id;
    if (expanded === l.id) detail.style.display = 'table-row';
    detail.innerHTML = '<td colspan="6"><div class="detail-inner">'+
      '<div><h3>Request Headers</h3><pre>'+escapeHtml(fmtHeaders(l.req_headers))+'</pre></div>'+
      '<div><h3>Response Headers</h3><pre>'+escapeHtml(fmtHeaders(l.resp_headers))+'</pre></div>'+
      '<div><h3>Request Body</h3><pre>'+escapeHtml(l.req_body || '(empty)')+'</pre></div>'+
      '<div><h3>Response Body</h3>'+fmtRespBody(l)+'</div>'+
      '</div></td>';
    tbody.appendChild(detail);
  }
}

function toggle(id) {
  const el = document.getElementById('detail-'+id);
  if (!el) return;
  if (expanded === id) { el.style.display = 'none'; expanded = null; }
  else {
    if (expanded !== null) { const prev = document.getElementById('detail-'+expanded); if (prev) prev.style.display='none'; }
    el.style.display = 'table-row'; expanded = id;
  }
}

async function poll() {
  try {
    const r = await fetch('/api/logs');
    const logs = await r.json();
    render(logs);
  } catch(e) {}
}

poll();
setInterval(poll, 2000);
</script>
</body>
</html>`
