'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const {webcrypto} = require('node:crypto');
const root = path.join(__dirname, '..');
const fixture = fs.readFileSync(path.join(root, 'examples/wenzflow-roadmap.json'), 'utf8');
const scripts = ['roadmaps.js', 'roadmap-import.js'].map(file => fs.readFileSync(path.join(root, 'internal/adminui', file), 'utf8'));
const clone = value => JSON.parse(JSON.stringify(value));
const flat = nodes => ({primaryRootId: 'root', nodes});
const node = (uuid = 'root', parentId = null, extra = {}) => ({uuid, parentId, content: uuid, ...extra});

function harness() {
  const elements = {}, calls = [];
  const env = {
    TextEncoder, TextDecoder, Uint8Array, crypto: webcrypto, console,
    authEpoch: 1, state: {authenticated: true}, route: 'roadmaps',
    document: {addEventListener() {}, querySelectorAll: () => []},
    window: {addEventListener() {}, confirm: () => true},
    $: key => elements[key],
    esc: value => String(value).replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;').replaceAll('"', '&quot;').replaceAll("'", '&#39;'),
    toast: () => {}, showError: error => {throw error;},
    formError: message => {env.error = message;},
    openModal: (_title, _subtitle, body, buttons) => {
      if (elements['#roadmap-import-form']) elements['#roadmap-import-form'].isConnected = false;
      env.markup = body + buttons;
      elements['#roadmap-import-form'] = {isConnected: true, addEventListener() {}};
      elements['#roadmap-import-file'] = {addEventListener() {}};
      elements['#form-error'] = {replaceChildren() {env.error = '';}};
      elements['#roadmap-import-preview'] = {textContent: '', innerHTML: ''};
      elements['[data-action="roadmap-import-confirm"]'] = {disabled: true};
    },
    closeModal: () => {elements['#roadmap-import-form'].isConnected = false;},
    api: async (url, options) => {
      calls.push(clone({url, options}));
      return {...clone(options.body), id: 'saved-id', revision: 1};
    },
  };
  vm.createContext(env);
  scripts.forEach(source => vm.runInContext(source, env));
  vm.runInContext('renderRoadmapEditor = () => {}; roadmapSearchResources = async () => {};', env);
  return {
    env, elements, calls,
    parse: data => clone(env.parseWenzflowRoadmap(typeof data === 'string' ? data : JSON.stringify(data))),
    open() {env.roadmapImportDialog(); return elements['#roadmap-import-form'];},
    eval: code => vm.runInContext(code, env),
  };
}
function file(text = fixture, extra = {}) {
  const bytes = new TextEncoder().encode(text);
  return {name: '路线.json', size: bytes.byteLength, arrayBuffer: async () => bytes.buffer, ...extra};
}
function deferred() {
  let resolve;
  const promise = new Promise(done => {resolve = done;});
  return {promise, resolve};
}

test('native desktop source maps order, parents, collapse and reference semantics', () => {
  const h = harness(), result = h.parse(fixture), doc = result.doc;
  assert.equal(doc.id, ''); assert.equal(doc.revision, 0); assert.equal(doc.summary, '');
  assert.equal(doc.title, 'Go 学习路线');
  assert.deepEqual(doc.nodes.map(n => n.title), ['Go 学习路线', '学习准备', '基础语法', '变量与函数']);
  assert.equal(doc.nodes[0].parent_id, '');
  assert.equal(doc.nodes[2].parent_id, doc.nodes[0].id);
  assert.equal(doc.nodes[3].parent_id, doc.nodes[2].id);
  assert.equal(doc.nodes[2].collapsed, true);
  assert.deepEqual(result.depths, [1, 2, 2, 3]);
  assert.equal(result.depth, 3);
  assert.match(doc.nodes[2].note, /WenzFlow 子笔记引用：55555555/);
  assert.match(doc.nodes[2].note, /WenzFlow 待办：未完成/);
  assert.match(doc.nodes[1].note, /原链接：https:\/\/go.dev\/learn\//);
  assert.match(doc.nodes[1].note, /链接标题：Go 学习入口/);
  for (const n of doc.nodes) {
    assert.deepEqual(n.item_ids, []);
    assert.match(n.id, /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
    assert.equal('fontColor' in n, false);
  }
  assert.equal(new Set(doc.nodes.map(n => n.id)).size, 4);
  assert.notEqual(h.parse(fixture).doc.nodes[0].id, doc.nodes[0].id);
});

test('parent-after-child source, omitted indices and ties preserve stable sibling order', () => {
  const h = harness();
  const result = h.parse(flat([node('b', 'root'), node('a', 'root', {index: 0}), node(), node('c', 'root', {index: 3})]));
  assert.deepEqual(result.doc.nodes.map(n => n.title), ['root', 'b', 'a', 'c']);
});

test('nullable native serializer fields and BOM are accepted', () => {
  const h = harness();
  const value = flat([node('root', null, {expand: null, index: null, note: null, nodeType: null, link: null, isTodo: null, isChecked: null})]);
  const out = h.parse('\uFEFF' + JSON.stringify(value));
  assert.equal(out.doc.nodes[0].collapsed, false);
});

test('source IDs are remapped, including strings special to JS prototypes', () => {
  const h = harness();
  const out = h.parse({nodes: [node('__proto__'), node('constructor', '__proto__')]});
  assert.equal(out.doc.nodes[1].parent_id, out.doc.nodes[0].id);
  assert.equal({}.polluted, undefined);
});

test('legacy root/children source preserves nesting, order and fold state', () => {
  const h = harness();
  const out = h.parse({root: {id: 1, text: '旧版', children: [{id: 'one', title: '分支', collapsed: true, children: [{label: '子节点'}]}]}});
  assert.deepEqual(out.doc.nodes.map(n => n.title), ['旧版', '分支', '子节点']);
  assert.equal(out.doc.nodes[1].collapsed, true);
  assert.equal(out.depth, 3);
  assert.equal(h.parse({text: '根', children: []}).doc.title, '根');
});

test('blank, image, formula, todo and internal references have explicit conversion warnings', () => {
  const h = harness();
  const out = h.parse(flat([node('root', null, {content: ''}), node('image', 'root', {content: null, image: 'attachment-id'}), node('math', 'root', {content: '', formula: 'a^2+b^2=c^2', isTodo: true, isChecked: true, wenzLink: 'note-id'})]));
  assert.equal(out.doc.title, '导入的学习路线');
  assert.equal(out.doc.nodes[1].title, '图片节点');
  assert.match(out.doc.nodes[1].note, /未导入附件/);
  assert.equal(out.doc.nodes[2].title, '公式节点');
  assert.match(out.doc.nodes[2].note, /公式：a\^2/);
  assert.match(out.doc.nodes[2].note, /待办：已完成/);
  assert.match(out.warnings.join('\n'), /不复制或下载附件/);
  assert.match(out.warnings.join('\n'), /不保留公式排版/);
});

const invalidCases = [
  ['malformed JSON', '{', /有效的 JSON/],
  ['array document', [], /原始 JSON/],
  ['null document', 'null', /原始 JSON/],
  ['unrelated JSON', {hello: 'world'}, /未找到/],
  ['drawboard JSON', {elements: []}, /白板/],
  ['empty nodes', {nodes: []}, /没有节点/],
  ['invalid nodes type', {nodes: {}}, /节点数组/],
  ['non-object node', {nodes: [null]}, /格式无效/],
  ['missing ID', {nodes: [{content: 'root'}]}, /ID必须是文本/],
  ['empty ID', {nodes: [node(' ')]}, /不能为空/],
  ['duplicate ID', {nodes: [node(), node()]}, /重复节点/],
  ['multiple roots', {nodes: [node(), node('another')]}, /只能有一个/],
  ['no root', {nodes: [node('a', 'b'), node('b', 'a')]}, /中心节点/],
  ['missing parent', flat([node(), node('a', 'missing')]), /孤立节点/],
  ['self cycle', flat([node(), node('a', 'a')]), /循环/],
  ['disconnected cycle', flat([node(), node('a', 'b'), node('b', 'a')]), /循环/],
  ['wrong primary root', {primaryRootId: 'child', nodes: [node(), node('child', 'root')]}, /primaryRootId/],
  ['invalid text type', flat([node('root', null, {content: {text: 'root'}})]), /名称必须是文本/],
  ['invalid parent type', flat([node('root', 1)]), /父节点 ID必须是文本/],
  ['invalid expand type', flat([node('root', null, {expand: 'false'})]), /布尔值/],
  ['invalid todo type', flat([node('root', null, {isTodo: 1})]), /布尔值/],
  ['invalid reference type', flat([node('root', null, {note: {body: 'x'}})]), /子笔记引用必须是文本/],
  ['invalid link title type', flat([node('root', null, {linkTitle: 0})]), /链接标题必须是文本/],
  ['negative index', flat([node('root', null, {index: -1})]), /非负整数/],
  ['fractional index', flat([node('root', null, {index: 0.5})]), /非负整数/],
  ['unsafe index', flat([node('root', null, {index: 1e20})]), /非负整数/],
  ['NUL text', flat([node('root', null, {content: 'bad\u0000text'})]), /无效字符/],
  ['lone surrogate', flat([node('root', null, {content: 'bad\ud800'})]), /无效字符/],
  ['long note', flat([node('root', null, {note: '文'.repeat(4000)})]), /4000/],
  ['invalid legacy child', {root: {text: 'root', children: ['lost node']}}, /无效的子节点/],
  ['invalid legacy children', {root: {text: 'root', children: {}}}, /children 必须是数组/],
  ['duplicate legacy ID', {root: {id: 'a', text: 'root', children: [{id: 'a', text: 'duplicate'}]}}, /重复节点/],
];
for (const [name, value, pattern] of invalidCases) test('reject ' + name, () => assert.throws(() => harness().parse(value), pattern));

test('300 nodes accepted; 301 rejected without partial conversion', () => {
  const h = harness();
  const data = flat([node(), ...Array.from({length: 299}, (_, i) => node('n' + i, 'root'))]);
  assert.equal(h.parse(data).doc.nodes.length, 300);
  data.nodes.push(node('overflow', 'root'));
  assert.throws(() => h.parse(data), /300/);
});

test('12 levels accepted; 13 rejected in flat and legacy formats', () => {
  const h = harness();
  const nodes = Array.from({length: 12}, (_, i) => node(i ? 'n' + i : 'root', i ? i === 1 ? 'root' : 'n' + (i - 1) : null));
  assert.equal(h.parse(flat(nodes)).depth, 12);
  nodes.push(node('n12', 'n11'));
  assert.throws(() => h.parse(flat(nodes)), /12 层/);
  const tree = {text: 'root', children: []}; let parent = tree;
  for (let i = 1; i < 13; i++) {const child = {text: 'child', children: []}; parent.children.push(child); parent = child;}
  assert.throws(() => h.parse({root: tree}), /12 层/);
});

test('title limits count Unicode codepoints, not UTF-16 units', () => {
  const h = harness();
  assert.equal(h.parse(flat([node('root', null, {content: '🌱'.repeat(200)})])).doc.title.length, 400);
  assert.throws(() => h.parse(flat([node('root', null, {content: '🌱'.repeat(201)})])), /200/);
});

test('parser and file reader enforce the byte limit before parsing or reading', async () => {
  const h = harness();
  assert.throws(() => h.parse(' '.repeat(4 * 1024 * 1024 + 1)), /4 MiB/);
  const form = h.open();
  await h.env.roadmapReadImportFile(form, file('', {size: 4 * 1024 * 1024 + 1, arrayBuffer: () => {throw Error('must not read');}}));
  assert.match(h.env.error, /4 MiB/);
  assert.equal(form.roadmapImport.result, null);
});

test('near-limit source cannot create a draft exceeding the API request byte limit', () => {
  const h = harness(), limit = 4 * 1024 * 1024;
  const data = count => flat(Array.from({length: 300}, (_, i) => node(i ? 'n' + i : 'root', i ? 'root' : null, {note: '🌱'.repeat(count)})));
  let low = 3000, high = 3500;
  while (low < high) {
    const mid = Math.ceil((low + high) / 2);
    if (Buffer.byteLength(JSON.stringify(data(mid))) <= limit) low = mid; else high = mid - 1;
  }
  const source = JSON.stringify(data(low));
  assert(Buffer.byteLength(source) <= limit);
  assert.throws(() => h.parse(source), /转换后的路线图超过 4 MiB/);
});

test('source whitespace follows server normalization, including U+0085', () => {
  const h = harness();
  assert.equal(h.parse(flat([node('root', null, {content: '\u0085'})])).doc.title, '导入的学习路线');
  assert.equal(h.parse(flat([node('root', null, {content: '\u0085 标题 \u0085'})])).doc.title, '标题');
});

test('UI preview is escaped, local-only, and confirmation creates a dirty new draft exactly once', async () => {
  const h = harness(), form = h.open();
  const text = JSON.stringify(flat([node('root', null, {content: '<img src=x onerror=alert(1)>'})]));
  await h.env.roadmapReadImportFile(form, file(text));
  assert.match(h.elements['#roadmap-import-preview'].innerHTML, /&lt;img/);
  assert.doesNotMatch(h.elements['#roadmap-import-preview'].innerHTML, /<img/);
  assert.equal(h.calls.length, 0);
  h.env.roadmapConfirmImport();
  assert.equal(h.eval('roadmapEditor.doc.id'), '');
  assert.equal(h.eval('roadmapEditor.doc.revision'), 0);
  assert.equal(h.eval('roadmapDirty()'), true);
  const first = h.eval('roadmapEditor');
  h.env.roadmapConfirmImport();
  assert.equal(h.eval('roadmapEditor'), first);
  assert.equal(h.calls.length, 0);
  await h.env.roadmapSave();
  assert.equal(h.calls.length, 1);
  assert.equal(h.calls[0].url, '/workspace/roadmaps');
  assert.equal(h.calls[0].options.method, 'POST');
  assert.equal(h.calls[0].options.body.revision, 0);
  assert.equal(h.eval('roadmapDirty()'), false);
});

test('file validation rejects non-JSON, empty and non-UTF8 files without opening a draft', async () => {
  for (const [sample, expected] of [[file(fixture, {name: 'map.png'}), /\.json/], [file(''), /为空/], [file('x', {arrayBuffer: async () => new Uint8Array([0xff]).buffer}), /UTF-8/]]) {
    const h = harness(), form = h.open();
    await h.env.roadmapReadImportFile(form, sample);
    assert.match(h.env.error, expected);
    h.env.roadmapConfirmImport();
    assert.equal(h.eval('roadmapEditor'), null);
  }
});

test('latest selected file wins and a failed selection clears the previous preview', async () => {
  const h = harness(), form = h.open(), delay = deferred();
  const first = h.env.roadmapReadImportFile(form, file(fixture, {arrayBuffer: () => delay.promise}));
  await h.env.roadmapReadImportFile(form, file(JSON.stringify(flat([node('root', null, {content: '最新文件'})]))));
  delay.resolve(new TextEncoder().encode(fixture).buffer); await first;
  assert.equal(form.roadmapImport.result.doc.title, '最新文件');
  await h.env.roadmapReadImportFile(form, file('{'));
  assert.equal(form.roadmapImport.result, null);
  assert.equal(h.elements['[data-action="roadmap-import-confirm"]'].disabled, true);
  h.env.roadmapConfirmImport();
  assert.equal(h.eval('roadmapEditor'), null);
});

for (const [name, invalidate] of [
  ['modal closed', h => h.env.closeModal()],
  ['account changed', h => {h.env.authEpoch++;}],
  ['logged out', h => {h.env.state.authenticated = false;}],
  ['route changed', h => {h.env.route = 'library';}],
  ['open request changed', h => h.eval('roadmapOpenRequest++')],
  ['another modal opened', h => h.open()],
]) test('asynchronous import ignored after ' + name, async () => {
  const h = harness(), form = h.open(), delay = deferred();
  const waiting = h.env.roadmapReadImportFile(form, file(fixture, {arrayBuffer: () => delay.promise}));
  invalidate(h); delay.resolve(new TextEncoder().encode(fixture).buffer); await waiting;
  assert.equal(form.roadmapImport.result, null);
  h.env.roadmapConfirmImport();
  assert.equal(h.eval('roadmapEditor'), null);
  assert.equal(h.calls.length, 0);
});

test('unsaved existing draft is preserved on cancel; confirm never retains its ID/revision or references', async () => {
  const h = harness();
  h.eval("roadmapStartEditor({...roadmapNewDocument(), id:'existing', revision:7}, true); roadmapEditor.doc.summary='未保存的内容';");
  const old = h.eval('roadmapEditor'), form = h.open();
  await h.env.roadmapReadImportFile(form, file());
  h.env.window.confirm = () => false;
  h.env.roadmapConfirmImport();
  assert.equal(h.eval('roadmapEditor'), old);
  assert.equal(old.doc.summary, '未保存的内容');
  h.env.window.confirm = () => true;
  h.env.roadmapConfirmImport();
  assert.equal(h.eval('roadmapEditor.doc.id'), '');
  assert.equal(h.eval('roadmapEditor.doc.revision'), 0);
  assert.equal(h.calls.length, 0);
});

test('saving or replacing an editor blocks stale import confirmation', async () => {
  const h = harness(); h.eval('roadmapStartEditor(roadmapNewDocument());');
  const form = h.open(); await h.env.roadmapReadImportFile(form, file());
  const old = h.eval('roadmapEditor');
  h.eval('roadmapEditor.saving = true'); h.env.roadmapConfirmImport();
  assert.equal(h.eval('roadmapEditor'), old);
  h.eval('roadmapStartEditor(roadmapNewDocument());');
  const next = h.eval('roadmapEditor'); h.env.roadmapConfirmImport();
  assert.equal(h.eval('roadmapEditor'), next);
});
