'use strict';
(function(scope){
  const escape=value=>String(value??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
  function timestamp(value){
    const m=String(value).match(/^(?:(\d{1,3}):)?(\d{2}):(\d{2})[.,](\d{3})$/);
    if(!m||Number(m[2])>59||Number(m[3])>59)return NaN;
    return ((Number(m[1]||0)*60+Number(m[2]))*60+Number(m[3]))*1000+Number(m[4]);
  }
  function parseSubtitles(content){
    const blocks=String(content||'').replace(/^\uFEFF/,'').replace(/\r\n?/g,'\n').split(/\n[ \t]*\n/),cues=[];
    for(const block of blocks){
      const lines=block.trim().split('\n');
      if(/^(NOTE|STYLE|REGION)(\s|$)/.test(lines[0]))continue;
      const index=lines.findIndex(line=>line.includes('-->'));
      if(index<0)continue;
      const parts=lines[index].trim().split(/\s+-->\s+/);
      if(parts.length!==2)continue;
      const start=timestamp(parts[0]),end=timestamp(parts[1].split(/\s/)[0]);
      const text=lines.slice(index+1).join('\n').replace(/<[^>]*>/g,'').replace(/&(amp|lt|gt|quot|apos|nbsp);/g,(_,key)=>({amp:'&',lt:'<',gt:'>',quot:'"',apos:"'",nbsp:' '}[key])).trim();
      if(Number.isFinite(start)&&Number.isFinite(end)&&end>start&&text)cues.push({start,end,text});
      if(cues.length>=5000)break;
    }
    return cues.sort((a,b)=>a.start-b.start||a.end-b.end);
  }
  function activeCues(cues,timeMS){return cues.filter(cue=>cue.start<=timeMS&&timeMS<cue.end)}
  function chapterAt(chapters,timeMS){let found=null;for(const chapter of chapters||[]){if(chapter.start_ms>timeMS)break;found=chapter}return found}
  function nextIndex(length,index,mode,start=0,end=length-1){
    if(length<1||index<0||index>=length)return -1;
    if(mode==='single')return index;
    if(mode==='range'){
      if(!Number.isInteger(start)||!Number.isInteger(end)||start<0||end>=length||start>end)return -1;
      return index<start||index>=end?start:index+1;
    }
    return mode==='auto'&&index+1<length?index+1:-1;
  }
  function clock(ms){const seconds=Math.max(0,Math.floor((Number(ms)||0)/1000));return (seconds>=3600?Math.floor(seconds/3600)+':':'')+String(Math.floor(seconds/60)%60).padStart(2,'0')+':'+String(seconds%60).padStart(2,'0')}
  function mount({media,host,item,queue=[],preferences={},onNext,onCheckpoint,onError=()=>{},reporting=()=>true}){
    let alive=true,scrubbing=false,restored=false,userInteracted=false,currentCue=-1,sentence=-1,lyrics=false,waiting=false,inFlight=null,pending=null,intervals=[],previous=null,lastScroll=-1,lastAckMS=null;
    let mode=preferences.mode||'auto',rangeStart=0,rangeEnd=Math.max(0,queue.length-1),trackIndex=0,translationIndex=-1;
    const tracks=(item.subtitles||[]).map(track=>({...track,cues:parseSubtitles(track.content)})).filter(track=>track.cues.length);
    const chapters=item.chapters||[],listeners=[];
    const listen=(node,event,handler)=>{node.addEventListener(event,handler);listeners.push(()=>node.removeEventListener(event,handler))};
    host.innerHTML=`<div class="player-progress"><label for="media-seek">播放进度 <span id="seek-preview"></span></label><input id="media-seek" type="range" min="0" max="1" step="0.05" value="0" aria-label="拖动播放进度"><div class="chapter-markers" aria-hidden="true"></div><div class="player-time"><span id="media-time">00:00 / 00:00</span><strong id="media-chapter"></strong></div></div><div class="player-options"><label>播放模式<select id="playback-mode"><option value="auto">自动下一集</option><option value="single">单集循环</option><option value="range">指定队列范围循环</option><option value="once">仅播放本集</option></select></label><label>播放速度<select id="playback-rate">${[.5,.75,1,1.25,1.5,2].map(rate=>`<option value="${rate}" ${rate===1?'selected':''}>${rate}×</option>`).join('')}</select></label></div><div id="queue-range" class="queue-range" hidden><label>起始集<select id="queue-start">${queue.map((q,i)=>`<option value="${i}">${i+1}. ${escape(q.title)}</option>`).join('')}</select></label><label>结束集<select id="queue-end">${queue.map((q,i)=>`<option value="${i}">${i+1}. ${escape(q.title)}</option>`).join('')}</select></label><small>范围内自动下一集，结束后回到起始集；仅循环本次已加载队列。</small></div><p class="muted player-queue">当前队列 ${queue.length} 集，播放模式不改变资料筛选范围。</p>${chapters.length?`<details class="chapter-outline"><summary>章节大纲 · ${chapters.length} 章</summary><div>${chapters.map(chapter=>`<button type="button" data-chapter-ms="${chapter.start_ms}">${clock(chapter.start_ms)} · ${escape(chapter.title)}</button>`).join('')}</div></details>`:''}${tracks.length?`<div class="subtitle-options"><label>原文字幕<select id="subtitle-track">${tracks.map((track,i)=>`<option value="${i}">${escape(track.language)} · ${escape(track.id)}</option>`).join('')}</select></label><label>翻译字幕<select id="translation-track"><option value="-1">不显示翻译</option>${tracks.map((track,i)=>`<option value="${i}">${escape(track.language)} · ${escape(track.id)}</option>`).join('')}</select></label></div><div class="subtitle-current" aria-live="off"><p id="subtitle-original"></p><p id="subtitle-translation" lang="zh"></p></div><div class="actions"><button type="button" id="lyrics-toggle" aria-pressed="false">歌词模式</button><button type="button" id="sentence-toggle" aria-pressed="false">单句循环</button><button type="button" id="sentence-prev">上一句</button><button type="button" id="sentence-next">下一句</button></div><p class="muted" id="sentence-status">点击字幕可跳转；单句循环用于语言跟读。</p><div id="lyrics-list" class="lyrics-list" hidden></div>`:'<p class="muted">未提供可用字幕；有字幕时可启用歌词、翻译和单句跟读。</p>'}<p id="playback-status" role="status"></p>`;
    const find=id=>host.querySelector('#'+id),seek=find('media-seek'),status=find('playback-status');
    find('playback-mode').value=mode;find('queue-end').value=String(rangeEnd);find('queue-range').hidden=mode!=='range';
    if(preferences.rangeStart!=null&&preferences.rangeEnd<queue.length){rangeStart=preferences.rangeStart;rangeEnd=preferences.rangeEnd;find('queue-start').value=String(rangeStart);find('queue-end').value=String(rangeEnd)}
    function savePreferences(){Object.assign(preferences,{mode,rangeStart,rangeEnd})}
    function setSentence(index){
      const cues=tracks[trackIndex]?.cues||[];
      if(!cues.length)return;
      sentence=Math.max(0,Math.min(index,cues.length-1));
      find('sentence-toggle').setAttribute('aria-pressed','true');find('sentence-toggle').classList.add('active');
      find('sentence-status').textContent='正在循环第 '+(sentence+1)+' 句：'+cues[sentence].text;
      media.currentTime=cues[sentence].start/1000;userInteracted=true;sync();
    }
    function clearSentence(){sentence=-1;if(find('sentence-toggle')){find('sentence-toggle').setAttribute('aria-pressed','false');find('sentence-toggle').classList.remove('active');find('sentence-status').textContent='点击字幕可跳转；单句循环用于语言跟读。'}}
    function renderLyrics(){
      const cues=tracks[trackIndex]?.cues||[],translations=tracks[translationIndex]?.cues||[];
      if(!find('lyrics-list'))return;
      let j=0;
      find('lyrics-list').innerHTML=cues.map((cue,index)=>{
        while(j<translations.length&&translations[j].end<=cue.start)j++;
        const texts=[];
        for(let k=j;k<translations.length&&translations[k].start<cue.end;k++){if(translations[k].end>cue.start)texts.push(translations[k].text)}
        return `<button type="button" data-cue="${index}"><time>${clock(cue.start)}</time><span>${escape(cue.text)}${texts.length?`<small>${escape(texts.join('\n'))}</small>`:''}</span></button>`;
      }).join('');
      currentCue=-1;lastScroll=-1;sync();
    }
    function sync(){
      if(!alive)return;
      const duration=Number.isFinite(media.duration)?media.duration:Math.max(0,(item.duration_ms||0)/1000),time=media.currentTime||0;
      seek.disabled=duration<=0;seek.max=String(duration||1);
      if(!scrubbing)seek.value=String(time);
      seek.setAttribute('aria-valuetext',clock(Number(seek.value)*1000)+'，共 '+clock(duration*1000));
      find('media-time').textContent=clock(time*1000)+' / '+clock(duration*1000);
      find('media-chapter').textContent=chapterAt(chapters,time*1000)?.title||'';
      const cueList=tracks[trackIndex]?.cues||[];
      const matching=activeCues(cueList,time*1000),active=matching.length?cueList.indexOf(matching[0]):-1;
      if(find('subtitle-original')){
        find('subtitle-original').lang=tracks[trackIndex]?.language||'';
        find('subtitle-translation').lang=tracks[translationIndex]?.language||'';
        find('subtitle-original').textContent=matching.map(cue=>cue.text).join('\n');
        find('subtitle-translation').textContent=translationIndex!==trackIndex?activeCues(tracks[translationIndex]?.cues||[],time*1000).map(cue=>cue.text).join('\n'):'';
        if(active!==currentCue){
          host.querySelector('[data-cue].current')?.classList.remove('current');
          const node=host.querySelector(`[data-cue="${active}"]`);node?.classList.add('current');
          if(node&&lyrics&&active!==lastScroll){node.scrollIntoView({block:'nearest',behavior:'smooth'});lastScroll=active}
          currentCue=active;
        }
      }
      if(sentence>=0&&!media.paused&&!scrubbing){const cue=cueList[sentence];if(cue&&(time*1000>=cue.end-35||time*1000<cue.start-100))media.currentTime=cue.start/1000}
    }
    function preview(){const ms=Number(seek.value)*1000;find('seek-preview').textContent=clock(ms)+(chapterAt(chapters,ms)?' · '+chapterAt(chapters,ms).title:'')}
    listen(seek,'pointerdown',()=>{scrubbing=true;userInteracted=true});
    listen(seek,'input',()=>{scrubbing=true;preview()});
    listen(seek,'change',()=>{clearSentence();media.currentTime=Number(seek.value);scrubbing=false;userInteracted=true;find('seek-preview').textContent='';sync()});
    listen(document,'pointerup',()=>{setTimeout(()=>{if(alive&&scrubbing){scrubbing=false;find('seek-preview').textContent='';sync()}},0)});
    listen(seek,'pointercancel',()=>{scrubbing=false;find('seek-preview').textContent='';sync()});
    listen(media,'seeking',()=>{userInteracted=true;previous=null});
    listen(media,'loadedmetadata',()=>{const duration=media.duration||0;host.querySelector('.chapter-markers').innerHTML=chapters.filter(c=>duration>0&&c.start_ms<duration*1000).map(c=>`<i style="left:${100*c.start_ms/(duration*1000)}%" title="${escape(c.title)}"></i>`).join('');sync()});
    listen(find('playback-mode'),'change',event=>{mode=event.target.value;find('queue-range').hidden=mode!=='range';savePreferences()});
    listen(find('playback-rate'),'change',event=>{media.playbackRate=Number(event.target.value);previous=null});
    for(const id of ['queue-start','queue-end'])listen(find(id),'change',()=>{rangeStart=Number(find('queue-start').value);rangeEnd=Number(find('queue-end').value);status.textContent=rangeStart>rangeEnd?'起始集不能晚于结束集。':'';savePreferences()});
    listen(host,'click',event=>{
      const chapter=event.target.closest('[data-chapter-ms]'),cue=event.target.closest('[data-cue]');
      if(chapter){clearSentence();media.currentTime=Number(chapter.dataset.chapterMs)/1000;userInteracted=true}
      if(cue){const index=Number(cue.dataset.cue);if(sentence>=0)setSentence(index);else{media.currentTime=tracks[trackIndex].cues[index].start/1000;userInteracted=true;sync()}}
    });
    if(tracks.length){
      if(tracks.length>1){translationIndex=tracks.findIndex((track,index)=>index!==trackIndex&&track.language!==tracks[trackIndex].language);find('translation-track').value=String(translationIndex)}
      listen(find('subtitle-track'),'change',event=>{trackIndex=Number(event.target.value);clearSentence();renderLyrics()});
      listen(find('translation-track'),'change',event=>{translationIndex=Number(event.target.value);renderLyrics()});
      listen(find('lyrics-toggle'),'click',()=>{lyrics=!lyrics;find('lyrics-list').hidden=!lyrics;find('lyrics-toggle').setAttribute('aria-pressed',String(lyrics));find('lyrics-toggle').classList.toggle('active',lyrics);lastScroll=-1;currentCue=-2;sync()});
      listen(find('sentence-toggle'),'click',()=>{if(sentence>=0)clearSentence();else setSentence(Math.max(0,currentCue))});
      listen(find('sentence-prev'),'click',()=>{const index=Math.max(0,(sentence>=0?sentence:currentCue)-1);if(sentence>=0)setSentence(index);else{media.currentTime=tracks[trackIndex].cues[index].start/1000;userInteracted=true;sync()}});
      listen(find('sentence-next'),'click',()=>{const cues=tracks[trackIndex].cues,index=Math.min(cues.length-1,(sentence>=0?sentence:Math.max(0,currentCue))+1);if(sentence>=0)setSentence(index);else{media.currentTime=cues[index].start/1000;userInteracted=true;sync()}});
      renderLyrics();
    }
    function collect(){
      const now=Date.now(),time=media.currentTime;
      const valid=reporting()&&!media.paused&&!media.ended&&!media.seeking&&!waiting&&media.readyState>=2&&document.visibilityState!=='hidden';
      if(valid&&previous){const elapsed=now-previous.at,delta=(time-previous.time)*1000;if(elapsed>0&&elapsed<=2000&&delta>0&&delta<=elapsed*Math.max(1,media.playbackRate)*3+300){
        const last=intervals[intervals.length-1];if(last&&last[1]===previous.at&&now-last[0]<90000)last[1]=now;else intervals.push([previous.at,now]);
      }}
      previous=valid?{at:now,time}:null;
    }
    async function flush(force=false){
      if((!reporting()&&!force)||!onCheckpoint)return;
      if(inFlight){await inFlight;if(intervals.length||lastAckMS===null||Math.abs(media.currentTime*1000-lastAckMS)>250)return flush(force);return}
      if(!pending){pending={timeMS:Math.max(0,Math.round(media.currentTime*1000)),intervals:intervals.slice(0,99)};intervals=intervals.slice(99)}
      const sample=pending;
      inFlight=Promise.resolve().then(()=>onCheckpoint(sample));
      try{await inFlight;lastAckMS=sample.timeMS;pending=null;if(alive)status.textContent='播放进度已同步到当前账号。'}finally{inFlight=null}
    }
    const safeFlush=()=>flush().catch(error=>{if(alive){status.textContent='同步暂未成功，仍可继续播放；下次同步会重试。';onError(error)}});
    const ticker=setInterval(()=>{collect();sync()},200),checkpoint=setInterval(safeFlush,15000);
    listen(media,'timeupdate',sync);
    listen(media,'play',()=>{userInteracted=true;previous=null;waiting=false});
    listen(media,'waiting',()=>{waiting=true;previous=null});
    listen(media,'playing',()=>{waiting=false;previous=null});
    listen(media,'pause',()=>{previous=null;safeFlush()});
    listen(document,'visibilitychange',()=>{previous=null;if(document.visibilityState==='hidden')safeFlush()});
    listen(media,'ended',async()=>{
      await safeFlush();if(!alive)return;
      if(sentence>=0){media.currentTime=tracks[trackIndex].cues[sentence].start/1000;media.play().catch(onError);return}
      if(mode==='single'){media.currentTime=0;media.play().catch(onError);return}
      if(mode==='range'&&rangeStart>rangeEnd){status.textContent='请修正播放范围后再继续。';return}
      const index=queue.findIndex(q=>q.id===item.id),next=nextIndex(queue.length,index,mode,rangeStart,rangeEnd);
      if(next>=0&&queue[next].id===item.id){media.currentTime=0;media.play().catch(onError);return}
      if(next>=0&&onNext){await onNext(queue[next].id).catch(onError)}else if(mode==='auto'&&onNext){await onNext(null).catch(onError)}else status.textContent='本次播放结束。';
    });
    sync();
    return {
      flush,
      restore(progress){if(restored)return;restored=true;const position=progress?.position;if(!position||position.version!==item.revision)return;const restore=()=>{if(alive&&!userInteracted&&media.paused&&Number.isFinite(media.duration))media.currentTime=Math.min(Math.max(0,position.time_ms/1000),Math.max(0,media.duration-.1))};if(media.readyState)restore();else listen(media,'loadedmetadata',restore)},
      pause(){media.pause()},
      destroy(){if(!alive)return;alive=false;clearInterval(ticker);clearInterval(checkpoint);for(const remove of listeners)remove();media.pause();host.innerHTML=''},
    };
  }
  const api={parseSubtitles,activeCues,chapterAt,nextIndex,clock,mount};
  if(typeof module!=='undefined'&&module.exports)module.exports=api;else scope.WzLearningPlayer=api;
})(typeof globalThis!=='undefined'?globalThis:this);
