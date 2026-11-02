'use strict';
const root=document.querySelector('#app'),esc=v=>String(v??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const TYPES={article:'文章',audio:'音频',video:'视频',carousel:'轮播'};
let user=null,epoch=0,listSeq=0,detailSeq=0,page=1,query='',kind='',detail=null,personal={},feedback={},draft=null,records=[],noticeTimer;
const controllers=new Set();
let player=null,channelID='',channelName='',playQueue=[],queueOffset=0,queueHasMore=false,channelSettings=null,capabilities=null;
const playbackPreferences={mode:'auto'};
let automaticReporting=true;
function discardPlayer(){const current=player;player=null;current?.destroy()}
async function releasePlayer(){const current=player;player=null;if(current){current.pause();let timer;try{await Promise.race([current.flush(),new Promise(resolve=>{timer=setTimeout(()=>{if(user)notify('最后一次播放同步尚未确认，已允许离开；请稍后核对进度。');resolve()},2000)})])}catch(error){if(user)notify(error.message)}finally{clearTimeout(timer);current.destroy()}}}
function resetLearningIdentity(){discardPlayer();channelID='';channelName='';playQueue=[];channelSettings=null;capabilities=null;automaticReporting=true;playbackPreferences.mode='auto';delete playbackPreferences.rangeStart;delete playbackPreferences.rangeEnd}
function listPath(offset=(page-1)*12){const params=new URLSearchParams({limit:12,offset,q:query,kind});return (channelID?'/channels/'+channelID+'/items':'/items')+'?'+params}
function playable(item){return ['audio','video'].includes(item.kind)&&item.assets?.length>0}

function uuid(){const b=new Uint8Array(16);crypto.getRandomValues(b);b[6]=(b[6]&15)|64;b[8]=(b[8]&63)|128;return [...b].map((v,i)=>([4,6,8,10].includes(i)?'-':'')+v.toString(16).padStart(2,'0')).join('')}
function notify(text){clearTimeout(noticeTimer);document.querySelector('#notice').textContent=text;noticeTimer=setTimeout(()=>document.querySelector('#notice').textContent='',5000)}
async function api(path,{method='GET',body}={}){const revision=epoch,c=new AbortController();controllers.add(c);const timer=setTimeout(()=>c.abort(),30000);try{const response=await fetch('/api/v1'+path,{method,credentials:'same-origin',headers:{...(body?{'Content-Type':'application/json'}:{}),...(user?{'X-WzStudy-User':user.id}:{})},body:body?JSON.stringify(body):undefined,signal:c.signal});const json=await response.json();if(revision!==epoch)throw new Error('登录状态已变化，请重新操作。');if(response.status===401&&user)showLogin('用户会话已失效或已切换，请重新登录。');if(!response.ok){const error=new Error(json.error?.message||'请求失败');error.status=response.status;error.code=json.error?.code;throw error;}return json.data}catch(error){if(error.name==='AbortError')throw new Error('请求已取消或超时，请刷新核对保存结果后再重试。');throw error}finally{clearTimeout(timer);controllers.delete(c)}}
function showLogin(message=''){resetLearningIdentity();epoch++;listSeq++;detailSeq++;root.style.visibility='';for(const c of controllers)c.abort();controllers.clear();root.querySelectorAll('video,audio').forEach(m=>m.pause());user=null;detail=null;personal={};feedback={};draft=null;records=[];root.innerHTML=`<main class="login-layout"><section class="login-story"><a class="brand" href="/learn/"><span class="brand-mark">温</span>温知 · 个人学习</a><h1>自己的资料，<br>自己的成长轨迹。</h1><p>登录后学习你录入的资料。收藏、笔记、草稿、进度和复习记录只属于当前用户。</p><div class="login-points"><span>资料按账号隔离</span><span>跨设备继续学习</span><span>管理员只管账号</span></div></section><section class="login-card"><span class="eyebrow">YOUR PERSONAL LEARNING</span><h2>用户登录</h2><p>请使用管理员为你创建的用户账号。</p><form id="login"><label for="email">用户邮箱</label><input id="email" name="email" type="email" autocomplete="username" required maxlength="254"><label for="password">登录密码</label><input id="password" name="password" type="password" autocomplete="current-password" required maxlength="128"><div id="login-error" class="error">${esc(message)}</div><button class="primary">进入个人学习</button></form><div class="login-foot">不使用客户端 Key 登录，不在浏览器本地存储密码或个人学习数据。<br><a href="/workspace/">用户资料录入工作台</a> · <a href="/admin/">管理员账号管理</a></div></section></main>`}
function shell(){root.innerHTML=`<header class="topbar"><a class="brand" href="/learn/"><span class="brand-mark">温</span>个人学习</a><div class="account"><div><strong>${esc(user.display_name)}</strong><small>${esc(user.email)}</small></div><a class="button" href="/workspace/">录入资料</a><button data-action="channels">共享渠道</button><button data-action="channel-settings">我的渠道</button><button data-action="password">修改密码</button><button data-action="logout">退出</button></div></header><main class="container" id="content"></main>`}
function footer(){return '<footer class="foot">学习记录按当前登录用户隔离。共享渠道只开放选定来源的已发布内容，不共享账号凭据、笔记或学习记录。</footer>'}
async function library(){await releasePlayer();detail=null;detailSeq++;const seq=++listSeq,revision=epoch;const main=document.querySelector('#content');main.innerHTML=`<section class="intro"><div><span class="eyebrow">MY LIBRARY</span><h1>${esc(channelID?channelName:'在自己的知识库里，持续进步')}</h1><p>${channelID?'你正在学习已授权渠道中的资料，学习记录仍只归你本人。':'这里只显示你已发布到个人学习库的资料。'}</p></div><p class="boundary">${channelID?'<button type="button" data-action="own-library">返回我的资料库</button>':'上传和整理资料请前往「录入资料」。通过我的渠道可显式共享选定来源。'}</p></section><form class="toolbar" id="search"><input name="query" aria-label="搜索个人资料" placeholder="搜索自己的资料…" value="${esc(query)}"><select name="kind" aria-label="资料类型"><option value="">全部类型</option>${Object.entries(TYPES).map(([k,v])=>`<option value="${k}" ${kind===k?'selected':''}>${v}</option>`).join('')}</select><button>搜索</button></form><div class="summary" id="summary"></div><section class="library" id="library"><p class="loading">正在读取你的资料…</p></section><div id="pages"></div>${footer()}`;
try{const params=new URLSearchParams({limit:12,offset:(page-1)*12,q:query,kind});const [list,learning]=await Promise.all([api(listPath()),api('/client/learning?limit=1')]);if(seq!==listSeq||revision!==epoch||!main.isConnected||detail)return;document.querySelector('#summary').innerHTML=`<span><b>${learning.total}</b>份学习过的资料</span><span>累计学习 <b>${Math.round((learning.duration_ms||0)/60000)}</b>分钟</span>`;playQueue=list.items.filter(playable);queueOffset=(page-1)*12;queueHasMore=list.has_more??list.items.length===12;delete playbackPreferences.rangeStart;delete playbackPreferences.rangeEnd;document.querySelector('#library').innerHTML=list.items.map(item=>`<article class="lesson"><span class="kind">${TYPES[item.kind||'article']||'资料'}</span><h2>${esc(item.title)}</h2><p>${esc(item.summary||item.content?.slice(0,100)||'打开资料开始学习')}</p><div class="meta"><span>${esc(item.category||'未分类')}</span><span>${esc(item.author||'我的资料')}</span></div><button data-action="open" data-id="${esc(item.id)}">开始学习 →</button></article>`).join('')||'<div class="empty">暂时没有匹配的个人资料。可先到录入工作台上传并发布资料。</div>';document.querySelector('#pages').innerHTML=`<div class="pagination"><span>第 ${page} 页</span><div><button data-action="prev" ${page===1?'disabled':''}>上一页</button><button data-action="next" ${list.items.length<12?'disabled':''}>下一页</button></div></div>`}catch(error){if(seq===listSeq&&revision===epoch&&user){document.querySelector('#library').innerHTML='<p class="error">'+esc(error.message)+'</p>'}}}
function bodyText(item){let text=item.content||item.summary||'';if(item.kind&&item.kind!=='article'&&!item.assets?.length)text+='\n\n该资料仅登记了外部链接，未上传可直接播放的媒体。';return text.trim()?text:'该资料暂未提供正文。'}
function mediaHTML(item){
  const assets=item.assets||[];
  const url=a=>/^\/api\/v1\/(?:media\/[0-9a-f-]+|channels\/[0-9a-f-]+\/items\/[0-9a-f-]+\/media\/[0-9a-f-]+)$/.test(a.url)?esc(a.url):'';
  if(item.kind==='video'||item.kind==='audio')return assets[0]?`<${item.kind} id="learning-media" controls preload="metadata" playsinline src="${url(assets[0])}"></${item.kind}><section id="learning-player" aria-label="学习播放控制"></section>`:'';
  if(item.kind==='carousel')return assets.map(a=>`<img loading="lazy" src="${url(a)}" alt="${esc(a.name)}">`).join('');
  return '';
}
function sourceLink(item){try{const u=new URL(item.url);if(['https:','http:'].includes(u.protocol)&&!u.username&&!u.password)return `<a class="source-link" href="${esc(u.href)}" target="_blank" rel="noopener noreferrer">打开原始链接 ↗</a>`}catch{}return ''}
function progressKey(){return !detail?.assets?.length||!detail.kind||detail.kind==='article'?'preview':''}
function renderPersonal(){if(!detail)return;const unsaved=document.querySelector("#note")?.value;const action=name=>feedback.actions?.[name]?.active,progress=personal.progress?.[progressKey()];const root=document.querySelector('#personal');root.innerHTML=`<section class="personal-panel"><h2>我的学习状态</h2><div class="actions"><button data-action="like" class="${action('like')?'active':''}">${action('like')?'已喜欢':'喜欢'}</button><button data-action="favorite" class="${action('favorite')?'active':''}">${action('favorite')?'已收藏':'收藏'}</button><button data-action="review-plan" class="${personal.review_pending?'active':''}">${personal.review_pending?'取消待复习':'加入待复习'}</button><button data-action="review">完成一次复习</button></div><p class="personal-stats">查看 ${personal.view_count||0} 次 · 复习 ${personal.review_count||0} 次 · 记录 ${personal.record_count||0} 条</p><label for="progress-position">${progressKey()?'阅读位置（UTF-16 字符偏移）':detail.kind==='carousel'?'轮播进度':'媒体播放进度'}</label>${progressKey()?`<div class="progress-input"><input id="progress-position" type="number" min="0" max="${bodyText(detail).length}" value="${progress?.position?.offset||0}"><button data-action="progress">保存进度</button></div>`:detail.kind==='carousel'?`<div class="progress-input"><select id="progress-position">${detail.assets.map(a=>`<option value="${esc(a.id)}" ${progress?.position?.image_id===a.id?'selected':''}>${esc(a.name)}</option>`).join('')}</select><button data-action="progress">保存进度</button></div>`:'<button data-action="progress">保存当前播放位置</button>'}<p class="muted" id="personal-progress-status">${progress?'上次已保存，可跨设备继续。':'还没有保存进度。'}</p></section><section class="personal-panel"><h2>私人笔记</h2><div class="note-label"><label for="note">记录想法</label><small>最多 300 字</small></div><textarea id="note" maxlength="300" placeholder="只在你的账号内保存…">${esc(unsaved??(draft&&!draft.deleted?draft.text:''))}</textarea><div class="save-row"><button data-action="draft">保存草稿</button><button class="primary" data-action="note">保存笔记</button><small id="draft-state">${draft&&!draft.deleted?'已恢复私人草稿':''}</small></div><div id="records">${records.map(r=>`<article class="record"><p>${esc(r.text)}</p><small>${esc(new Date(r.created_at).toLocaleString('zh-CN'))}</small><br><button class="small" data-action="delete-note" data-id="${esc(r.id)}">删除</button></article>`).join('')||'<p class="muted" style="margin-top:20px">还没有笔记。</p>'}</div></section>`}
async function personalData(seq,{restore=false}={}){
 const id=detail?.id;if(!id)return;
 const [learning,fb,notes,remoteDraft]=await Promise.all([api('/client/learning?item_id='+id),api('/client/feedback?item_id='+id),api('/client/learning/items/'+id+'/records'),api('/client/learning/items/'+id+'/draft')]);
 if(seq!==detailSeq||detail?.id!==id||!user)return;
 personal=learning.items[0]||{};feedback=fb.items[0]||{};records=notes.items||[];draft=remoteDraft.draft;renderPersonal();
 if(restore)player?.restore(personal.progress?.['']);
}
function attachPlayer(item,seq){
 const media=document.querySelector('#learning-media'),host=document.querySelector('#learning-player');if(!media||!host)return;
 const reportBodies=new WeakMap(),sessionID=uuid();let sessionSequence=0,progressConflict=false;
 const revision=epoch;
 player=WzLearningPlayer.mount({media,host,item,queue:playQueue,preferences:playbackPreferences,reporting:()=>Boolean(user&&epoch===revision&&seq===detailSeq&&capabilities?.write&&automaticReporting&&!progressConflict),
  onError:error=>{if(user&&epoch===revision)notify(error.message||'播放器操作失败')},
  onNext:async id=>{
   if(seq!==detailSeq||revision!==epoch||!user)return;
   if(!id&&queueHasMore){
    const list=await api(listPath(queueOffset+12));
    if(seq!==detailSeq||revision!==epoch)return;
    queueOffset+=12;queueHasMore=list.has_more??list.items.length===12;
    const added=list.items.filter(playable);playQueue=playQueue.concat(added);id=added[0]?.id;
    // Skip article-only pages without claiming they are playable episodes.
    while(!id&&queueHasMore){const more=await api(listPath(queueOffset+12));if(seq!==detailSeq||revision!==epoch)return;queueOffset+=12;queueHasMore=more.has_more??more.items.length===12;const next=more.items.filter(playable);playQueue=playQueue.concat(next);id=next[0]?.id}
   }
   if(id)await openItem(id,{autoplay:true});else notify('已播放到当前筛选队列末尾。');
  },
  onCheckpoint:async sample=>{
   if(seq!==detailSeq||revision!==epoch||!user)return;
   if(progressConflict){const latest=await api('/client/learning?item_id='+item.id);if(seq!==detailSeq||revision!==epoch)return;personal=latest.items[0]||{};sample.timeMS=Math.max(0,Math.round(media.currentTime*1000));progressConflict=false;}
   let events=reportBodies.get(sample);
   if(!events){
    events=[{event_id:uuid(),item_id:item.id,action:'progress',mode:'',expected_revision:personal.progress?.['']?.revision||0,position:{kind:'media',version:item.revision,time_ms:item.duration_ms>0?Math.min(item.duration_ms,sample.timeMS):sample.timeMS}}];
    sample.intervals.forEach(([start,end],index)=>events.push({event_id:uuid(),item_id:item.id,action:'duration',session_id:sessionID,session_sequence:sessionSequence+index+1,start_at:new Date(start).toISOString(),end_at:new Date(end).toISOString()}));
    reportBodies.set(sample,events);
   }
   let ack;try{ack=await api('/client/learning/events',{method:'POST',body:{events}})}catch(error){if(error.status===409){progressConflict=true;reportBodies.delete(sample);throw new Error('另一设备已更新进度，自动保存已暂停。确认后点击「保存当前播放位置」可保存本页位置。');}throw error;}
   if(seq!==detailSeq||revision!==epoch)return;
   sessionSequence+=events.filter(event=>event.action==='duration').length;
   personal=ack.states.find(state=>state.item_id===item.id)||personal;
   const progressStatus=document.querySelector('#personal-progress-status');if(progressStatus&&personal.progress?.[''])progressStatus.textContent='上次已保存，可跨设备继续。';
   const text=document.querySelector('.personal-stats');if(text)text.textContent=`查看 ${personal.view_count||0} 次 · 复习 ${personal.review_count||0} 次 · 记录 ${personal.record_count||0} 条`;
  }
 });
}

async function openItem(id,{autoplay=false}={}){
 await releasePlayer();listSeq++;const seq=++detailSeq,revision=epoch;personal={};feedback={};draft=null;records=[];
 const main=document.querySelector('#content');main.innerHTML='<p class="loading">正在打开你的资料…</p>';
 let item;
 try{item=await api((channelID?'/channels/'+channelID+'/items/':'/items/')+encodeURIComponent(id))}catch(error){if(seq===detailSeq&&user)main.innerHTML='<p class="error">'+esc(error.message)+'</p><button data-action="back">返回我的资料库</button>';return}
 if(seq!==detailSeq||revision!==epoch||!user)return;
 detail=item;
 main.innerHTML=`<button data-action="back">← ${esc(channelID?channelName:'我的资料库')}</button><div class="learn-layout"><article class="reader"><span class="kind">${TYPES[item.kind||'article']}</span><h1>${esc(item.title)}</h1><p class="muted">${esc(item.author||'')} · ${esc(item.category||'未分类')}</p><div id="learning-sync" class="learning-sync" role="status">正在确认学习同步权限…</div>${mediaHTML(item)}${item.outline?`<details class="content-outline"><summary>内容大纲</summary><pre>${esc(item.outline)}</pre></details>`:''}<div class="article">${esc(bodyText(item))}</div>${sourceLink(item)}</article><aside id="personal"><p class="loading">正在读取个人学习记录…</p></aside></div>${footer()}`;
 try{
  capabilities=await api('/client/learning/capabilities');
  if(seq!==detailSeq||revision!==epoch)return;
  const sync=document.querySelector('#learning-sync');
  if(!capabilities.write)throw new Error('当前身份未获学习上报权限，请使用学习账号登录。');
  sync.innerHTML='<strong>学习数据同步已开启</strong><label><input type="checkbox" id="automatic-reporting" '+(automaticReporting?'checked':'')+'> 自动保存播放进度与有效播放时长</label><small>查看、喜欢、笔记和复习仍可正常保存；学习记录只属于当前登录账号。</small>';
  attachPlayer(item,seq);
  try{await learningEvent({action:'view'})}catch(error){notify('查看记录暂未同步：'+error.message)}
  await personalData(seq,{restore:true});
 }catch(error){
  if(seq!==detailSeq||revision!==epoch)return;
  document.querySelector('#personal').innerHTML='<p class="error">个人学习状态暂时不可用：'+esc(error.message)+'</p><button data-action="retry-learning">重试学习同步</button>';
  if(!player){document.querySelector('#learning-sync').textContent='学习同步暂不可用，不影响播放或阅读。';attachPlayer(item,seq)}
 }
 if(autoplay&&seq===detailSeq)document.querySelector('#learning-media')?.play().catch(()=>notify('浏览器阻止了自动播放，请点击播放继续。'));
}

async function learningEvent(event){return api('/client/learning/events',{method:'POST',body:{events:[{event_id:uuid(),item_id:detail.id,...event}]}})}
function passwordForm(){discardPlayer();detail=null;detailSeq++;listSeq++;document.querySelector('#content').innerHTML=`<section class="personal-panel settings"><h1>修改自己的密码</h1><p class="muted">修改成功后会撤销所有设备的登录会话，学习数据不受影响。</p><form id="password-form"><label for="old-password">当前密码</label><input id="old-password" name="current" type="password" autocomplete="current-password" required><label for="new-password">新密码</label><input id="new-password" name="password" type="password" autocomplete="new-password" required minlength="8" maxlength="128"><label for="confirm-password">确认新密码</label><input id="confirm-password" name="confirm" type="password" autocomplete="new-password" required><p class="error" id="password-error"></p><button class="primary">保存新密码</button><button type="button" data-action="back">取消</button></form></section>`}
document.addEventListener('submit',async event=>{const form=event.target;if(!['login','search','password-form'].includes(form.id))return;event.preventDefault();if(form.dataset.busy)return;form.dataset.busy='true';const button=form.querySelector('button');button.disabled=true;const fd=new FormData(form);try{if(form.id==='login'){const session=await api('/auth/login',{method:'POST',body:{email:fd.get('email'),password:fd.get('password')}});wzAuthSync.notify();form.reset();if(!['admin','user'].includes(session.role)||!session.user)throw new Error('无法确认登录状态，请重试。');epoch++;user=session.user;page=1;query='';kind='';shell();await library()}else if(form.id==='search'){query=String(fd.get('query')).trim();kind=String(fd.get('kind'));page=1;await library()}else{if(fd.get('password')!==fd.get('confirm'))throw new Error('两次输入的新密码不一致。');await api('/user/auth/password',{method:'POST',body:{current_password:fd.get('current'),password:fd.get('password')}});wzAuthSync.notify();form.reset();showLogin('密码已修改，请重新登录。')}}catch(error){const box=form.querySelector('.error');if(form.isConnected&&box)box.textContent=error.message;else if(user)notify(error.message)}finally{delete form.dataset.busy;if(button.isConnected)button.disabled=false}});
document.addEventListener('click',async event=>{const b=event.target.closest('[data-action]');if(!b||b.disabled||!user)return;const {action,id}=b.dataset,seq=detailSeq,revision=epoch;try{b.disabled=true;if(action==='logout'){await api('/user/auth/logout',{method:'POST'});wzAuthSync.notify();showLogin();return}if(action==='password'){await releasePlayer();passwordForm();return}if(action==='channels'){await channelBrowser();return}if(action==='channel-settings'){await channelSettingsForm();return}if(action==='use-channel'){channelID=id;channelName=b.dataset.name;page=1;query='';kind='';await library();return}if(action==='own-library'){channelID='';channelName='';page=1;await library();return}if(action==='retry-learning'){await openItem(detail.id);return}if(action==='back'){await library();return}if(action==='next'||action==='prev'){page=Math.max(1,page+(action==='next'?1:-1));await library();return}if(action==='open'){await openItem(id);return}if(!detail)return;if(action==='like'||action==='favorite'){await api('/client/feedback',{method:'POST',body:{events:[{event_id:uuid(),item_id:detail.id,action,value:!feedback.actions?.[action]?.active}]}})}else if(action==='review-plan')await learningEvent({action:'review_plan',value:!personal.review_pending});else if(action==='review')await learningEvent({action:'review'});else if(action==='draft'){const text=document.querySelector('#note').value;const ack=await api('/client/learning/drafts/commands',{method:'POST',body:{event_id:uuid(),item_id:detail.id,text,expected_revision:draft?.revision||0}});if(seq===detailSeq&&revision===epoch){draft=ack.draft;document.querySelector('#draft-state').textContent='草稿已保存';notify('私人草稿已保存')}return}else if(action==='note'){const text=document.querySelector('#note').value.trim();if(!text)throw new Error('请先填写笔记内容。');await learningEvent({action:'record_create',record_id:uuid(),text,...(draft&&!draft.deleted&&draft.text.trim()===text?{draft_revision:draft.revision}:{})});if(seq===detailSeq&&revision===epoch)document.querySelector('#note').value=''}else if(action==='delete-note'){if(!confirm('删除这条私人笔记？'))return;await learningEvent({action:'record_delete',record_id:id})}else if(action==='progress'){if(player){await player.flush(true);notify('当前播放位置已保存');return}const mode=progressKey(),current=personal.progress?.[mode],input=document.querySelector('#progress-position');let position;if(mode){const offset=Number(input.value);if(!Number.isInteger(offset)||offset<0||offset>bodyText(detail).length)throw new Error('阅读位置超出正文范围。');position={kind:'article',version:detail.revision,block_id:'body',offset}}else if(detail.kind==='carousel')position={kind:'image',version:detail.revision,image_id:input.value};else position={kind:'media',version:detail.revision,time_ms:Math.round(document.querySelector('#learning-media').currentTime*1000)};await learningEvent({action:'progress',mode,expected_revision:current?.revision||0,position})}if(seq===detailSeq&&revision===epoch){await personalData(seq);notify('已保存到当前用户账号')}}catch(error){if(user&&revision===epoch)notify(error.message)}finally{if(b.isConnected)b.disabled=false}});

async function channelBrowser(){
 await releasePlayer();detail=null;detailSeq++;const seq=++listSeq,revision=epoch;
 const main=document.querySelector('#content');main.innerHTML='<p class="loading">正在读取已授权共享渠道…</p>';
 try{
  const data=await api('/channels');if(seq!==listSeq||revision!==epoch)return;
  main.innerHTML=`<section class="intro"><div><h1>共享学习渠道</h1><p>公开渠道及邀请你的渠道；所有访问都需要登录。</p></div><button data-action="own-library">我的资料库</button></section><div class="library">${data.items.map(channel=>`<article class="lesson"><span class="kind">${channel.visibility==='public'?'已登录用户可见':'仅受邀用户'}</span><h2>${esc(channel.name)}</h2><p>${esc(channel.description||'提供者未填写说明')}</p><button data-action="use-channel" data-id="${esc(channel.id)}" data-name="${esc(channel.name)}">浏览渠道资料</button></article>`).join('')||'<p class="empty">暂无可访问的共享渠道。可请资料提供者在「我的渠道」邀请你的登录邮箱。</p>'}</div>${footer()}`;
 }catch(error){if(seq===listSeq&&revision===epoch)main.innerHTML='<p class="error">'+esc(error.message)+'</p>'}
}
async function channelSettingsForm(){
 await releasePlayer();detail=null;detailSeq++;const seq=++listSeq,revision=epoch;
 const main=document.querySelector('#content');main.innerHTML='<p class="loading">正在读取你的渠道配置…</p>';
 try{
  const [channel,workspace]=await Promise.all([api('/workspace/channel'),api('/workspace/workspace')]);
  if(seq!==listSeq||revision!==epoch)return;channelSettings=channel;
  main.innerHTML=`<section class="personal-panel channel-settings"><h1>我的学习渠道</h1><p class="muted">每个账号拥有独立渠道，默认仅自己可见。只共享明确勾选来源中的已发布内容；新建来源不会自动加入。公开也需要访问者先登录，不开放匿名访问。</p><form id="channel-form"><label>渠道名称<input name="name" required maxlength="200" value="${esc(channel.name)}"></label><label>渠道说明<textarea name="description" maxlength="4000">${esc(channel.description)}</textarea></label><label>访问权限<select name="visibility"><option value="private" ${channel.visibility==='private'?'selected':''}>仅自己</option><option value="public" ${channel.visibility==='public'?'selected':''}>公开 · 已登录用户</option><option value="invited" ${channel.visibility==='invited'?'selected':''}>邀请指定用户</option></select></label><fieldset><legend>共享数据源（不选则不共享任何内容）</legend>${workspace.sources.map(source=>`<label class="source-choice"><input type="checkbox" name="source_ids" value="${esc(source.id)}" ${(channel.source_ids||[]).includes(source.id)?'checked':''}> ${esc(source.name)}${source.enabled?'':'（已停用，不可访问）'}</label>`).join('')||'<p class="muted">请先在录入资料工作台创建来源或录入资料。</p>'}</fieldset><label>受邀用户邮箱<textarea name="invited_emails" placeholder="每行一个学习账号邮箱，支持待使用邀请">${esc((channel.invited_users||[]).map(invite=>invite.email).join('\n'))}</textarea></label><small>仅「邀请指定用户」模式使用名单。访问者使用自己的账号登录，不需要你的密码或来源 Key。</small><p class="error" id="channel-error"></p><div class="actions"><button class="primary">保存渠道设置</button><button type="button" data-action="own-library">返回</button></div></form></section>${footer()}`;
 }catch(error){if(seq===listSeq&&revision===epoch)main.innerHTML='<p class="error">'+esc(error.message)+'</p>'}
}
document.addEventListener('submit',async event=>{
 const form=event.target;if(form.id!=='channel-form')return;event.preventDefault();if(form.dataset.busy)return;
 const revision=epoch,seq=listSeq,button=form.querySelector('button');form.dataset.busy='true';button.disabled=true;
 try{const fd=new FormData(form),emails=String(fd.get('invited_emails')||'').split(/[\n,;，；]+/).map(value=>value.trim()).filter(Boolean);
  const channel=await api('/workspace/channel',{method:'PUT',body:{revision:channelSettings.revision,name:fd.get('name'),description:fd.get('description'),visibility:fd.get('visibility'),source_ids:fd.getAll('source_ids'),invited_emails:emails}});
  if(revision===epoch&&seq===listSeq){channelSettings=channel;notify('渠道设置已保存；学习记录仍按访问者各自账号隔离。')}
 }catch(error){if(form.isConnected&&epoch===revision)form.querySelector('.error').textContent=error.message+'（请检查邮箱格式；如版本冲突，请重新打开配置。）'}finally{delete form.dataset.busy;if(button.isConnected)button.disabled=false}
});
document.addEventListener('change',event=>{if(event.target.id==='automatic-reporting'){automaticReporting=event.target.checked;notify(automaticReporting?'已开启自动播放学习记录上报':'已关闭自动播放上报；手动学习操作仍可保存')}});

let learnerCheck=0;function revalidateLearner(){if(!user)return;const check=++learnerCheck,revision=epoch;player?.pause();root.style.visibility='hidden';api('/user/auth/session').then(()=>{if(check===learnerCheck&&revision===epoch)root.style.visibility=''}).catch(()=>{if(check===learnerCheck&&revision===epoch&&user)showLogin('无法确认用户会话，请重新登录。')})}wzAuthSync.subscribe(revalidateLearner);window.addEventListener('focus',revalidateLearner);
showLogin();api('/user/auth/session').then(session=>{epoch++;user=session.user;shell();return library()}).catch(()=>{});
