'use strict';
// WenzFlow desktop: 导出原始内容 -> UTF-8 JSON. Parse locally; only the
// converted draft is submitted through the existing, owner-scoped save API.
const ROADMAP_IMPORT_MAX_BYTES = 4 * 1024 * 1024;

function roadmapImportObject(value) {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

function roadmapImportText(value, field, required = false) {
  if (value == null && !required) return '';
  if (typeof value !== 'string') throw new Error(`${field}必须是文本。`);
  // JSON escapes can contain lone surrogates even in a valid UTF-8 file. Do
  // not let JSON.stringify / Go silently replace them when saving the draft.
  for (const ch of value) {
    const code = ch.codePointAt(0);
    if (code === 0 || (code >= 0xd800 && code <= 0xdfff)) {
      throw new Error(`${field}含有无效字符，请修正后重新导出。`);
    }
  }
  // Go's strings.TrimSpace also treats U+0085 as whitespace.
  const text = value.replace(/^[\s\u0085]+|[\s\u0085]+$/g, '');
  if (required && !text) throw new Error(`${field}不能为空。`);
  return text;
}

function roadmapImportBoolean(value, field) {
  if (value != null && typeof value !== 'boolean') throw new Error(`${field}必须是布尔值。`);
  return value;
}

function parseWenzflowRoadmap(source) {
  if (typeof source !== 'string' || new TextEncoder().encode(source).byteLength > ROADMAP_IMPORT_MAX_BYTES) {
    throw new Error('思维导图文件不能超过 4 MiB。');
  }
  let data;
  try {
    data = JSON.parse(source.replace(/^\uFEFF/, '').trim());
  } catch (_) {
    throw new Error('文件不是有效的 JSON，请在 WenzFlow 中选择“导出原始内容”。');
  }
  if (!roadmapImportObject(data)) throw new Error('请选择 WenzFlow 思维导图的原始 JSON 文件。');

  let rawNodes, primaryRoot = '', format;
  if (Object.hasOwn(data, 'nodes')) {
    if (!Array.isArray(data.nodes)) throw new Error('思维导图 nodes 必须是节点数组。');
    rawNodes = data.nodes;
    primaryRoot = roadmapImportText(data.primaryRootId, '中心节点 ID');
    format = 'WenzFlow 原生思维导图';
  } else if (roadmapImportObject(data.root) || Array.isArray(data.children)) {
    // WenzFlow also reads legacy root/children trees. Bound recursion before
    // descending and use internal IDs so omitted legacy IDs cannot collide.
    rawNodes = [];
    const sourceIDs = new Set();
    function visit(node, parentId, index, depth) {
      if (!roadmapImportObject(node)) throw new Error('思维导图包含无效的子节点。');
      if (depth > 12) throw new Error('路线图最多 12 层，请拆分思维导图后再导入。');
      if (rawNodes.length >= 300) throw new Error('一张路线图最多 300 个节点，请拆分后再导入。');
      if (node.id != null) {
        if (typeof node.id !== 'string' && !Number.isSafeInteger(node.id)) throw new Error('旧版节点 ID 必须是文本或整数。');
        const sourceID = roadmapImportText(String(node.id), '旧版节点 ID', true);
        if (sourceIDs.has(sourceID)) throw new Error('思维导图包含重复节点 ID。');
        sourceIDs.add(sourceID);
      }
      const uuid = 'legacy-' + rawNodes.length;
      roadmapImportBoolean(node.collapsed, '节点折叠状态');
      rawNodes.push({
        ...node, uuid, parentId, index,
        content: node.text ?? node.content ?? node.title ?? node.label,
        expand: node.collapsed !== true,
      });
      if (node.children != null && !Array.isArray(node.children)) throw new Error('节点 children 必须是数组。');
      (node.children || []).forEach((child, i) => visit(child, uuid, i, depth + 1));
    }
    visit(data.root || data, null, 0, 1);
    format = 'WenzFlow 旧版树形思维导图';
  } else {
    throw new Error('未找到 WenzFlow 思维导图节点。请使用“导出原始内容”的 JSON，不支持图片、PDF 或白板文件。');
  }
  if (!rawNodes.length) throw new Error('思维导图没有节点。');
  if (rawNodes.length > 300) throw new Error('一张路线图最多 300 个节点，请拆分后再导入。');

  const byID = new Map(), children = new Map(), warnings = new Set();
  const entries = rawNodes.map((raw, position) => {
    if (!roadmapImportObject(raw)) throw new Error(`第 ${position + 1} 个节点格式无效。`);
    const label = `第 ${position + 1} 个节点`;
    const sourceID = roadmapImportText(raw.uuid, label + ' ID', true);
    if (byID.has(sourceID)) throw new Error('思维导图包含重复节点 ID。');
    const parent = roadmapImportText(raw.parentId, label + '父节点 ID');
    if (raw.index != null && (!Number.isSafeInteger(raw.index) || raw.index < 0)) {
      throw new Error(`${label}的同级顺序 index 必须是非负整数。`);
    }
    const expand = roadmapImportBoolean(raw.expand, label + '展开状态');
    const todo = roadmapImportBoolean(raw.isTodo, label + '待办状态');
    const checked = roadmapImportBoolean(raw.isChecked, label + '完成状态');
    const content = roadmapImportText(raw.content, label + '名称');
    const image = roadmapImportText(raw.image, label + '图片引用');
    const formula = roadmapImportText(raw.formula, label + '公式');
    const nodeType = roadmapImportText(raw.nodeType, label + '类型');
    const title = content || (image || nodeType === 'image' ? '图片节点' : formula || nodeType === 'formula' ? '公式节点' : parent ? '未命名节点' : '导入的学习路线');
    if (!content) warnings.add('空名称已补为占位名称，请在编辑器中核对并修改。');
    if ([...title].length > 200) throw new Error(`${label}的名称超过 200 个字符，请缩短后重新导出。`);

    const noteParts = [];
    const addReference = (value, field, prefix) => {
      const text = roadmapImportText(value, label + field);
      if (text) noteParts.push(prefix + text);
      return text;
    };
    // Native `note` is a child-note ID list, NOT a note body or study item ID.
    if (addReference(raw.note, '子笔记引用', 'WenzFlow 子笔记引用：')) warnings.add('子笔记引用仅保留为说明文字，不会导入笔记正文或自动关联学习资料。');
    const link = addReference(raw.link, '链接', '原链接：');
    const linkTitle = addReference(raw.linkTitle, '链接标题', '链接标题：');
    if (link || linkTitle) {
      warnings.add('链接仅保留为说明文字，不会自动打开或采集。');
    }
    if (addReference(raw.wenzLink, '内部链接', 'WenzFlow 内部链接：')) warnings.add('WenzFlow 内部链接仅保留为说明文字，不会自动关联学习资料。');
    if (image) {
      noteParts.push('WenzFlow 图片引用（未导入附件）：' + image);
      warnings.add('图片仅保留节点和引用标识，不复制或下载附件。');
    }
    if (formula) {
      noteParts.push('公式：' + formula);
      warnings.add('公式以原始文本保留在学习说明中，不保留公式排版。');
    }
    if (todo) {
      noteParts.push('WenzFlow 待办：' + (checked ? '已完成' : '未完成'));
      warnings.add('待办状态已转为说明文字，不会写入个人学习进度。');
    }
    if (nodeType && !['text', 'image', 'formula'].includes(nodeType)) {
      noteParts.push('WenzFlow 节点类型：' + nodeType);
      warnings.add('特殊节点仅保留文字与结构，不保留原有交互。');
    }
    const note = noteParts.join('\n\n');
    if ([...note].length > 4000) throw new Error(`${label}转换后的学习说明超过 4000 个字符，请精简后重新导出。`);
    const entry = {
      sourceID, parent, index: raw.index ?? 0, position,
      node: {id: roadmapUUID(), parent_id: '', title, note, item_ids: [], collapsed: expand === false},
    };
    byID.set(sourceID, entry);
    if (!children.has(parent)) children.set(parent, []);
    children.get(parent).push(entry);
    return entry;
  });

  const roots = children.get('') || [];
  if (roots.length !== 1) throw new Error('思维导图必须且只能有一个中心节点。');
  const root = roots[0];
  if (primaryRoot && primaryRoot !== root.sourceID) throw new Error('primaryRootId 与实际中心节点不一致，请修正后重新导出。');
  let maxDepth = 1;
  // Check every chain, including disconnected cycles which a root-only walk
  // would miss. At most 300 nodes / 12 levels; never discard unreachable data.
  for (const entry of entries) {
    const seen = new Set([entry.sourceID]);
    let parent = entry.parent, depth = 1;
    while (parent) {
      if (seen.has(parent)) throw new Error('思维导图包含循环父子关系。');
      seen.add(parent);
      const ancestor = byID.get(parent);
      if (!ancestor) throw new Error('思维导图包含找不到父节点的孤立节点。');
      if (++depth > 12) throw new Error('路线图最多 12 层，请拆分思维导图后再导入。');
      parent = ancestor.parent;
    }
    maxDepth = Math.max(maxDepth, depth);
    entry.depth = depth;
    entry.node.parent_id = entry.parent ? byID.get(entry.parent).node.id : '';
  }
  for (const siblings of children.values()) siblings.sort((a, b) => a.index - b.index || a.position - b.position);
  const ordered = [];
  function emit(entry) {
    ordered.push(entry);
    (children.get(entry.sourceID) || []).forEach(emit);
  }
  emit(root);
  const doc = {id: '', revision: 0, title: root.node.title, summary: '', nodes: ordered.map(entry => entry.node)};
  // New UUIDs and reference labels can make the saved request larger than
  // the source file. Validate against the existing API's 4 MiB body limit.
  if (new TextEncoder().encode(JSON.stringify(roadmapPayload(doc))).byteLength > ROADMAP_IMPORT_MAX_BYTES) {
    throw new Error('转换后的路线图超过 4 MiB 保存上限，请拆分或精简思维导图后再导入。');
  }
  return {doc, format, depth: maxDepth, depths: ordered.map(entry => entry.depth), warnings: [...warnings]};
}

function roadmapImportCurrent(form, session) {
  return !!session && !!form?.isConnected && form.roadmapImport === session && session.epoch === authEpoch &&
    state.authenticated && route === 'roadmaps' && session.editor === roadmapEditor &&
    session.openRequest === roadmapOpenRequest;
}

function roadmapImportDialog() {
  if (!state.authenticated || route !== 'roadmaps' || roadmapEditor?.saving) return;
  openModal('导入 WenzFlow 思维导图', '导入为新的学习路线图，不覆盖已有路线图。', `
    <form id="roadmap-import-form">
      <div id="form-error"></div>
      <div class="field"><label for="roadmap-import-file">选择思维导图 JSON 文件</label>
        <input id="roadmap-import-file" type="file" accept=".json,application/json" aria-describedby="roadmap-import-help">
        <p id="roadmap-import-help" class="field-note">在 WenzFlow 桌面端打开思维导图，选择“导出、打印或分享 → 导出原始内容”。最多 4 MiB、300 个节点、12 层。</p>
      </div>
      <p class="field-note">文件只在浏览器内解析。确认后先打开未保存草稿，点击“保存路线图”才写入服务端。节点颜色、字体和画布布局不导入；资料需在导入后手动关联。</p>
      <div id="roadmap-import-preview" class="roadmap-import-preview" aria-live="polite">选择文件后显示结构预览。</div>
    </form>`, '<button class="btn" data-action="close-modal">取消</button><button class="btn primary" data-action="roadmap-import-confirm" disabled>作为新路线图打开</button>', {icon: 'upload', className: 'roadmap-import-dialog'});
  const form = $('#roadmap-import-form');
  form.roadmapImport = {epoch: authEpoch, editor: roadmapEditor, openRequest: roadmapOpenRequest, sequence: 0, result: null};
  $('#roadmap-import-file').addEventListener('change', event => roadmapReadImportFile(form, event.target.files?.[0]));
  form.addEventListener('submit', event => {event.preventDefault(); roadmapConfirmImport();});
}

async function roadmapReadImportFile(form, file) {
  const session = form.roadmapImport;
  if (!roadmapImportCurrent(form, session)) return;
  const sequence = ++session.sequence;
  session.result = null;
  const button = $('[data-action="roadmap-import-confirm"]'), preview = $('#roadmap-import-preview');
  button.disabled = true;
  $('#form-error').replaceChildren();
  preview.textContent = file ? '正在读取并校验思维导图…' : '选择文件后显示结构预览。';
  const current = () => roadmapImportCurrent(form, session) && sequence === session.sequence;
  if (!file) return;
  try {
    if (!/\.json$/i.test(file.name)) throw new Error('请选择“导出原始内容”生成的 .json 文件。');
    if (!file.size) throw new Error('文件为空，请重新导出思维导图。');
    if (file.size > ROADMAP_IMPORT_MAX_BYTES) throw new Error('思维导图文件不能超过 4 MiB。');
    const buffer = await file.arrayBuffer();
    if (!current()) return;
    let source;
    try { source = new TextDecoder('utf-8', {fatal: true}).decode(buffer); }
    catch (_) { throw new Error('文件不是有效的 UTF-8 文本，请在 WenzFlow 中重新导出。'); }
    const result = parseWenzflowRoadmap(source);
    if (!current()) return;
    session.result = result;
    preview.innerHTML = `<h3>${esc(result.doc.title)}</h3><p>${esc(result.format)} · ${result.doc.nodes.length} 个节点 · ${result.depth} 层</p>
      ${result.warnings.length ? `<ul class="roadmap-import-warnings">${result.warnings.map(text => `<li>${esc(text)}</li>`).join('')}</ul>` : ''}
      <ol class="roadmap-import-tree" aria-label="导入节点预览">${result.doc.nodes.map((node, i) => `<li style="--node-depth:${result.depths[i] - 1}"><span>${esc(node.title)}</span>${node.collapsed ? '<small>已折叠</small>' : ''}</li>`).join('')}</ol>`;
    button.disabled = false;
  } catch (error) {
    if (!current()) return;
    preview.textContent = '未导入任何内容，请检查文件后重新选择。';
    formError(error.message || '读取文件失败，请重新选择。');
  }
}

function roadmapConfirmImport() {
  const form = $('#roadmap-import-form'), session = form?.roadmapImport;
  if (!session || !roadmapImportCurrent(form, session) || !session.result || roadmapEditor?.saving) return;
  if (roadmapDirty() && !window.confirm('打开导入的路线图会放弃当前未保存的修改，是否继续？')) return;
  const doc = session.result.doc;
  session.result = null;
  roadmapOpenRequest++;
  roadmapListRequest++;
  closeModal();
  roadmapStartEditor(doc, false);
  toast('思维导图已导入为新草稿，请核对并保存路线图。');
}
