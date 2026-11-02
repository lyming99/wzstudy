'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');
const script = fs.readFileSync(path.join(__dirname, '../internal/adminui/app.js'), 'utf8');
const begin = script.indexOf('async function organizationModal(');
const end = script.indexOf('\nfunction renderCatalog(', begin);
assert(begin >= 0 && end > begin, 'organization UI handlers not embedded');
const handlers = script.slice(begin, end);
const itemId = '11111111-2222-4333-8444-555555555555';
const collectionId = 'aaaaaaaa-2222-4333-8444-555555555555';

function harness(view = 'library') {
  let form, markup = '';
  const writes = [];
  const fields = {
    '#organization-target': { value: '@choose' },
    '#organization-category': { value: '' },
    '#organization-mode': { value: 'learning' },
  };
  const row = { id: itemId, revision: 8, draft: { title: '待审核标题', category: '原分类' }, published: { revision: 5, title: '公开标题' } };
  const env = {
    authEpoch: 1, route: view, state: { authenticated: true },
    CONTENT_MODES:{default:'默认',news:'资讯',learning:'学习'},
    effectiveContentMode:value=>['default','news','learning'].includes(value)?value:'default',
    contentModeOptions:value=>'<option value="'+(value||'default')+'">模式</option>',
    selected: new Map([[itemId, { id: itemId, revision: 8 }]]),
    querySelected: new Map([[itemId, 5]]),
    getItem: () => ({ id: itemId, revision: 8 }),
    esc: value => String(value ?? '').replaceAll('"', '&quot;'),
    categories: () => ['原分类', '人工智能'],
    toast: () => {}, formError: message => { env.formErrorMessage = message; },
    openModal: (_title, _description, html) => { markup = html; form = { isConnected: true }; },
    closeModal: () => { form.isConnected = false; },
    refresh: async () => { env.refreshed = true; },
    loadQuery: async () => { env.queryReloaded = true; },
    $: selector => selector === '#organization-form' ? form : fields[selector],
    api: async (url, options) => {
      if (options?.method === 'POST') {
        writes.push(JSON.parse(JSON.stringify({ url, body: options.body })));
        return { moved: 1, category_updated: 1, collections_updated: 1 };
      }
      if (url.includes('/workspace/items/')) return structuredClone(row);
      if (url.includes('/workspace/catalog/collections?')) return {
        items: [{ id: collectionId, revision: 3, draft: { title: '目标合集', chapters: [] } }], total: 1,
      };
      throw new Error('Unexpected request: ' + url);
    },
  };
  vm.createContext(env);
  vm.runInContext(handlers, env);
  return { env, writes, row, fields, form: () => form, markup: () => markup };
}

test('move uses current item/collection revisions and updates selection only after success', async () => {
  const h = harness();
  await h.env.organizationModal('move', 'library', itemId);
  assert.match(h.markup(), /移动会从原有合集移除/);
  h.fields['#organization-target'].value = collectionId;
  await h.env.saveOrganization(h.form());
  assert.deepEqual(h.writes, [{ url: '/workspace/items/organize', body: {
    items: [{ id: itemId, revision: 8 }], collection_id: collectionId, collection_revision: 3,
  } }]);
  assert.equal(h.env.selected.size, 0);
  assert.equal(h.env.refreshed, true);
});

test('query selections resolve published revision to current management revision', async () => {
  const h = harness('query');
  await h.env.organizationModal('category', 'query');
  h.fields['#organization-category'].value = '  人工智能  ';
  await h.env.saveOrganization(h.form());
  assert.deepEqual(h.writes[0].body, { items: [{ id: itemId, revision: 8 }], category: '人工智能' });
  assert.equal(h.env.queryReloaded, true);
  assert.equal(h.env.refreshed, undefined);
});

test('empty category explicitly clears classification without any body/title fields', async () => {
  const h = harness();
  await h.env.organizationModal('category');
  await h.env.saveOrganization(h.form());
  assert.deepEqual(Object.keys(h.writes[0].body).sort(), ['category', 'items']);
  assert.equal(h.writes[0].body.category, '');
});

test('moving out is explicit; an unselected target never submits', async () => {
  const h = harness();
  await h.env.organizationModal('move');
  await h.env.saveOrganization(h.form());
  assert.equal(h.writes.length, 0);
  assert.match(h.env.formErrorMessage, /请选择目标合集/);
  h.fields['#organization-target'].value = '';
  await h.env.saveOrganization(h.form());
  assert.equal(h.writes[0].body.collection_id, '');
  assert.equal('collection_revision' in h.writes[0].body, false);
});

test('a stale public selection is rejected before opening a write form', async () => {
  const h = harness('query');
  h.row.published.revision = 6;
  await assert.rejects(h.env.organizationModal('category', 'query'), /所选资料已变化/);
  assert.equal(h.writes.length, 0);
  assert.equal(h.form(), undefined);
});

test('account or route change invalidates an open organization form', async () => {
  const h = harness();
  await h.env.organizationModal('category');
  h.env.authEpoch++;
  await assert.rejects(h.env.saveOrganization(h.form()), /账号或页面已切换/);
  assert.equal(h.writes.length, 0);
  assert.equal(h.env.selected.size, 1);
});


test('content mode is submitted independently and never overwrites category', async () => {
 const h=harness();await h.env.organizationModal('mode');await h.env.saveOrganization(h.form());
 assert.deepEqual(h.writes[0].body,{items:[{id:itemId,revision:8}],content_mode:'learning'});
});
