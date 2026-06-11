function showTab(id){document.querySelectorAll('section').forEach(s=>s.classList.remove('active'));document.getElementById(id).classList.add('active')}
function headers(){const t=document.getElementById('token').value.trim();const h={'Content-Type':'application/json','X-CSRF-Token':window.csrfToken};if(t)h.Authorization='Bearer '+t;return h;}
async function api(path, opts={}){opts.headers=headers();const r=await fetch(path,opts);const txt=await r.text();try{return JSON.parse(txt)}catch(e){return {status:r.status,body:txt}}}
function table(rows){if(!rows||!rows.length)return '<p>No data</p>';const keys=Object.keys(rows[0]);return '<table><thead><tr>'+keys.map(k=>'<th>'+k+'</th>').join('')+'</tr></thead><tbody>'+rows.map(r=>'<tr>'+keys.map(k=>'<td>'+escapeHtml(JSON.stringify(r[k]??''))+'</td>').join('')+'</tr>').join('')+'</tbody></table>'}
function escapeHtml(s){return s.replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]))}
function actionButton(label, fn){return '<button onclick="'+fn+'">'+label+'</button>'}
function renderRows(rows, actions){if(!rows||!rows.length)return '<p>No data</p>';const keys=Object.keys(rows[0]);return '<table><thead><tr>'+keys.map(k=>'<th>'+k+'</th>').join('')+'<th>actions</th></tr></thead><tbody>'+rows.map(r=>'<tr>'+keys.map(k=>'<td>'+escapeHtml(JSON.stringify(r[k]??''))+'</td>').join('')+'<td>'+actions(r)+'</td></tr>').join('')+'</tbody></table>'}
async function loadUsers(){const data=await api('/api/admin/users');window.users=Array.isArray(data)?data:[];document.getElementById('usersOut').innerHTML=renderRows(window.users,r=>actionButton('Edit',"editUser('"+r.id+"')")+actionButton('Delete',"deleteUser('"+r.id+"')"));fillUserSelect()}
async function createUser(){const body={email:userEmail.value,displayName:userDisplayName.value,externalSubject:userExternalSubject.value||userEmail.value,source:userSource.value,status:'active'};await api('/api/admin/users',{method:'POST',body:JSON.stringify(body)});loadUsers()}
async function editOwnUser(){const me=await api('/api/admin/me');if(me.id){openUserEditor(me)}else{editUserOut.textContent=JSON.stringify(me,null,2);showTab('editUser')}}
async function editUser(id){const data=await api('/api/admin/users/'+id);if(data.id)openUserEditor(data);else{editUserOut.textContent=JSON.stringify(data,null,2);showTab('editUser')}}
function openUserEditor(user){editUserId.value=user.id;editUserEmail.value=user.email||'';editUserDisplayName.value=user.displayName||'';editUserStatus.value=user.status||'active';editUserOut.textContent='';showTab('editUser')}
async function saveUserEdit(){const body={email:editUserEmail.value,displayName:editUserDisplayName.value,status:editUserStatus.value};const data=await api('/api/admin/users/'+editUserId.value,{method:'PATCH',body:JSON.stringify(body)});editUserOut.textContent=JSON.stringify(data,null,2);loadUsers()}
async function changeUserPassword(){const body={currentPassword:editCurrentPassword.value,newPassword:editNewPassword.value};const data=await api('/api/admin/users/'+editUserId.value+'/password',{method:'POST',body:JSON.stringify(body)});editUserOut.textContent=JSON.stringify(data,null,2);editCurrentPassword.value='';editNewPassword.value=''}
async function deleteUser(id){if(confirm('Delete user '+id+'?')){await api('/api/admin/users/'+id,{method:'DELETE'});loadUsers()}}
async function loadGroups(){const data=await api('/api/admin/groups');window.groups=Array.isArray(data)?data:[];document.getElementById('groupsOut').innerHTML=renderRows(window.groups,r=>actionButton('Members',"selectGroup('"+r.id+"')")+actionButton('Delete',"deleteGroup('"+r.id+"')"));fillGroupSelect()}
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

// fillSelectGeneric (re)populates a <select> with id-valued options labelled by
// name, preserving the current selection. Supports an empty placeholder and
// multi-select. Used so admins pick entities by name instead of typing ids.
function fillSelectGeneric(selectId, items, label, opts){
  const sel=document.getElementById(selectId);
  if(!sel) return;
  opts=opts||{};
  const prev=opts.multi?Array.from(sel.selectedOptions).map(o=>o.value):sel.value;
  sel.innerHTML='';
  if(opts.placeholder){
    const o=document.createElement('option');o.value='';o.textContent=opts.placeholder;sel.appendChild(o);
  }
  (Array.isArray(items)?items:[]).forEach(it=>{
    const o=document.createElement('option');
    o.value=it.id;
    o.textContent=label(it);
    sel.appendChild(o);
  });
  if(opts.multi){
    const set=new Set(Array.isArray(prev)?prev:[]);
    Array.from(sel.options).forEach(o=>{if(o.value&&set.has(o.value))o.selected=true;});
  }else if(prev){ sel.value=prev; }
}
function storageLabel(s){return (s.name||s.slug||s.id)+' ('+s.id+')';}
function tokenLabel(t){return (t.name||t.id)+' ('+t.id+')';}
function fillStorageDetailSelect(){fillSelectGeneric('currentStorageId',window.storages,storageLabel,{placeholder:'(select storage)'});}
function fillTokenStorageSelect(){fillSelectGeneric('tokenStorageIds',window.storages,storageLabel,{multi:true});}
function fillTokenSelects(){fillSelectGeneric('tokenMcpScopesTokenId',window.tokens,tokenLabel,{placeholder:'(select token)'});fillSelectGeneric('connectTokenId',window.tokens,tokenLabel,{placeholder:'(select token)'});}
function fillMcpServerSelect(){fillSelectGeneric('currentMcpServerId',window.mcpServers,s=>(s.name||s.slug||s.id)+' ('+s.id+')',{placeholder:'(select server)'});}
function fillGroupSelect(){fillSelectGeneric('memberGroupId',window.groups,g=>(g.name||g.id)+' ('+g.id+')',{placeholder:'(select group)'});}
function fillUserSelect(){fillSelectGeneric('memberUserId',window.users,u=>(u.email||u.displayName||u.externalSubject||u.id)+' ('+u.id+')',{placeholder:'(select user)'});}

async function refreshAddStorages(){await loadStorages();fillStorageSelect();}
async function loadStorages(){const data=await api('/api/admin/storages');window.storages=Array.isArray(data)?data:[];document.getElementById('storagesOut').innerHTML=renderRows(window.storages,r=>actionButton('Open',"openStorage('"+r.id+"')")+actionButton('Delete',"deleteStorage('"+r.id+"')"));fillStorageSelect();fillSearchStorageSelect();fillStorageDetailSelect();fillTokenStorageSelect()}
async function createStorage(){await api('/api/admin/storages',{method:'POST',body:JSON.stringify({name:storageName.value,visibility:storageVisibility.value})});loadStorages()}
async function deleteStorage(id){if(confirm('Delete storage '+id+'?')){await api('/api/admin/storages/'+id,{method:'DELETE'});loadStorages()}}
async function loadTokens(){const data=await api('/api/admin/tokens');window.tokens=Array.isArray(data)?data:[];document.getElementById('tokensOut').innerHTML=renderRows(window.tokens,r=>actionButton('Connect',"connectTokenId.value='"+r.id+"';showTab('connect')")+actionButton('Delete',"deleteToken('"+r.id+"')"));fillSearchTokenSelect();fillTokenSelects()}
async function createToken(){const storageIds=Array.from(document.getElementById('tokenStorageIds').selectedOptions).map(o=>o.value).filter(Boolean);const body={name:tokenName.value,mode:tokenMode.value,storageIds,rateLimit:{enabled:true,requestsPerMinute:Number(tokenRpm.value||0),requestsPerHour:Number(tokenRph.value||0),requestsPerDay:Number(tokenRpd.value||0)}};if(tokenMcpScopesJson.value.trim()){try{body.mcpServers=JSON.parse(tokenMcpScopesJson.value)}catch(e){alert('Invalid MCP scopes JSON');return}}const data=await api('/api/admin/tokens',{method:'POST',body:JSON.stringify(body)});if(data.rawToken){rawTokenBox.style.display='block';rawToken.textContent=data.rawToken;connectRawToken.value=data.rawToken}await loadTokens();if(data&&data.token&&data.token.id)connectTokenId.value=data.token.id}
async function saveTokenMcpScopes(){const id=tokenMcpScopesTokenId.value.trim();if(!id){alert('token id required');return}let mcpServers=[];if(tokenMcpScopesJson.value.trim()){try{mcpServers=JSON.parse(tokenMcpScopesJson.value)}catch(e){alert('Invalid MCP scopes JSON');return}}const data=await api('/api/admin/tokens/'+encodeURIComponent(id)+'/mcp-scopes',{method:'PATCH',body:JSON.stringify({mcpServers})});alert(JSON.stringify(data,null,2))}
async function loadMcpServers(){const data=await api('/api/admin/mcp-servers');window.mcpServers=Array.isArray(data)?data:[];document.getElementById('mcpServersOut').innerHTML=renderRows(window.mcpServers,r=>actionButton('Open',"openMcpServer('"+r.id+"')")+actionButton('Delete',"deleteMcpServer('"+r.id+"')"));fillMcpServerSelect()}
async function createMcpServer(){const body={name:mcpName.value,url:mcpUrl.value,transport:mcpTransport.value,authType:mcpAuthType.value,authHeaderName:mcpAuthHeader.value,authSecret:mcpAuthSecret.value,headersJson:mcpHeadersJson.value||'{}'};const data=await api('/api/admin/mcp-servers',{method:'POST',body:JSON.stringify(body)});mcpServerDetailOut.textContent=JSON.stringify(data,null,2);if(data.id){openMcpServer(data.id)}loadMcpServers()}
function openMcpServer(id){currentMcpServerId.value=id;showTab('mcpServerDetail');loadMcpServerDetails()}
async function deleteMcpServer(id){if(confirm('Delete MCP server '+id+'?')){await api('/api/admin/mcp-servers/'+id,{method:'DELETE'});loadMcpServers()}}
async function loadMcpServerDetails(){const id=(currentMcpServerId.value||'').trim();if(!id)return;const data=await api('/api/admin/mcp-servers/'+encodeURIComponent(id));mcpServerInfoOut.textContent=JSON.stringify(data.server||data,null,2);renderMcpCapabilities(data)}
async function connectTestMcpServer(){const id=(currentMcpServerId.value||'').trim();if(!id)return;mcpCapabilitiesBox.textContent='Connecting...';const data=await api('/api/admin/mcp-servers/'+encodeURIComponent(id)+'/connect-test',{method:'POST',body:'{}'});renderMcpCapabilities(data);mcpServerDetailOut.textContent=JSON.stringify(data,null,2);loadMcpServers()}
function renderMcpCapabilities(data){const box=document.getElementById('mcpCapabilitiesBox');if(!data||!data.tools){box.textContent='No data';return}const slug=(data.server&&data.server.slug)||'';let html='';const section=(title,items,prefix,field,labelFn)=>{html+='<div style="margin-top:10px"><b>'+title+'</b><br/>';(items||[]).forEach(it=>{const val=it[field]||'';const label=labelFn?labelFn(it,slug):val;html+='<label><input type="checkbox" class="mcp-cap" data-kind="'+prefix+'" value="'+escapeHtml(val)+'" '+(it.enabled?'checked':'')+'/> '+escapeHtml(label)+'</label><br/>'});html+='</div>'};section('Tools',data.tools,'tool','toolName',(it,s)=>s+'__'+(it.toolName||''));section('Resources',data.resources,'res','uri',(it,s)=>'syna-mcp/'+s+'/'+(it.uri||''));section('Prompts',data.prompts,'prompt','promptName',(it,s)=>s+'__'+(it.promptName||''));box.innerHTML=html}
async function saveMcpCapabilities(){const id=(currentMcpServerId.value||'').trim();if(!id)return;const enabledTools=[],enabledResources=[],enabledPrompts=[];document.querySelectorAll('.mcp-cap:checked').forEach(el=>{if(el.dataset.kind==='tool')enabledTools.push(el.value);if(el.dataset.kind==='res')enabledResources.push(el.value);if(el.dataset.kind==='prompt')enabledPrompts.push(el.value)});const data=await api('/api/admin/mcp-servers/'+encodeURIComponent(id)+'/capabilities',{method:'PUT',body:JSON.stringify({enabledTools,enabledResources,enabledPrompts})});mcpServerDetailOut.textContent=JSON.stringify(data,null,2);renderMcpCapabilities(data)}
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
async function loadAll(){await Promise.all([loadCurrentUser(),loadStatus(),loadUsers(),loadGroups(),loadStorages(),loadTokens(),loadMcpServers(),loadUsage()]);dashboardOut.textContent='Loaded status, users, groups, storages, tokens, mcp servers and usage.'}
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
