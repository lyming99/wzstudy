'use strict';
const assert=require('node:assert/strict');
const player=require('../internal/learnui/player.js');
const tests=[];
function test(name,run){run();tests.push(name);console.log('PASS:',name)}
test('SRT parsing supports BOM, CRLF and multiline captions',()=>{
 const cues=player.parseSubtitles('\uFEFF1\r\n00:00:00,000 --> 00:00:02,000\r\nHello\r\nWorld\r\n\r\n2\r\n00:00:02,000 --> 00:00:04,500\r\nNext');
 assert.deepEqual(cues,[{start:0,end:2000,text:'Hello\nWorld'},{start:2000,end:4500,text:'Next'}]);
});
test('WebVTT identifiers, settings and short timestamps',()=>{
 const cues=player.parseSubtitles('WEBVTT\n\nNOTE ignore this\n00:00.000 --> 00:01.000\nhidden\n\ncue-id\n00:01.250 --> 00:02.500 align:start position:0%\n<v narrator>Hello &amp; <b>world</b>');
 assert.deepEqual(cues,[{start:1250,end:2500,text:'Hello & world'}]);
});
test('Malformed and backwards cues are ignored',()=>assert.deepEqual(player.parseSubtitles('1\n00:99:00,000 --> 01:00:00,000\ninvalid\n\n2\n00:00:02,000 --> 00:00:01,000\nbackwards\n\n3\nnot a timestamp --> 00:00:01,000\ninvalid'),[]));
test('VTT style and region blocks cannot become captions',()=>assert.deepEqual(player.parseSubtitles('STYLE\n::cue { color: red; }\n\nREGION\nid:one\n\nWEBVTT'),[]));
test('Caption overlaps retain every active translation',()=>{
 const cues=[{start:0,end:3000,text:'A'},{start:2000,end:4000,text:'B'}];
 assert.deepEqual(player.activeCues(cues,2500).map(c=>c.text),['A','B']);
 assert.deepEqual(player.activeCues(cues,4000),[]);
});
test('Subtitle output is always plain text, never executable markup',()=>{
 const cues=player.parseSubtitles('1\n00:00:00,000 --> 00:00:02,000\n<img src=x onerror=alert(1)>&lt;script&gt;');
 assert.equal(cues[0].text,'<script>'); // the view escapes this text again
});
test('Chapters use the most recent start, including exact boundaries',()=>{
 const chapters=[{start_ms:0,title:'intro'},{start_ms:3000,title:'practice'}];
 assert.equal(player.chapterAt(chapters,2999).title,'intro');assert.equal(player.chapterAt(chapters,3000).title,'practice');assert.equal(player.chapterAt([],0),null);
});
test('Automatic next advances and stops at the queue end',()=>{assert.equal(player.nextIndex(3,0,'auto'),1);assert.equal(player.nextIndex(3,2,'auto'),-1)});
test('Single episode mode never advances',()=>assert.equal(player.nextIndex(3,1,'single'),1));
test('Range mode advances, wraps, and rejoins the selected range',()=>{assert.equal(player.nextIndex(5,1,'range',1,3),2);assert.equal(player.nextIndex(5,3,'range',1,3),1);assert.equal(player.nextIndex(5,0,'range',1,3),1);assert.equal(player.nextIndex(5,4,'range',1,3),1)});
test('A one-episode range loops that episode',()=>assert.equal(player.nextIndex(3,1,'range',1,1),1));
test('Invalid range and invalid queue positions never play another item',()=>{assert.equal(player.nextIndex(3,0,'range',2,1),-1);assert.equal(player.nextIndex(3,0,'range',0,4),-1);assert.equal(player.nextIndex(0,0,'auto'),-1);assert.equal(player.nextIndex(2,-1,'auto'),-1)});
test('Once mode stops and does not imply automatic next',()=>assert.equal(player.nextIndex(3,0,'once'),-1));
test('Time labels handle long media and invalid input',()=>{assert.equal(player.clock(3661000),'1:01:01');assert.equal(player.clock(-1),'00:00');assert.equal(player.clock(NaN),'00:00')});
console.log(`${tests.length} player logic checks passed.`);
