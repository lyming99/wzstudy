'use strict';

// 采集任务板块：派发采集目标、跟踪领取与入库、取消与重试。
// 仅管理员可见；接口统一在 /api/v1/admin/agent 下，数据仍写入任务绑定的来源。
const tasksView = (() => {
  let requestSerial = 0;
  let detailSerial = 0;
  let filter = 'all';
  let snapshot = null;
  let sources = [];
  let agents = [];

  const STATUS = {
    pending: { label: '待领取', tone: 'warn' },
    claimed: { label: '已领取', tone: 'info' },
    running: { label: '执行中', tone: 'info' },
    succeeded: { label: '已完成', tone: 'ok' },
    failed: { label: '失败', tone: 'error' },
    cancelled: { label: '已取消', tone: 'muted' },
    expired: { label: '已过期', tone: 'error' },
  };
  const FILTERS = [
    ['all', '全部'],
    ['active', '进行中'],
    ['succeeded', '已完成'],
    ['attention', '异常'],
  ];
  const EVENT_LABELS = {
    round_start: '轮次',
    assistant: '模型输出',
    tool_call: '调用工具',
    tool_result: '工具结果',
    notice: '提示',
    finish: '结束',
  };

  const date = value => {
    if (!value) return '—';
    const parsed = new Date(value);
    if (Number.isNaN(parsed.getTime())) return '—';
    return new Intl.DateTimeFormat('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }).format(parsed);
  };
  const clip = (value, maximum) => {
    const text = String(value || '').trim();
    return text.length > maximum ? text.slice(0, maximum) + '…' : text;
  };
  const getName = (list, id, fallback) => list.find(item => item.id === id)?.name || (id ? id.slice(0, 8) : fallback);
  const sourceName = id => getName(sources, id, '未知来源');
  const agentName = id => getName(agents, id, '任意采集端');
  const ingested = task => Number(task.stats?.items_created || 0) + Number(task.stats?.items_updated || 0) + Number(task.stats?.items_unchanged || 0);
  const quota = task => Number(task.constraints?.max_items || 0);

  function badge(status) {
    const value = STATUS[status] || { label: status || '未知', tone: 'muted' };
    return `<span class="task-badge ${value.tone}">${value.label}</span>`;
  }

  function metrics(status) {
    const counts = status?.tasks || {};
    const cards = [
      ['待领取', counts.pending || 0, 'clock', 'warn'],
      ['执行中', Number(counts.claimed || 0) + Number(counts.running || 0), 'refresh', 'info'],
      ['已完成', counts.succeeded || 0, 'check', 'ok'],
      ['失败 / 过期', Number(counts.failed || 0) + Number(counts.expired || 0), 'info', 'error'],
    ];
    return `<div class="task-metrics">${cards.map(([label, value, symbol, tone]) => `
      <article class="task-metric">
        <span class="task-metric-icon ${tone}">${icon(symbol)}</span>
        <span class="task-metric-label">${label}</span>
        <strong>${Number(value).toLocaleString('zh-CN')}</strong>
      </article>`).join('')}</div>`;
  }

  function matches(task, value) {
    if (value === 'all') return true;
    if (value === 'active') return ['pending', 'claimed', 'running'].includes(task.status);
    if (value === 'attention') return ['failed', 'expired', 'cancelled'].includes(task.status);
    return task.status === value;
  }

  function row(task) {
    const cancelable = ['pending', 'claimed', 'running'].includes(task.status);
    const retryable = ['failed', 'cancelled', 'expired'].includes(task.status);
    const detail = task.result?.error?.message || task.cancel_reason || '';
    return `<tr>
      <td data-label="状态">${badge(task.status)}</td>
      <td data-label="任务">
        <strong>${esc(task.title)}</strong>
        <small>${esc(clip(task.goal, 110))}</small>
        ${detail ? `<small class="task-error-line">${esc(clip(detail, 90))}</small>` : ''}
      </td>
      <td data-label="来源">${esc(sourceName(task.source_id))}</td>
      <td data-label="进度">${ingested(task)} / ${quota(task)}${task.attempts ? ` · 第 ${task.attempts} 次` : ''}</td>
      <td data-label="采集端">${esc(agentName(task.agent_id))}</td>
      <td data-label="更新">${date(task.updated_at)}</td>
      <td class="task-actions">
        <button class="text-btn" data-task-action="detail" data-id="${esc(task.id)}">详情</button>
        ${cancelable ? `<button class="text-btn danger" data-task-action="cancel" data-id="${esc(task.id)}">取消</button>` : ''}
        ${retryable ? `<button class="text-btn" data-task-action="retry" data-id="${esc(task.id)}">重试</button>` : ''}
      </td>
    </tr>`;
  }

  function render() {
    const root = $('#main-content');
    if (!root || !snapshot) return;
    const status = snapshot.status || {};
    const tasks = snapshot.tasks || [];
    const visible = tasks.filter(task => matches(task, filter));
    const enabled = status.enabled !== false;
    root.innerHTML = `<section class="task-page" data-task-page>
      ${heading('采集任务', '把采集目标派发给采集端；采集端领取后抓取、整理并写入任务绑定的来源。', `<button class="btn primary" data-task-action="dispatch" ${enabled ? '' : 'disabled'}>${icon('plus')}派发任务</button>`, enabled ? '已启用' : '未启用')}
      ${enabled ? '' : `<div class="task-alert">${icon('info')}<span>采集模块当前未启用。请先在服务端设置 <code>AGENT_ENABLED=true</code> 并重启，然后刷新此页面。</span></div>`}
      ${metrics(status)}
      <div class="task-toolbar">
        <nav class="section-tabs" aria-label="任务筛选">${FILTERS.map(([key, label]) => `<button data-task-filter="${key}" class="${filter === key ? 'active' : ''}" ${filter === key ? 'aria-current="page"' : ''}>${label}</button>`).join('')}</nav>
        <div class="task-toolbar-actions">
          <span>共 ${Number(snapshot.total || 0).toLocaleString('zh-CN')} 个任务 · 采集端 ${Number(agents.length).toLocaleString('zh-CN')} 个</span>
          <button class="btn small-btn" data-task-action="reload">${icon('refresh')}刷新</button>
        </div>
      </div>
      <div class="panel task-list">
        ${visible.length ? `<table class="task-table">
          <thead><tr><th>状态</th><th>任务</th><th>来源</th><th>入库 / 配额</th><th>采集端</th><th>更新</th><th>操作</th></tr></thead>
          <tbody>${visible.map(row).join('')}</tbody>
        </table>` : empty('没有符合条件的任务', filter === 'all' ? '点击“派发任务”创建第一个采集目标。' : '切换筛选或刷新后再看。')}
      </div>
      <p class="task-boundary">${icon('shield')}任务只能写入创建时绑定的来源；配额、允许域名与字段长度由服务端二次校验。采集端只领取任务，不能读取资料库。</p>
    </section>`;
  }

  async function load() {
    if (!state.authenticated || state.role !== 'admin') return;
    const seq = ++requestSerial;
    const root = $('#main-content');
    if (root && !snapshot) {
      root.innerHTML = heading('采集任务', '正在读取任务与采集端状态…', '') + `<div class="panel task-list">${empty('正在读取…', '请稍候。')}</div>`;
    }
    if (root) root.setAttribute('aria-busy', 'true');
    try {
      const [status, list, sourceList, agentList] = await Promise.all([
        api('/admin/agent/status'),
        api('/admin/agent/tasks?limit=50'),
        api('/workspace/sources'),
        api('/admin/agent/agents'),
      ]);
      if (seq !== requestSerial || !state.authenticated) return;
      snapshot = { status, tasks: list.tasks || [], total: list.total || 0 };
      sources = Array.isArray(sourceList) ? sourceList : [];
      agents = Array.isArray(agentList) ? agentList : [];
      render();
    } catch (error) {
      if (seq !== requestSerial) return;
      if (root) root.innerHTML = empty('无法读取采集任务', esc(error.message), '<button class="btn small-btn" data-task-action="reload">重新加载</button>');
      showError(error);
    } finally {
      if (root && seq === requestSerial) root.removeAttribute('aria-busy');
    }
  }

  function dispatchModal() {
    if (!sources.length) {
      openModal('先准备采集来源', '任务必须绑定一个启用中的来源，采集端只能写入该来源。', empty('还没有可用来源', '请到“数据录入 → 录入端管理”创建一个手动来源，或确认现有来源已启用且允许 ingest。'), '<button class="btn" data-action="close-modal">知道了</button>', { icon: 'info' });
      return;
    }
    openModal('派发采集任务', '采集端领取后按目标抓取并整理，写入创建时选择的来源。', `
      <form data-task-form="dispatch" class="task-form" novalidate>
        <label class="task-field">采集来源<span class="task-required">*</span>
          <select name="source_id" required>
            <option value="">请选择来源</option>
            ${sources.map(item => `<option value="${esc(item.id)}">${esc(item.name)}${item.enabled === false ? '（已停用）' : ''}</option>`).join('')}
          </select>
        </label>
        <label class="task-field">采集目标<span class="task-required">*</span>
          <textarea name="goal" rows="3" maxlength="4000" required placeholder="例如：采集 Go 官方博客最近三篇文章，整理成中文学习资料并附原文链接"></textarea>
        </label>
        <label class="task-field">任务标题
          <input name="title" maxlength="200" placeholder="留空时自动截取采集目标">
        </label>
        <div class="task-form-grid">
          <label class="task-field">最多入库<input name="max_items" type="number" min="1" max="100" value="5"></label>
          <label class="task-field">最大轮次<input name="max_rounds" type="number" min="1" max="32" value="8"></label>
          <label class="task-field">工具调用上限<input name="max_tool_calls" type="number" min="1" max="128" value="24"></label>
          <label class="task-field">超时（秒）<input name="timeout_seconds" type="number" min="30" max="1800" value="300"></label>
          <label class="task-field">优先级<input name="priority" type="number" min="-10" max="10" value="0"></label>
          <label class="task-field">指定采集端
            <select name="assigned_agent_id"><option value="">任意采集端</option>${agents.map(item => `<option value="${esc(item.id)}">${esc(item.name)}</option>`).join('')}</select>
          </label>
        </div>
        <div class="task-form-grid">
          <label class="task-field">语言<input name="language" maxlength="35" placeholder="zh-CN"></label>
          <label class="task-field">分类<input name="category" maxlength="200" placeholder="留空沿用来源分类"></label>
        </div>
        <label class="task-field">标签<input name="tags" placeholder="用逗号分隔，最多 20 个"></label>
        <label class="task-field">允许域名<input name="allow_domains" placeholder="用逗号分隔，如 go.dev, blog.golang.org；留空不限（仍禁止内网）"></label>
        <p class="task-hint">${icon('shield')}配额、允许域名与条目字段由服务端再次校验；采集端不能把数据写入其他来源。</p>
      </form>`,
      '<button class="btn" data-action="close-modal">取消</button><button class="btn primary" data-task-action="dispatch-submit">派发任务</button>',
      { icon: 'upload', className: 'task-dialog' });
  }

  const splitList = value => String(value || '').split(',').map(item => item.trim()).filter(Boolean);
  const numberValue = (data, name, fallback) => {
    const parsed = Number(data.get(name));
    return Number.isFinite(parsed) && parsed >= 0 ? parsed : fallback;
  };
  const integerValue = (data, name, fallback) => {
    const raw = String(data.get(name) || '').trim();
    if (!raw) return fallback;
    const parsed = Number(raw);
    return Number.isInteger(parsed) ? parsed : fallback;
  };

  async function submitDispatch(form) {
    const data = new FormData(form);
    const button = $('[data-task-action="dispatch-submit"]');
    const body = {
      source_id: String(data.get('source_id') || ''),
      title: String(data.get('title') || '').trim(),
      goal: String(data.get('goal') || '').trim(),
      constraints: {
        max_items: numberValue(data, 'max_items', 5),
        language: String(data.get('language') || '').trim(),
        category: String(data.get('category') || '').trim(),
        tags: splitList(data.get('tags')),
        allow_domains: splitList(data.get('allow_domains')),
      },
      budget: {
        max_rounds: numberValue(data, 'max_rounds', 8),
        max_tool_calls: numberValue(data, 'max_tool_calls', 24),
        timeout_seconds: numberValue(data, 'timeout_seconds', 300),
      },
      priority: integerValue(data, 'priority', 0),
      assigned_agent_id: String(data.get('assigned_agent_id') || ''),
    };
    if (!body.source_id) { toast('请选择采集来源。', true); return; }
    if (!body.goal) { toast('请填写采集目标。', true); return; }
    if (body.goal.length > 4000) { toast('采集目标不能超过 4000 字节。', true); return; }
    if (button) button.disabled = true;
    try {
      await api('/admin/agent/tasks', { method: 'POST', body });
      closeModal();
      toast('任务已派发，等待采集端领取。');
      filter = 'all';
      await load();
    } catch (error) {
      toast(error.message, true);
    } finally {
      if (button && button.isConnected) button.disabled = false;
    }
  }

  function detailMarkup(task, events) {
    const stats = task.stats || {};
    const error = task.result?.error;
    const rows = [
      ['新增', stats.items_created || 0],
      ['更新', stats.items_updated || 0],
      ['未变', stats.items_unchanged || 0],
      ['轮次', stats.rounds || 0],
      ['工具调用', stats.tool_calls || 0],
      ['耗时', stats.duration_ms ? `${stats.duration_ms} ms` : '—'],
    ];
    return `<div class="task-detail">
      <div class="task-detail-head">
        ${badge(task.status)}
        <span>来源：${esc(sourceName(task.source_id))}</span>
        <span>采集端：${esc(agentName(task.agent_id))}</span>
        <span>尝试：${Number(task.attempts || 0)} / ${Number(task.max_attempts || 0)}</span>
      </div>
      <p class="task-detail-goal">${esc(task.goal)}</p>
      <div class="task-detail-stats">${rows.map(([label, value]) => `<div><span>${label}</span><strong>${Number(value).toLocaleString('zh-CN')}</strong></div>`).join('')}</div>
      ${error ? `<div class="task-alert error">${icon('info')}<span>${esc(error.code)}：${esc(error.message)}</span></div>` : ''}
      ${task.cancel_requested ? `<div class="task-alert">${icon('info')}<span>已请求取消${task.cancel_reason ? `：${esc(task.cancel_reason)}` : ''}，等待采集端在下次心跳时停止。</span></div>` : ''}
      <h3 class="task-detail-title">进度事件</h3>
      ${events === null ? '<p class="task-hint">正在读取事件…</p>' : events.length ? `<ol class="task-events">${events.map(event => `<li>
        <span class="task-event-kind ${esc(event.kind)}">${EVENT_LABELS[event.kind] || esc(event.kind)}</span>
        <span class="task-event-text">${esc(clip(event.text || event.summary || event.tool || '', 280))}</span>
        <time>${date(event.at)}</time>
      </li>`).join('')}</ol>` : empty('暂无事件', '采集端领取任务后会逐步上报轮次与工具调用。')}
    </div>`;
  }

  async function showDetail(id) {
    const task = (snapshot?.tasks || []).find(item => item.id === id);
    if (!task) return;
    const seq = ++detailSerial;
    openModal('任务详情', esc(task.title), `<div id="task-detail-root">${detailMarkup(task, null)}</div>`, '<button class="btn" data-action="close-modal">关闭</button>', { icon: 'layers', className: 'task-dialog' });
    try {
      const events = await api('/admin/agent/tasks/' + encodeURIComponent(id) + '/events');
      const root = $('#task-detail-root');
      if (root && seq === detailSerial) root.innerHTML = detailMarkup(task, Array.isArray(events) ? events : []);
    } catch (error) {
      const root = $('#task-detail-root');
      if (root && seq === detailSerial) root.innerHTML = detailMarkup(task, []) + `<p class="task-hint">事件读取失败：${esc(error.message)}</p>`;
    }
  }

  async function cancelTask(id) {
    const task = (snapshot?.tasks || []).find(item => item.id === id);
    if (!task) return;
    if (!window.confirm(`取消任务「${task.title}」？运行中的任务会在采集端下次心跳时停止。`)) return;
    try {
      await api('/admin/agent/tasks/' + encodeURIComponent(id) + '/cancel', { method: 'POST', body: { reason: '管理员取消' } });
      toast(task.status === 'pending' ? '任务已取消。' : '已请求取消，等待采集端停止。');
      await load();
    } catch (error) {
      toast(error.message, true);
    }
  }

  async function retryTask(id) {
    const task = (snapshot?.tasks || []).find(item => item.id === id);
    if (!task) return;
    if (!window.confirm(`把任务「${task.title}」重新放回待领取队列？`)) return;
    try {
      await api('/admin/agent/tasks/' + encodeURIComponent(id) + '/retry', { method: 'POST', body: {} });
      toast('任务已重置为待领取。');
      await load();
    } catch (error) {
      toast(error.message, true);
    }
  }

  function bindEvents() {
    document.addEventListener('submit', event => {
      const form = event.target.closest('form[data-task-form="dispatch"]');
      if (!form) return;
      event.preventDefault();
      submitDispatch(form).catch(error => toast(error.message, true));
    });
    document.addEventListener('click', async event => {
      const filterButton = event.target.closest('[data-task-filter]');
      if (filterButton) {
        filter = filterButton.dataset.taskFilter;
        render();
        return;
      }
      const button = event.target.closest('[data-task-action]');
      if (!button) return;
      const action = button.dataset.taskAction;
      const id = button.dataset.id;
      try {
        if (action === 'dispatch') dispatchModal();
        else if (action === 'dispatch-submit') {
          const form = $('#modal-root form[data-task-form="dispatch"]');
          if (form) await submitDispatch(form);
        } else if (action === 'reload') await load();
        else if (action === 'detail') await showDetail(id);
        else if (action === 'cancel') await cancelTask(id);
        else if (action === 'retry') await retryTask(id);
      } catch (error) {
        toast(error.message, true);
      }
    });
  }

  bindEvents();

  return { render, load };
})();
