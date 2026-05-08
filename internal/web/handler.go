package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/zmiishe/synamcps/internal/access"
	"github.com/zmiishe/synamcps/internal/config"
	"github.com/zmiishe/synamcps/internal/models"
	"github.com/zmiishe/synamcps/internal/session"
)

type Capabilities struct {
	Transports []string `json:"transports"`
	Auth       []string `json:"auth"`
}

func NewHandler(cfg config.Config, sessions *session.Store, accessService *access.Service) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/capabilities", func(w http.ResponseWriter, r *http.Request) {
		caps := Capabilities{
			Transports: []string{"streamable_http"},
			Auth:       []string{},
		}
		if cfg.Transport.LegacySSE {
			caps.Transports = append(caps.Transports, "legacy_sse")
		}
		for _, p := range cfg.OAuth.Providers {
			caps.Auth = append(caps.Auth, p.Name)
		}
		if cfg.Teleport.Enabled {
			caps.Auth = append(caps.Auth, "teleport_proxy")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(caps)
	})

	mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir("web/assets"))))
	mux.HandleFunc("/login", loginHandler(cfg, sessions, accessService))
	mux.HandleFunc("/logout", logoutHandler())
	mux.HandleFunc("/app", appHandler(sessions))
	mux.HandleFunc("/app/mcp-connect", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(mcpConnectHTML))
	})
	return mux
}

func loginHandler(cfg config.Config, sessions *session.Store, accessService *access.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(loginHTML))
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		username := strings.TrimSpace(r.FormValue("username"))
		password := r.FormValue("password")
		expectedPassword := cfg.DefaultAdminPassword()
		if cfg.Web.Admin.Enabled && expectedPassword != "" && username == cfg.Web.Admin.Username && password == expectedPassword {
			principal := models.Principal{
				UserID:     "default-admin",
				Email:      cfg.Web.Admin.Username,
				SubjectKey: "user:internal:default-admin",
				Scopes:     []string{"platform_admin", "admin"},
				AuthSource: "internal",
			}
			createWebLogin(w, r, sessions, principal, cfg.Web.Admin.SessionTTLHours)
			return
		}

		if accessService != nil {
			user, ok, err := accessService.Store().AuthenticateUser(r.Context(), username, password)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if ok {
				principal := models.Principal{
					UserID:     user.ExternalSubject,
					Email:      user.Email,
					SubjectKey: user.SubjectKey,
					AuthSource: user.Source,
				}
				if user.SubjectKey == "user:internal:default-admin" {
					principal.Scopes = []string{"platform_admin", "admin"}
				}
				createWebLogin(w, r, sessions, principal, cfg.Web.Admin.SessionTTLHours)
				return
			}
		}
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
	}
}

func createWebLogin(w http.ResponseWriter, r *http.Request, sessions *session.Store, principal models.Principal, ttlHours int) {
	if ttlHours <= 0 {
		ttlHours = 12
	}
	ws := sessions.CreateWebSession(principal, time.Duration(ttlHours)*time.Hour)
	http.SetCookie(w, &http.Cookie{
		Name:     "session_id",
		Value:    ws.SessionID,
		Path:     "/",
		Expires:  ws.ExpiresAt,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/app", http.StatusSeeOther)
}

func logoutHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{
			Name:     "session_id",
			Value:    "",
			Path:     "/",
			Expires:  time.Unix(0, 0),
			MaxAge:   -1,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	}
}

func appHandler(sessions *session.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("session_id")
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		ws, ok := sessions.GetWebSession(cookie.Value)
		if !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		html := strings.ReplaceAll(baseHTML, "__CSRF_TOKEN__", ws.CSRFToken)
		_, _ = w.Write([]byte(html))
	}
}

const loginHTML = `<!doctype html><html><head><title>Synamcps Login</title>
<style>
body{font-family:system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;margin:0;min-height:100vh;display:grid;place-items:center;background:#0f172a;color:#e2e8f0}
form{border:1px solid #334155;border-radius:14px;background:#000000;padding:28px;min-width:320px}
input,button{box-sizing:border-box;width:100%;margin:8px 0;padding:10px;border-radius:8px;border:1px solid #334155;background:#020617;color:#e5e7eb}
button{cursor:pointer;background:#2563eb;border-color:#2563eb}
.login-logo{display:block;margin:0 auto 18px;max-width:220px;height:auto}
</style></head><body>
<form method="post" action="/login">
<img class="login-logo" src="/assets/synamcp-logo-medium.png" alt="Synamcps"/>
<h1>Synamcps Admin</h1>
<label>Username<input name="username" autocomplete="username" autofocus></label>
<label>Password<input name="password" type="password" autocomplete="current-password"></label>
<button type="submit">Log in</button>
</form>
</body></html>`

const baseHTML = `<!doctype html><html><head><title>Synamcps Admin</title>
<style>
body{font-family:system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;margin:0;background:#0f172a;color:#e2e8f0}
input,select,textarea,button{margin:4px;padding:8px;border-radius:6px;border:1px solid #334155;background:#111827;color:#e5e7eb}
button{cursor:pointer;background:#2563eb;border-color:#2563eb}
nav{display:flex;flex-direction:column;gap:8px;margin-top:18px}
nav button{background:#1e293b;border-color:#334155;text-align:left;width:100%}
section{display:none;margin-top:18px}.active{display:block}
table{border-collapse:collapse;width:100%;margin-top:12px}td,th{border-bottom:1px solid #334155;padding:8px;text-align:left}
pre{background:#020617;padding:12px;border-radius:8px;overflow:auto}
.card{border:1px solid #334155;border-radius:10px;padding:14px;margin:10px 0;background:#111827}
.layout{display:grid;grid-template-columns:146px 1fr;min-height:100vh}
.sidebar{background:#020617;border-right:1px solid #334155;padding:18px;position:sticky;top:0;height:100vh;box-sizing:border-box}
.sidebar-logo{display:block;width:110px;height:110px;object-fit:contain;margin-bottom:16px}
.sidebar-links{position:fixed;top:18px;right:28px;display:flex;gap:14px;align-items:center;z-index:20}
.sidebar a{color:#93c5fd;text-decoration:none}
.main{padding:28px;overflow:auto}
.page-title{margin-top:0}
</style></head><body>
<div class="layout">
<aside class="sidebar">
<img class="sidebar-logo" src="/assets/synamcp-logo-small.png" alt="Synamcps"/>
<div class="sidebar-links">
<a id="currentUserLink" href="#" onclick="editOwnUser();return false">Loading user...</a>
<a href="/logout">Log out</a>
</div>
<nav>
<button onclick="showTab('dashboard')">Dashboard</button>
<button onclick="showTab('status')">Status</button>
<button onclick="showTab('search')">Search</button>
<button onclick="showTab('users')">Users</button>
<button onclick="showTab('groups')">Groups</button>
<button onclick="showTab('storages')">Storages</button>
<button onclick="showTab('storage')">Storage</button>
<button onclick="showTab('addItem')">Add item</button>
<button onclick="showTab('tokens')">Tokens</button>
<button onclick="showTab('usage')">Usage</button>
<button onclick="showTab('connect')">MCP Connect</button>
</nav>
</aside>
<main class="main">
<h1 class="page-title">Synamcps Admin</h1>
<label>Bearer token <input id="token" style="width:520px" placeholder="JWT or Synamcps token"/></label>

<section id="dashboard" class="active"><h2>Dashboard</h2><button onclick="loadAll()">Refresh</button><div id="dashboardOut" class="card"></div></section>

<section id="status"><h2>Status</h2>
<button onclick="loadStatus()">Refresh status</button>
<div id="statusOut" class="card">Loading...</div>
</section>

<section id="search"><h2>Search</h2>
<div class="card">
  <div style="display:flex;gap:10px;flex-wrap:wrap">
    <button onclick="showSearchTab('byToken')">Search by token</button>
    <button onclick="showSearchTab('byStorage')">Search by storage</button>
  </div>
  <div id="searchByToken" style="margin-top:12px">
    <div class="muted">Uses the selected token for Authorization (paste raw token).</div>
    <label>Token
      <select id="searchTokenId" style="width:440px"></select>
    </label>
    <button onclick="refreshSearchTokens()">Refresh tokens</button><br/>
    <label>Raw token <input id="searchRawToken" placeholder="raw token (required)" style="width:520px"/></label><br/>
    <label>Query <input id="searchTokenQuery" placeholder="search query" style="width:520px"/></label>
    <label style="margin-left:10px">TopK <input id="searchTokenTopK" value="10" size="4"/></label>
    <button onclick="runSearchByToken()">Search</button>
  </div>
  <div id="searchByStorage" style="margin-top:12px;display:none">
    <div class="muted">Uses the Bearer token from the top bar, and limits search to selected storage.</div>
    <label>Storage
      <select id="searchStorageId" style="width:440px"></select>
    </label>
    <button onclick="refreshSearchStorages()">Refresh storages</button><br/>
    <label>Query <input id="searchStorageQuery" placeholder="search query" style="width:520px"/></label>
    <label style="margin-left:10px">TopK <input id="searchStorageTopK" value="10" size="4"/></label>
    <button onclick="runSearchByStorage()">Search</button>
  </div>
  <div style="margin-top:12px">
    <div><b>Results</b></div>
    <pre id="searchOut" style="margin-top:8px"></pre>
  </div>
</div>
</section>

<section id="users"><h2>Users</h2>
<input id="userEmail" placeholder="email"/>
<input id="userDisplayName" placeholder="display name"/>
<input id="userExternalSubject" placeholder="user id / external subject"/>
<select id="userSource"><option value="internal">internal</option><option value="google">google</option><option value="keycloak">keycloak</option><option value="teleport">teleport</option><option value="oidc">oidc</option></select>
<button onclick="createUser()">Create user</button>
<button onclick="loadUsers()">Refresh users</button>
<div id="usersOut"></div></section>

<section id="editUser"><h2>Edit User</h2>
<input id="editUserId" type="hidden"/>
<label>Email <input id="editUserEmail" placeholder="email"/></label>
<label>Display name <input id="editUserDisplayName" placeholder="display name"/></label>
<label>Status <select id="editUserStatus"><option value="active">active</option><option value="disabled">disabled</option></select></label>
<button onclick="saveUserEdit()">Save user</button>
<button onclick="showTab('users')">Back to users</button>
<div class="card">
<b>Change password</b><br/>
<input id="editCurrentPassword" type="password" placeholder="current password (required for own existing password)"/>
<input id="editNewPassword" type="password" placeholder="new password, min 8 chars"/>
<button onclick="changeUserPassword()">Change password</button>
</div>
<pre id="editUserOut"></pre>
</section>

<section id="groups"><h2>Groups</h2>
<input id="groupName" placeholder="group name"/><button onclick="createGroup()">Create group</button>
<div class="card">
<b>Group members</b><br/>
<input id="memberGroupId" placeholder="group id"/>
<input id="memberUserId" placeholder="user id"/>
<button onclick="loadGroupMembers()">Load members</button>
<button onclick="addGroupMember()">Add user</button>
<button onclick="removeGroupMember()">Remove user</button>
<div id="groupMembersOut"></div>
</div>
<div id="groupsOut"></div></section>

<section id="storages"><h2>Storages</h2>
<input id="storageSlug" placeholder="slug"/><input id="storageName" placeholder="name"/>
<select id="storageVisibility"><option>personal</option><option>group</option><option>public</option></select>
<button onclick="createStorage()">Create storage</button>
<div id="storagesOut"></div></section>

<section id="storage"><h2>Storage</h2>
<div class="card">
<b>Selected storage</b><br/>
<input id="currentStorageId" placeholder="storage id" style="width:420px"/>
<button onclick="loadStorageDetails()">Load</button>
<label style="margin-left:10px">Page <input id="storageItemsPage" value="1" size="4"/></label>
<label>Page size <input id="storageItemsPageSize" value="20" size="4"/></label>
<button onclick="prevStoragePage()">Prev</button>
<button onclick="nextStoragePage()">Next</button>
</div>
<div class="card"><b>Storage info</b><pre id="storageInfoOut"></pre></div>
<div class="card"><b>Access (users / groups)</b><pre id="storageAclOut"></pre></div>
<div class="card"><b>Keys (tokens)</b><pre id="storageTokensOut"></pre></div>
<div class="card"><b>Items</b><pre id="storageItemsOut"></pre></div>
</section>

<section id="addItem"><h2>Add item</h2>
<div class="card">
<label>Storage
  <select id="addStorageId" style="width:440px"></select>
</label>
<button onclick="refreshAddStorages()">Refresh storages</button><br/>
<label>Title <input id="addTitle" placeholder="title" style="width:520px"/></label><br/>
<label>Type
  <select id="addType" onchange="renderAddType()">
    <option value="text">text</option>
    <option value="file">file</option>
    <option value="link">link</option>
  </select>
</label>
<label style="margin-left:10px">Visibility
  <select id="addVisibility">
    <option value="personal">personal</option>
    <option value="group">group</option>
    <option value="public">public</option>
  </select>
</label>
<div id="addTypeBox" style="margin-top:10px"></div>
<button onclick="submitAddItem()">Add</button>
<pre id="addItemOut"></pre>
</div>
</section>

<section id="tokens"><h2>Tokens</h2>
<input id="tokenName" placeholder="token name"/><input id="tokenStorageIds" placeholder="storage ids, comma-separated"/>
<select id="tokenMode"><option value="read">read</option><option value="read_write">read_write</option></select>
<input id="tokenRpm" placeholder="rpm" size="5"/><input id="tokenRph" placeholder="rph" size="5"/><input id="tokenRpd" placeholder="rpd" size="5"/>
<button onclick="createToken()">Create token</button>
<div id="rawTokenBox" class="card" style="display:none"><b>Raw token is shown once:</b><pre id="rawToken"></pre></div>
<div id="tokensOut"></div></section>

<section id="usage"><h2>Usage</h2>
<select id="usageGroup"><option value="storage">storage</option><option value="token">token</option><option value="user">user</option><option value="tool">tool</option><option value="status">status</option></select>
<button onclick="loadUsage()">Load usage</button>
<canvas id="usageChart" width="900" height="260" class="card"></canvas>
<pre id="usageOut"></pre></section>

<section id="connect"><h2>MCP Connect</h2>
<input id="connectTokenId" placeholder="token id"/>
<input id="connectRawToken" placeholder="raw token, only available after create/rotate" style="width:420px"/>
<select id="connectClient"><option value="cursor">Cursor</option><option value="claude_desktop">Claude Desktop</option><option value="claude_code">Claude Code</option><option value="continue">Continue</option><option value="cline">Cline</option><option value="generic">Generic MCP</option></select>
<button onclick="connectConfig()">Generate config</button>
<div class="card" style="margin-top:10px">
<div style="display:flex;gap:12px;align-items:center;justify-content:space-between;flex-wrap:wrap">
  <div><b>Config file</b>: <span id="connectFileName" class="muted">—</span></div>
  <button id="connectCopyBtn" onclick="copyConnectBody()" style="display:none">Copy configBody</button>
</div>
<div style="margin-top:10px">
  <b>Instructions</b>
  <ol id="connectInstructions" class="muted" style="margin-top:8px"></ol>
</div>
<div style="margin-top:10px">
  <b>configBody</b> <span class="muted">(click to copy)</span>
  <pre id="connectBody" onclick="copyConnectBody()" style="cursor:pointer"></pre>
  <div id="connectCopyHint" class="muted"></div>
</div>
</div>
<pre id="connectOut" style="display:none"></pre></section>

<pre id="out"></pre>
<script>
window.csrfToken='__CSRF_TOKEN__';
function showTab(id){document.querySelectorAll('section').forEach(s=>s.classList.remove('active'));document.getElementById(id).classList.add('active')}
function headers(){const t=document.getElementById('token').value.trim();const h={'Content-Type':'application/json','X-CSRF-Token':window.csrfToken};if(t)h.Authorization='Bearer '+t;return h;}
async function api(path, opts={}){opts.headers=headers();const r=await fetch(path,opts);const txt=await r.text();try{return JSON.parse(txt)}catch(e){return {status:r.status,body:txt}}}
function table(rows){if(!rows||!rows.length)return '<p>No data</p>';const keys=Object.keys(rows[0]);return '<table><thead><tr>'+keys.map(k=>'<th>'+k+'</th>').join('')+'</tr></thead><tbody>'+rows.map(r=>'<tr>'+keys.map(k=>'<td>'+escapeHtml(JSON.stringify(r[k]??''))+'</td>').join('')+'</tr>').join('')+'</tbody></table>'}
function escapeHtml(s){return s.replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]))}
function actionButton(label, fn){return '<button onclick="'+fn+'">'+label+'</button>'}
function renderRows(rows, actions){if(!rows||!rows.length)return '<p>No data</p>';const keys=Object.keys(rows[0]);return '<table><thead><tr>'+keys.map(k=>'<th>'+k+'</th>').join('')+'<th>actions</th></tr></thead><tbody>'+rows.map(r=>'<tr>'+keys.map(k=>'<td>'+escapeHtml(JSON.stringify(r[k]??''))+'</td>').join('')+'<td>'+actions(r)+'</td></tr>').join('')+'</tbody></table>'}
async function loadUsers(){const data=await api('/api/admin/users');document.getElementById('usersOut').innerHTML=renderRows(data,r=>actionButton('Edit',"editUser('"+r.id+"')")+actionButton('Delete',"deleteUser('"+r.id+"')"))}
async function createUser(){const body={email:userEmail.value,displayName:userDisplayName.value,externalSubject:userExternalSubject.value||userEmail.value,source:userSource.value,status:'active'};await api('/api/admin/users',{method:'POST',body:JSON.stringify(body)});loadUsers()}
async function editOwnUser(){const me=await api('/api/admin/me');if(me.id){openUserEditor(me)}else{editUserOut.textContent=JSON.stringify(me,null,2);showTab('editUser')}}
async function editUser(id){const data=await api('/api/admin/users/'+id);if(data.id)openUserEditor(data);else{editUserOut.textContent=JSON.stringify(data,null,2);showTab('editUser')}}
function openUserEditor(user){editUserId.value=user.id;editUserEmail.value=user.email||'';editUserDisplayName.value=user.displayName||'';editUserStatus.value=user.status||'active';editUserOut.textContent='';showTab('editUser')}
async function saveUserEdit(){const body={email:editUserEmail.value,displayName:editUserDisplayName.value,status:editUserStatus.value};const data=await api('/api/admin/users/'+editUserId.value,{method:'PATCH',body:JSON.stringify(body)});editUserOut.textContent=JSON.stringify(data,null,2);loadUsers()}
async function changeUserPassword(){const body={currentPassword:editCurrentPassword.value,newPassword:editNewPassword.value};const data=await api('/api/admin/users/'+editUserId.value+'/password',{method:'POST',body:JSON.stringify(body)});editUserOut.textContent=JSON.stringify(data,null,2);editCurrentPassword.value='';editNewPassword.value=''}
async function deleteUser(id){if(confirm('Delete user '+id+'?')){await api('/api/admin/users/'+id,{method:'DELETE'});loadUsers()}}
async function loadGroups(){const data=await api('/api/admin/groups');document.getElementById('groupsOut').innerHTML=renderRows(data,r=>actionButton('Members',"selectGroup('"+r.id+"')")+actionButton('Delete',"deleteGroup('"+r.id+"')"))}
async function createGroup(){await api('/api/admin/groups',{method:'POST',body:JSON.stringify({name:document.getElementById('groupName').value})});loadGroups()}
function selectGroup(id){memberGroupId.value=id;loadGroupMembers()}
async function deleteGroup(id){if(confirm('Delete group '+id+'?')){await api('/api/admin/groups/'+id,{method:'DELETE'});loadGroups();groupMembersOut.innerHTML=''}}
async function loadGroupMembers(){if(!memberGroupId.value)return;const data=await api('/api/admin/groups/'+memberGroupId.value+'/members');document.getElementById('groupMembersOut').innerHTML=renderRows(data,r=>actionButton('Remove',"removeGroupMemberById('"+r.groupId+"','"+r.userId+"')"))}
async function addGroupMember(){await api('/api/admin/groups/'+memberGroupId.value+'/members/'+memberUserId.value,{method:'PUT'});loadGroupMembers()}
async function removeGroupMember(){await removeGroupMemberById(memberGroupId.value,memberUserId.value)}
async function removeGroupMemberById(groupId,userId){await api('/api/admin/groups/'+groupId+'/members/'+userId,{method:'DELETE'});loadGroupMembers()}
function openStorage(id){currentStorageId.value=id;storageItemsPage.value='1';showTab('storage');loadStorageDetails()}
function fillStorageSelect(){
  const sel=document.getElementById('addStorageId');
  if(!sel) return;
  const current=sel.value;
  sel.innerHTML='';
  const opt0=document.createElement('option');
  opt0.value='';
  opt0.textContent='(auto / personal default)';
  sel.appendChild(opt0);
  const list=Array.isArray(window.storages)?window.storages:[];
  list.forEach(s=>{
    const o=document.createElement('option');
    o.value=s.id;
    o.textContent=(s.name||s.id)+' ('+s.id+')';
    sel.appendChild(o);
  });
  if(current) sel.value=current;
}

function fillSearchStorageSelect(){
  const sel=document.getElementById('searchStorageId');
  if(!sel) return;
  const current=sel.value;
  sel.innerHTML='';
  const list=Array.isArray(window.storages)?window.storages:[];
  list.forEach(s=>{
    const o=document.createElement('option');
    o.value=s.id;
    o.textContent=(s.name||s.id)+' ('+s.id+')';
    sel.appendChild(o);
  });
  if(current) sel.value=current;
}

function fillSearchTokenSelect(){
  const sel=document.getElementById('searchTokenId');
  if(!sel) return;
  const current=sel.value;
  sel.innerHTML='';
  const list=Array.isArray(window.tokens)?window.tokens:[];
  list.forEach(t=>{
    const o=document.createElement('option');
    o.value=t.id;
    o.textContent=(t.name||t.id)+' ('+t.id+')';
    sel.appendChild(o);
  });
  if(current) sel.value=current;
}

async function refreshAddStorages(){await loadStorages();fillStorageSelect();}
async function loadStorages(){const data=await api('/api/admin/storages');window.storages=data;document.getElementById('storagesOut').innerHTML=renderRows(data,r=>actionButton('Open',"openStorage('"+r.id+"')")+actionButton('Delete',"deleteStorage('"+r.id+"')"));fillStorageSelect();fillSearchStorageSelect()}
async function createStorage(){await api('/api/admin/storages',{method:'POST',body:JSON.stringify({slug:storageSlug.value,name:storageName.value,visibility:storageVisibility.value})});loadStorages()}
async function deleteStorage(id){if(confirm('Delete storage '+id+'?')){await api('/api/admin/storages/'+id,{method:'DELETE'});loadStorages()}}
async function loadTokens(){const data=await api('/api/admin/tokens');window.tokens=data;document.getElementById('tokensOut').innerHTML=renderRows(data,r=>actionButton('Connect',"connectTokenId.value='"+r.id+"';showTab('connect')")+actionButton('Delete',"deleteToken('"+r.id+"')"));fillSearchTokenSelect()}
async function createToken(){const storageIds=tokenStorageIds.value.split(',').map(s=>s.trim()).filter(Boolean);const body={name:tokenName.value,mode:tokenMode.value,storageIds,rateLimit:{enabled:true,requestsPerMinute:Number(tokenRpm.value||0),requestsPerHour:Number(tokenRph.value||0),requestsPerDay:Number(tokenRpd.value||0)}};const data=await api('/api/admin/tokens',{method:'POST',body:JSON.stringify(body)});if(data.rawToken){rawTokenBox.style.display='block';rawToken.textContent=data.rawToken;connectRawToken.value=data.rawToken;if(data.token)connectTokenId.value=data.token.id}loadTokens()}
async function deleteToken(id){if(confirm('Delete token '+id+'?')){await api('/api/admin/tokens/'+id,{method:'DELETE'});loadTokens()}}
let lastConnectBody='';
async function connectConfig(){
  connectCopyHint.textContent='';
  connectFileName.textContent='—';
  connectInstructions.innerHTML='';
  connectBody.textContent='Loading...';
  connectCopyBtn.style.display='none';
  lastConnectBody='';
  const id=connectTokenId.value;
  const res=await fetch('/api/admin/tokens/'+encodeURIComponent(id)+'/connect-options',{method:'POST',headers:headers(),body:JSON.stringify({client:connectClient.value,rawToken:connectRawToken.value})});
  const txt=await res.text();
  let data=null; try{data=JSON.parse(txt)}catch(e){}
  if(!data){
    connectBody.textContent=txt;
    return;
  }
  if(!res.ok){
    connectBody.textContent=JSON.stringify(data,null,2);
    return;
  }
  connectFileName.textContent=data.configFileName||'';
  (data.instructions||[]).forEach(s=>{
    const li=document.createElement('li');
    li.textContent=s;
    connectInstructions.appendChild(li);
  });
  lastConnectBody=String(data.configBody||'');
  connectBody.textContent=lastConnectBody;
  connectCopyBtn.style.display=lastConnectBody?'inline-block':'none';
}
async function copyConnectBody(){
  if(!lastConnectBody){return;}
  try{
    if(navigator.clipboard && navigator.clipboard.writeText){
      await navigator.clipboard.writeText(lastConnectBody);
    }else{
      const ta=document.createElement('textarea');
      ta.value=lastConnectBody;
      document.body.appendChild(ta);
      ta.select();
      document.execCommand('copy');
      ta.remove();
    }
    connectCopyHint.textContent='Copied to clipboard.';
    setTimeout(()=>{connectCopyHint.textContent='';},1500);
  }catch(e){
    connectCopyHint.textContent='Copy failed. Select text manually.';
  }
}
async function loadUsage(){const data=await api('/api/admin/usage/series?groupBy='+usageGroup.value);usageOut.textContent=JSON.stringify(data,null,2);drawUsage(data)}
function drawUsage(series){const c=usageChart,ctx=c.getContext('2d');ctx.clearRect(0,0,c.width,c.height);ctx.fillStyle='#e2e8f0';ctx.fillText('Usage by '+usageGroup.value,20,24);const vals=(series||[]).map(s=>s.points?.[0]?.value||0);const max=Math.max(1,...vals);(series||[]).forEach((s,i)=>{const v=s.points?.[0]?.value||0;const x=20+i*80;const h=Math.round((v/max)*180);ctx.fillStyle='#38bdf8';ctx.fillRect(x,230-h,48,h);ctx.fillStyle='#e2e8f0';ctx.fillText(String(v),x,224-h);ctx.fillText(Object.values(s.labels||{})[0]||'',x,250)})}
async function loadCurrentUser(){const me=await api('/api/admin/me');if(me&&me.id){currentUserLink.textContent=me.email||me.displayName||me.externalSubject||me.id}}
async function loadStatus(){
  const res=await fetch('/api/admin/status',{headers:headers()});
  const txt=await res.text();
  let data=null; try{data=JSON.parse(txt)}catch(e){}
  if(!data){statusOut.textContent=txt;return;}
  const rows=(data.components||[]).map(c=>{
    const badge='<span style="display:inline-block;width:10px;height:10px;border-radius:50%;background:'+(
      c.color==='green'?'#22c55e':(c.color==='yellow'?'#eab308':'#ef4444')
    )+';margin-right:8px"></span>';
    const title=(c.kind==='llm'?('LLM '+c.role+' · '+(c.model||'')):(c.name));
    const msg=c.available?'ok':(c.message||'not available');
    return {component:badge+escapeHtml(title),available:String(!!c.available),errors:String(c.errorCount||0),message:escapeHtml(msg)};
  });
  statusOut.innerHTML=renderRows(rows,_=>'');
}
async function loadAll(){await Promise.all([loadCurrentUser(),loadStatus(),loadUsers(),loadGroups(),loadStorages(),loadTokens(),loadUsage()]);dashboardOut.textContent='Loaded status, users, groups, storages, tokens and usage.'}
loadAll();

function showSearchTab(which){
  const a=document.getElementById('searchByToken');
  const b=document.getElementById('searchByStorage');
  if(which==='byStorage'){a.style.display='none';b.style.display='block';}
  else {a.style.display='block';b.style.display='none';}
}
async function refreshSearchTokens(){const data=await api('/api/admin/tokens');window.tokens=data;fillSearchTokenSelect();}
async function refreshSearchStorages(){await loadStorages();fillSearchStorageSelect();}

async function runSearchByToken(){
  searchOut.textContent='Loading...';
  const raw=(searchRawToken.value||'').trim();
  const q=(searchTokenQuery.value||'').trim();
  const topK=Math.max(1,Number(searchTokenTopK.value||'10'));
  if(!raw){searchOut.textContent='Raw token is required for token search.';return;}
  const h=headers();
  h.Authorization='Bearer '+raw;
  const body={query:q,topK,filters:{}};
  const res=await fetch('/api/knowledge/search',{method:'POST',headers:h,body:JSON.stringify(body)});
  const txt=await res.text();
  try{searchOut.textContent=JSON.stringify(JSON.parse(txt),null,2)}catch(e){searchOut.textContent=txt}
}

async function runSearchByStorage(){
  searchOut.textContent='Loading...';
  const storageId=(searchStorageId.value||'').trim();
  const q=(searchStorageQuery.value||'').trim();
  const topK=Math.max(1,Number(searchStorageTopK.value||'10'));
  if(!storageId){searchOut.textContent='Select storage.';return;}
  const body={query:q,topK,filters:{storageId}};
  const res=await fetch('/api/knowledge/search',{method:'POST',headers:headers(),body:JSON.stringify(body)});
  const txt=await res.text();
  try{searchOut.textContent=JSON.stringify(JSON.parse(txt),null,2)}catch(e){searchOut.textContent=txt}
}

function storagePageParams(){const p=Math.max(1,Number(storageItemsPage.value||'1'));const ps=Math.max(1,Math.min(200,Number(storageItemsPageSize.value||'20')));return {page:p,pageSize:ps}}
function prevStoragePage(){storageItemsPage.value=String(Math.max(1,Number(storageItemsPage.value||'1')-1));loadStorageDetails()}
function nextStoragePage(){storageItemsPage.value=String(Number(storageItemsPage.value||'1')+1);loadStorageDetails()}
async function loadStorageDetails(){
  const id=(currentStorageId.value||'').trim();
  if(!id){storageInfoOut.textContent='';storageAclOut.textContent='';storageTokensOut.textContent='';storageItemsOut.textContent='';return;}
  storageInfoOut.textContent='Loading...';storageAclOut.textContent='Loading...';storageTokensOut.textContent='Loading...';storageItemsOut.textContent='Loading...';

  const details=await api('/api/admin/storages/'+encodeURIComponent(id));
  if(details && details.storage){
    storageInfoOut.textContent=JSON.stringify(details.storage,null,2);
    storageAclOut.textContent=JSON.stringify(details.acl||[],null,2);
    storageTokensOut.textContent=JSON.stringify(details.tokens||[],null,2);
  }else{
    storageInfoOut.textContent=JSON.stringify(details,null,2);
    storageAclOut.textContent='';
    storageTokensOut.textContent='';
    storageItemsOut.textContent='';
    return;
  }

  const {page,pageSize}=storagePageParams();
  const q=new URLSearchParams({storageId:id,page:String(page),pageSize:String(pageSize)});
  const items=await api('/api/knowledge?'+q.toString());
  storageItemsOut.textContent=JSON.stringify(items,null,2);
}

function renderAddType(){
  const box=document.getElementById('addTypeBox');
  const t=document.getElementById('addType').value;
  if(t==='text'){
    box.innerHTML='<label>Text<br/><textarea id="addText" rows="10" style="width:100%"></textarea></label>';
  }else if(t==='file'){
    box.innerHTML='<label>File <input id="addFile" type="file"/></label>';
  }else{
    box.innerHTML='<label>Link URL <input id="addLink" placeholder="https://..." style="width:520px"/></label>';
  }
}
renderAddType();

async function submitAddItem(){
  addItemOut.textContent='Loading...';
  const storageId=(addStorageId.value||'').trim();
  const title=(addTitle.value||'').trim();
  const visibility=addVisibility.value;
  const t=addType.value;
  if(t==='text'){
    const text=(document.getElementById('addText').value||'');
    const body={storageId,title,text,mimeType:'text/plain',visibility,groupIds:[],source:'admin'};
    const res=await fetch('/api/knowledge',{method:'POST',headers:headers(),body:JSON.stringify(body)});
    const txt=await res.text(); try{addItemOut.textContent=JSON.stringify(JSON.parse(txt),null,2)}catch(e){addItemOut.textContent=txt}
    return;
  }
  if(t==='file'){
    const f=document.getElementById('addFile').files?.[0];
    if(!f){addItemOut.textContent='Select file';return;}
    const fd=new FormData();
    fd.append('storageId',storageId);
    fd.append('title',title);
    fd.append('visibility',visibility);
    fd.append('source','admin');
    fd.append('file',f,f.name);
    const h=headers(); delete h['Content-Type'];
    const res=await fetch('/api/knowledge/ingest/file',{method:'POST',headers:h,body:fd});
    const txt=await res.text(); try{addItemOut.textContent=JSON.stringify(JSON.parse(txt),null,2)}catch(e){addItemOut.textContent=txt}
    return;
  }
  const link=(document.getElementById('addLink').value||'').trim();
  if(!link){addItemOut.textContent='Enter link URL';return;}
  const body={storageId,title,url:link,visibility,source:'admin'};
  const res=await fetch('/api/knowledge/ingest/link',{method:'POST',headers:headers(),body:JSON.stringify(body)});
  const txt=await res.text(); try{addItemOut.textContent=JSON.stringify(JSON.parse(txt),null,2)}catch(e){addItemOut.textContent=txt}
}
</script>
</main>
</div>
</body></html>`

const mcpConnectHTML = `<!doctype html><html><head><title>MCP Connect</title>
<style>
body{font-family:system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;margin:0;color:#111827}
.layout{display:grid;grid-template-columns:146px 1fr;min-height:100vh}
.sidebar{background:#f8fafc;border-right:1px solid #cbd5e1;padding:18px;box-sizing:border-box}
.sidebar-logo{display:block;width:110px;height:110px;object-fit:contain;margin-bottom:16px}
.sidebar a{display:block;margin:10px 0;color:#2563eb}
.main{padding:28px}
</style></head><body>
<div class="layout">
<aside class="sidebar">
<img class="sidebar-logo" src="/assets/synamcp-logo-small.png" alt="Synamcps"/>
<a href="/app">Admin</a>
<a href="/logout">Log out</a>
</aside>
<main class="main">
<h1>MCP Connection Guide</h1>
<div id="caps">loading...</div>
<script>
fetch('/api/capabilities').then(r=>r.json()).then(c=>{
  const root = document.getElementById('caps');
  root.innerHTML = '';
  const t = document.createElement('h2');
  t.textContent = 'Available transports: '+c.transports.join(', ');
  root.appendChild(t);
  const ul = document.createElement('ul');
  c.auth.forEach(a=>{
    const li = document.createElement('li');
    li.innerHTML = '<b>'+a+'</b>: use /mcp endpoint, get bearer token from '+a+' and send initialize JSON-RPC request.';
    ul.appendChild(li);
  });
  root.appendChild(ul);
});
</script>
</main>
</div>
</body></html>`
