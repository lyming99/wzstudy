'use strict';
// Dependency-free behavior tests for the runtime refresh lifecycle.
const assert=require('node:assert/strict');
const fs=require('node:fs');
const vm=require('node:vm');
const source=fs.readFileSync('internal/adminui/app.js','utf8');
assert.ok(source.includes('<option value="custom">OpenAI 兼容</option>'));
assert.ok(source.includes("${provider==='custom'?'selected':''}>OpenAI 兼容</option>"));
assert.ok(fs.readFileSync('internal/adminui/agent-access.js','utf8').includes("['custom', 'OpenAI 兼容']"));
const start=source.indexOf('function runtimeMarkup(status){');
const end=source.indexOf('async function loadRuntimeModels()',start);
const code=source.slice(start,end);
let resolveRequest,calls=0,timer=null;
const status={running:true,pid:123,logs:[]};
const live={children:[]};
function child(html){return {outerHTML:html,contains:()=>false,querySelector:()=>null,replaceWith(next){live.children[live.children.indexOf(this)]=next;}};}
live.children=[child('old-actions'),child('old-error'),child('old-head'),child('old-log')];
const body={isConnected:true,innerHTML:'untouched-form-with-unsaved-key',removeAttribute(){}};
const context={route:'agent-access',authEpoch:1,state:{authenticated:true,role:'admin'},
 $:selector=>selector==='#agent-runtime-body'?body:selector==='#runtime-live'?live:null,
 esc:x=>String(x??''),runtimeLogEntry:x=>String(x),
 api:()=>{calls++;return new Promise(resolve=>{resolveRequest=resolve});},
 clearTimeout:()=>{timer=null},setTimeout:fn=>{timer=fn;return 1},
 document:{activeElement:null,createElement:()=>({set innerHTML(value){this.content={children:[child('new-actions'),child('new-error'),child('new-head'),child('new-log')]}}})}
};
vm.createContext(context);vm.runInContext(code,context);
(async()=>{
 assert.match(context.runtimeLiveMarkup({running:false,last_error:'401'}),/已停止/);
 assert.match(context.runtimeLiveMarkup(status),/PID 123/);
 // Live polling cannot replace the form or expanded JSON log.
 live.children[3].querySelector=()=>({open:true});
 const first=context.loadAgentRuntime(true);resolveRequest(status);await first;
 assert.equal(body.innerHTML,'untouched-form-with-unsaved-key');
 assert.equal(live.children[0].outerHTML,'new-actions');
 assert.equal(live.children[3].outerHTML,'old-log');
 assert.equal(typeof timer,'function');
 // Ignore responses from the previous authentication lifecycle.
 live.children[0].outerHTML='fresh-state';
 const stale=context.loadAgentRuntime(true);context.authEpoch++;resolveRequest(status);await stale;
 assert.equal(live.children[0].outerHTML,'fresh-state');assert.equal(timer,null);
 // Older requests must not overwrite the response from a newer refresh.
 const older=context.loadAgentRuntime(true),resolveOlder=resolveRequest;
 const newer=context.loadAgentRuntime(true);resolveRequest(status);await newer;
 live.children[0].outerHTML='newest-state';resolveOlder(status);await older;
 assert.equal(live.children[0].outerHTML,'newest-state');
 // Detached panels and non-admin/off-route users do not continue polling.
 const detached=context.loadAgentRuntime(true);body.isConnected=false;resolveRequest(status);await detached;
 assert.equal(timer,null);body.isConnected=true;
 const before=calls;
 context.route='home';await context.loadAgentRuntime(true);
 context.route='agent-access';context.state.role='user';await context.loadAgentRuntime(true);
 context.state.role='admin';context.state.authenticated=false;await context.loadAgentRuntime(true);
 assert.equal(calls,before);
 // Form connection tests use unsaved inputs; saving explicitly applies them.
 context.state.authenticated=true;context.state.role='admin';
 const inputs={
  '#runtime-installdir':{value:'D:/agent'},'#runtime-workdir':{value:'D:/agent/work'},
  '#runtime-provider':{value:'custom'},'#runtime-model':{value:'unsaved-model'},
  '#runtime-baseurl':{value:'https://model.example/v1'},'#runtime-apikey':{value:'test-form-key'},
  '#runtime-autostart':{checked:true},'#runtime-clear-key':{checked:false},
  '#runtime-test-result':{isConnected:true},'[data-action="runtime-test"]':{}
 };
 context.$=selector=>inputs[selector]||(selector==='#agent-runtime-body'?body:selector==='#runtime-live'?live:null);
 context.document.querySelectorAll=()=>[];
 const requests=[],notices=[];
 context.toast=(message,error)=>notices.push({message,error});
 context.api=async(path,options)=>{requests.push({path,options});return path.endsWith('test-connection')?{model:'unsaved-model',latency_ms:12}:status};
 await context.testAgentRuntimeConnection();
 assert.equal(requests[0].path,'/admin/agent/runtime/test-connection');
 assert.equal(requests[0].options.body.model,'unsaved-model');
 assert.equal(requests[0].options.body.api_key,'test-form-key');
 assert.match(inputs['#runtime-test-result'].textContent,/连接成功/);
 inputs['#runtime-clear-key'].checked=true;requests.length=0;
 await context.testAgentRuntimeConnection();
 assert.equal(requests[0].options.body.use_saved_key,false);
 assert.equal(requests[0].options.body.clear_api_key,true);
 requests.length=0;
 await context.saveAgentRuntime();
 assert.equal(requests[0].options.method,'PUT');assert.equal(requests[0].options.body.restart,true);
 assert.equal(requests[0].options.body.model.model,'unsaved-model');
 assert.ok(notices.some(x=>x.message.includes('重新启动')));
 requests.length=0;
 await context.restartAgentRuntime();
 assert.equal(requests[0].path,'/admin/agent/runtime/restart');
 console.log('runtime UI refresh, connection and restart: all behavior tests passed');
})().catch(error=>{console.error(error);process.exitCode=1});
