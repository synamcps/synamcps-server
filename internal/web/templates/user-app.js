let storages=[];
let conversations=[];
let tokens=[];
let activeConversation=null;

function headers(){return {'Content-Type':'application/json','X-CSRF-Token':window.csrfToken};}
function escapeHtml(s){return String(s||'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));}
function markdown(s){return escapeHtml(s).replace(/\*\*([^*]+)\*\*/g,'<b>$1</b>').replace(/\[([^\]]+)\]\(([^)]+)\)/g,'<a href="$2">$1</a>').replace(/\n/g,'<br/>');}
async function api(path, opts={}){
  opts.headers=Object.assign(headers(),opts.headers||{});
  const res=await fetch(path,opts);
  const text=await res.text();
  let data=null;try{data=text?JSON.parse(text):null}catch(e){data={body:text};}
  if(!res.ok){throw new Error((data&&data.body)||text||res.statusText);}
  return data;
}

function selectedStorageIds(){
  return Array.from(document.querySelectorAll('.dataset-check:checked')).map(el=>el.value);
}

async function loadMe(){
  try{
    const me=await api('/api/admin/me');
    currentUser.textContent=me.email||me.displayName||me.externalSubject||me.id||'User';
    if((me.subjectKey||'').includes('default-admin')) adminLink.style.display='inline';
  }catch(e){currentUser.textContent='User';}
}

async function loadStorages(){
  try{
    storages=await api('/api/admin/storages');
    datasets.innerHTML=(storages||[]).map(s=>'<label class="dataset"><input class="dataset-check" type="checkbox" value="'+escapeHtml(s.id)+'" checked/> '+escapeHtml(s.name||s.slug||s.id)+' <span class="pill">'+escapeHtml(s.id)+'</span></label>').join('')||'<p>No readable storages.</p>';
  }catch(e){datasets.textContent=e.message;}
}

async function loadConversations(){
  try{
    const data=await api('/api/agent/conversations');
    conversations=data.conversations||[];
    conversations.sort((a,b)=>String(b.updatedAt).localeCompare(String(a.updatedAt)));
    conversationsEl();
  }catch(e){conversations.textContent=e.message;}
}

function conversationsEl(){
  document.getElementById('conversations').innerHTML=(conversations||[]).map(c=>'<div><button class="secondary" onclick="openConversation(\''+c.id+'\')">'+escapeHtml(c.title||c.id)+'</button></div>').join('')||'<p>No conversations.</p>';
}

async function createConversation(){
  const ids=selectedStorageIds();
  if(!ids.length){alert('Select at least one dataset.');return;}
  try{
    activeConversation=await api('/api/agent/conversations',{method:'POST',body:JSON.stringify({title:'Chat '+new Date().toLocaleString(),datasetStorageIds:ids})});
    await loadConversations();
    renderConversationMeta();
    chat.innerHTML='';
    refs.innerHTML='';
  }catch(e){alert(e.message);}
}

async function openConversation(id){
  activeConversation=(conversations||[]).find(c=>c.id===id)||{id};
  renderConversationMeta();
  chat.innerHTML='Loading...';
  try{
    const data=await api('/api/agent/conversations/'+encodeURIComponent(id)+'/messages');
    chat.innerHTML='';
    (data.messages||[]).forEach(renderMessage);
  }catch(e){chat.textContent=e.message;}
}

function renderConversationMeta(){
  if(!activeConversation){conversationMeta.textContent='Choose datasets and start a conversation.';return;}
  conversationMeta.innerHTML='Conversation <span class="pill">'+escapeHtml(activeConversation.id)+'</span> datasets: '+(activeConversation.datasetStorageIds||[]).map(id=>'<span class="pill">'+escapeHtml(id)+'</span>').join(' ');
}

function renderMessage(msg){
  const div=document.createElement('div');
  div.className='msg '+(msg.role==='user'?'user':'assistant');
  div.innerHTML=markdown(msg.content||'');
  chat.appendChild(div);
  chat.scrollTop=chat.scrollHeight;
}

async function sendMessage(){
  if(!activeConversation){await createConversation();if(!activeConversation)return;}
  const content=messageText.value.trim();
  if(!content)return;
  messageText.value='';
  renderMessage({role:'user',content});
  const assistant={role:'assistant',content:'Thinking...'};
  const div=document.createElement('div');
  div.className='msg assistant';
  div.textContent=assistant.content;
  chat.appendChild(div);
  try{
    const res=await fetch('/api/agent/conversations/'+encodeURIComponent(activeConversation.id)+'/messages',{method:'POST',headers:headers(),body:JSON.stringify({content})});
    if(!res.ok){div.textContent=await res.text();return;}
    await readSSE(res, event=>{
      if(event.event==='conversation'){activeConversation=event.data;renderConversationMeta();}
      if(event.event==='documents'){renderRefs(event.data||[]);}
      if(event.event==='saved_memory'){refs.innerHTML='<b>Saved memory:</b> <a href="'+escapeHtml(event.data.uiHref||'#')+'">'+escapeHtml(event.data.title||event.data.docId)+'</a>'+refs.innerHTML;}
      if(event.event==='message'){div.innerHTML=markdown(event.data.content||'');}
    });
  }catch(e){div.textContent=e.message;}
  await loadConversations();
}

async function readSSE(res,onEvent){
  const reader=res.body.getReader();
  const decoder=new TextDecoder();
  let buf='';
  while(true){
    const {value,done}=await reader.read();
    if(done)break;
    buf+=decoder.decode(value,{stream:true});
    let idx;
    while((idx=buf.indexOf('\n\n'))>=0){
      const raw=buf.slice(0,idx);buf=buf.slice(idx+2);
      const event=parseSSE(raw);
      if(event)onEvent(event);
    }
  }
}

function parseSSE(raw){
  let name='message', data='';
  raw.split('\n').forEach(line=>{
    if(line.startsWith('event:'))name=line.slice(6).trim();
    if(line.startsWith('data:'))data+=line.slice(5).trim();
  });
  try{return {event:name,data:JSON.parse(data)}}catch(e){return {event:name,data};}
}

function renderRefs(items){
  refs.innerHTML=(items||[]).map(d=>'<a href="'+escapeHtml(d.uiHref||'#')+'">'+escapeHtml(d.title||d.docId)+' <span class="pill">'+escapeHtml(d.docId)+'</span></a>').join('');
}

async function manualSearch(){
  const ids=selectedStorageIds();
  const query=searchQuery.value.trim();
  if(!ids.length||!query){searchOut.textContent='Select datasets and enter a query.';return;}
  const all=[];
  for(const id of ids){
    try{
      const hits=await api('/api/knowledge/search',{method:'POST',body:JSON.stringify({query,topK:5,filters:{storageId:id}})});
      all.push(...(hits||[]));
    }catch(e){all.push({storageId:id,error:e.message});}
  }
  searchOut.textContent=JSON.stringify(all,null,2);
}

async function loadTokens(){
  try{
    tokens=await api('/api/admin/tokens');
    document.getElementById('tokens').innerHTML=(tokens||[]).map(t=>'<div>'+escapeHtml(t.name||t.id)+' <span class="pill">'+escapeHtml(t.id)+'</span> <button class="secondary" onclick="revokeToken(\''+t.id+'\')">Revoke</button></div>').join('')||'<p>No tokens.</p>';
    connectTokenId.innerHTML=(tokens||[]).map(t=>'<option value="'+escapeHtml(t.id)+'">'+escapeHtml(t.name||t.id)+'</option>').join('');
  }catch(e){document.getElementById('tokens').textContent=e.message;}
}

async function createToken(){
  const ids=selectedStorageIds();
  if(!ids.length){alert('Select datasets first.');return;}
  try{
    const data=await api('/api/admin/tokens',{method:'POST',body:JSON.stringify({name:tokenName.value||'user-token',mode:tokenMode.value,storageIds:ids})});
    const raw=data.rawToken||'';
    if(raw){rawTokenBox.style.display='block';rawToken.textContent=raw;connectRawToken.value=raw;}
    await loadTokens();
    if(data.token&&data.token.id)connectTokenId.value=data.token.id;
  }catch(e){alert(e.message);}
}

async function revokeToken(id){
  if(!confirm('Revoke token '+id+'?'))return;
  try{await api('/api/admin/tokens/'+encodeURIComponent(id)+'/revoke',{method:'POST',body:'{}'});await loadTokens();}catch(e){alert(e.message);}
}

async function connectConfig(){
  const id=connectTokenId.value;
  if(!id){connectOut.textContent='Select a token.';return;}
  try{
    const data=await api('/api/admin/tokens/'+encodeURIComponent(id)+'/connect-options',{method:'POST',body:JSON.stringify({client:'cursor',rawToken:connectRawToken.value})});
    connectOut.textContent=(data.configFileName||'')+'\n\n'+(data.configBody||'')+'\n\n'+(data.instructions||[]).join('\n');
  }catch(e){connectOut.textContent=e.message;}
}

Promise.all([loadMe(),loadStorages(),loadConversations(),loadTokens()]);
