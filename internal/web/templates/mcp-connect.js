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
