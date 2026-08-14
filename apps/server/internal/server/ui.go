package server

import "net/http"

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(indexHTML))
}

const indexHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width,initial-scale=1">
  <title>mini-ci-cd</title>
  <style>
    :root{color-scheme:dark;--bg:#0b0d10;--panel:#15191f;--line:#29313b;--text:#f2f5f7;--muted:#9ba7b4;--accent:#69e3a7;--danger:#ff7b72}*{box-sizing:border-box}body{margin:0;background:radial-gradient(circle at top left,#16231e,var(--bg) 45%);font:15px/1.5 system-ui,sans-serif;color:var(--text);min-height:100vh;display:grid;place-items:center}.shell{width:min(92vw,520px)}.brand{font-weight:800;letter-spacing:-.04em;font-size:28px;margin:0 0 24px}.brand span{color:var(--accent)}.card{background:color-mix(in srgb,var(--panel) 92%,transparent);border:1px solid var(--line);border-radius:16px;padding:28px;box-shadow:0 20px 70px #0008}h1{font-size:22px;margin:0 0 8px}p{color:var(--muted);margin:0 0 24px}.field{margin:14px 0}label{display:block;margin-bottom:6px;font-size:13px;color:var(--muted)}input{width:100%;border:1px solid var(--line);background:#0e1217;color:var(--text);border-radius:9px;padding:11px 12px;outline:none}input:focus{border-color:var(--accent)}button{width:100%;margin-top:12px;border:0;border-radius:9px;padding:12px;font-weight:700;background:var(--accent);color:#07140d;cursor:pointer}button:disabled{opacity:.55;cursor:wait}.error{color:var(--danger);min-height:24px;margin-top:14px}.meta{display:flex;justify-content:space-between;border-top:1px solid var(--line);padding-top:18px;margin-top:18px;color:var(--muted)}.hidden{display:none}.pill{color:var(--accent)}
  </style>
</head>
<body><main class="shell"><div class="brand">mini<span>-ci-cd</span></div><section class="card" id="app">Loading…</section></main>
<script>
const app=document.querySelector('#app');
const esc=s=>String(s).replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
async function api(path,options={}){const r=await fetch(path,{...options,headers:{'Content-Type':'application/json',...(options.headers||{})}});if(r.status===204)return null;const body=await r.json();if(!r.ok)throw new Error(body.error?.message||'Request failed');return body}
function bind(form,path){form.addEventListener('submit',async e=>{e.preventDefault();const btn=form.querySelector('button'),err=form.querySelector('.error');btn.disabled=true;err.textContent='';try{const data=Object.fromEntries(new FormData(form));await api(path,{method:'POST',body:JSON.stringify(data)});await boot()}catch(x){err.textContent=x.message}finally{btn.disabled=false}})}
function setup(){app.innerHTML='<h1>Create the Owner account</h1><p>This account has full control of deployments. Public registration closes after setup.</p><form><div class="field"><label>Email</label><input name="email" type="email" autocomplete="email" required></div><div class="field"><label>Username</label><input name="username" minlength="2" maxlength="64" required></div><div class="field"><label>Password</label><input name="password" type="password" minlength="12" autocomplete="new-password" required></div><div class="field"><label>Confirm password</label><input name="confirmPassword" type="password" minlength="12" autocomplete="new-password" required></div><button>Create Owner</button><div class="error"></div></form>';bind(app.querySelector('form'),'/api/v1/setup')}
function login(){app.innerHTML='<h1>Welcome back</h1><p>Sign in to manage projects and deployments.</p><form><div class="field"><label>Email</label><input name="email" type="email" autocomplete="email" required></div><div class="field"><label>Password</label><input name="password" type="password" autocomplete="current-password" required></div><button>Sign in</button><div class="error"></div></form>';bind(app.querySelector('form'),'/api/v1/auth/login')}
function dashboard(u){app.innerHTML='<h1>System ready</h1><p>The authentication foundation is running. Project and deployment management are next.</p><div class="meta"><span>'+esc(u.username)+' · '+esc(u.email)+'</span><span class="pill">'+esc(u.role)+'</span></div><button id="logout">Sign out</button><div class="error"></div>';app.querySelector('#logout').onclick=async()=>{await api('/api/v1/auth/logout',{method:'POST',body:'{}'});await boot()}}
async function boot(){try{const st=await api('/api/v1/status');if(!st.initialized)return setup();try{return dashboard(await api('/api/v1/auth/me'))}catch{return login()}}catch(e){app.innerHTML='<h1>Unable to start</h1><p class="error">'+esc(e.message)+'</p>'}}
boot();
</script></body></html>`
