'use strict';

// Plans share the task editor/configuration snapshot, but never its execution state.
const collectionPlansView = (() => {
  let plans = [];
  let plansEpoch = null;
  let plansRole = null;
  let serial = 0;
  let historySerial = 0;
  let showArchived = false;
  const base = () => (state.role === 'admin' ? '/admin/agent' : '/workspace/agent') + '/collection-plans';
  const endpoint = id => base() + '/' + encodeURIComponent(id);
  async function request(path, options) {
    const epoch = typeof authEpoch === 'undefined' ? 0 : authEpoch;
    const role = state.role;
    const result = await api(path, options);
    if (!state.authenticated || role !== state.role || epoch !== (typeof authEpoch === 'undefined' ? 0 : authEpoch) || (typeof route !== 'undefined' && route !== 'tasks')) throw new Error('页面状态已变化，已忽略旧响应。');
    return result;
  }

  const KINDS = { manual: '手动触发', once: '单次定时', interval: '固定间隔', daily: '每天', weekly: '每周', unread_inventory: '未读存量条件', webhook: 'API / Webhook' };
  const UNREAD_OPERATORS = { lt: '低于（<）', lte: '不高于（≤）', eq: '等于（=）', gte: '不低于（≥）', gt: '高于（>）' };
  const UNREAD_SYMBOLS = { lt: '<', lte: '≤', eq: '=', gte: '≥', gt: '>' };
  const unreadRule = condition => `未读存量 ${UNREAD_SYMBOLS[condition?.operator] || '?'} ${Number(condition?.threshold ?? 0)} 条`;
  const REASONS = { unread_condition_not_met: '未读存量条件未满足，本次未派发', overlap: '已有未结束任务，本次跳过', misfire_window_exceeded: '超过补跑窗口，已跳过', source_or_agent_unavailable: '来源或指定采集端不可用', invalid_task_configuration: '配置超过当前限制，请编辑计划后再试' };
  const formatTime = (value, zone) => {
    if (!value) return '—';
    try { return new Intl.DateTimeFormat('zh-CN', { timeZone: zone || undefined, year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit' }).format(new Date(value)); } catch { return String(value); }
  };
  // datetime-local is a wall-clock value. Resolve it only in the explicit plan
  // timezone; Date's implicit browser timezone must never affect the schedule.
  const pickerFormatter = zone => {
    try { return new Intl.DateTimeFormat('en-GB', { timeZone: zone, calendar: 'gregory', numberingSystem: 'latn', year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23' }); }
    catch { throw new Error('计划时区无效，请填写 UTC 或 Asia/Shanghai 等有效时区。'); }
  };
  function zonedWallTime(value, formatter) {
    const date = new Date(value);
    const parts = Object.fromEntries(formatter.formatToParts(date).map(part => [part.type, part.value]));
    return `${parts.year.padStart(4,'0')}-${parts.month}-${parts.day}T${parts.hour}:${parts.minute}:${parts.second}.${String(date.getUTCMilliseconds()).padStart(3,'0')}`;
  }
  const pickerValue = (value, zone) => value ? zonedWallTime(value, pickerFormatter(zone)).replace(/\.000$/,'').replace(/:00$/,'') : '';
  function offsetLabel(offset) {
    if (!offset) return 'UTC';
    const seconds = Math.round(Math.abs(offset) / 1000);
    return `UTC${offset < 0 ? '-' : '+'}${String(Math.floor(seconds / 3600)).padStart(2,'0')}:${String(Math.floor(seconds / 60) % 60).padStart(2,'0')}${seconds % 60 ? ':' + String(seconds % 60).padStart(2,'0') : ''}`;
  }
  function pickerCandidates(value, zone) {
    const match = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})(?::(\d{2}))?$/.exec(value);
    if (!match || match[1] === '0000') throw new Error('请选择有效、完整的单次执行日期和时间。');
    const local = `${match[1]}-${match[2]}-${match[3]}T${match[4]}:${match[5]}:${match[6] || '00'}.000`;
    const wall = new Date(local + 'Z').getTime();
    if (!Number.isFinite(wall) || new Date(wall).toISOString() !== local + 'Z') throw new Error('请选择有效、完整的单次执行日期和时间。');
    const formatter = pickerFormatter(zone);
    // Sample both sides of a possible DST/offset transition, then round-trip
    // every candidate. Gaps have none; repeated times retain both instants.
    const offsets = new Set([-36,-12,0,12,36].map(hours => {
      const sample = wall + hours * 3600000;
      return new Date(zonedWallTime(sample,formatter) + 'Z').getTime() - sample;
    }));
    return [...offsets].map(offset => ({ at: new Date(wall - offset).toISOString(), label: offsetLabel(offset) }))
      .filter(candidate => zonedWallTime(candidate.at,formatter) === local)
      .sort((a,b) => a.at.localeCompare(b.at));
  }

  // randomUUID is unavailable on plain-HTTP LAN pages. This is an idempotency
  // label, not a credential; keep manual runs usable there as well.
  const newEventID = () => globalThis.crypto?.randomUUID ? crypto.randomUUID() : 'ui-' + Date.now().toString(36) + '-' + Math.random().toString(36).slice(2) + '-' + Math.random().toString(36).slice(2);
  const tabs = active => `<nav class="section-tabs collection-view-tabs" aria-label="采集任务视图"><button data-task-view="tasks" class="${active === 'tasks' ? 'active' : ''}">执行任务</button><button data-task-view="plans" class="${active === 'plans' ? 'active' : ''}">采集计划</button></nav>`;

  function describe(plan) {
    const schedule = plan.schedule || {};
    if (schedule.kind === 'unread_inventory') return `${unreadRule(schedule.unread_condition)} · 每 ${schedule.interval_minutes} 分钟检查`;
    if (schedule.kind === 'interval') return `每 ${schedule.interval_minutes} 分钟`;
    if (schedule.kind === 'daily') return `每天 ${schedule.time_of_day}`;
    if (schedule.kind === 'weekly') return `每周 ${(schedule.weekdays || []).map(day => '日一二三四五六'[day]).join('、')} ${schedule.time_of_day}`;
    if (schedule.kind === 'once') return formatTime(schedule.at, schedule.timezone);
    return KINDS[schedule.kind] || schedule.kind;
  }

  function render() {
    const root = $('#main-content'); if (!root) return;
    const epoch = typeof authEpoch === 'undefined' ? 0 : authEpoch;
    if (plansEpoch !== epoch || plansRole !== state.role) { plans = []; plansEpoch = epoch; plansRole = state.role; }
    const visible = plans.filter(plan => showArchived || !plan.deleted_at);
    root.innerHTML = `<section class="task-page" data-task-page>
      ${heading('采集计划', '定时或收到事件后生成独立执行任务；沿用当前工作区的采集权限与入库规则。', '<button class="btn primary" data-plan-action="create">'+icon('plus')+'新建计划</button>')}
      ${tabs('plans')}
      <div class="task-toolbar"><label><input type="checkbox" data-plan-archived ${showArchived ? 'checked' : ''}> 显示已归档计划</label><div class="task-toolbar-actions"><span>共 ${visible.length} 个计划</span><button class="btn small-btn" data-plan-action="reload">${icon('refresh')}刷新</button></div></div>
      <div class="panel task-list">${visible.length ? `<table class="task-table collection-plan-table"><thead><tr><th>计划</th><th>状态 / 触发方式</th><th>下次执行 / 检查</th><th>最近触发 / 检查</th><th>操作</th></tr></thead><tbody>${visible.map(plan => `<tr>
        <td data-label="计划"><strong>${esc(plan.name)}</strong><small>${esc(plan.task?.title || plan.task?.goal || '')}</small></td>
        <td data-label="触发"><span class="task-badge ${plan.deleted_at ? 'muted' : plan.enabled ? 'ok' : 'warn'}">${plan.deleted_at ? '已归档' : plan.enabled ? '已启用' : '已暂停'}</span><small>${esc(describe(plan))}</small><small>${esc(plan.schedule?.timezone || 'UTC')}</small></td>
        <td data-label="下次执行 / 检查">${esc(formatTime(plan.next_run_at, plan.schedule?.timezone))}${plan.schedule?.kind === 'unread_inventory' ? '<small>检查条件后决定是否采集</small>' : ''}</td><td data-label="最近触发 / 检查">${esc(formatTime(plan.last_unread_check?.checked_at || plan.last_run_at, plan.schedule?.timezone))}${plan.last_unread_check ? `<small>未读 ${Number(plan.last_unread_check.unread_count)} 条 · ${plan.last_unread_check.matched ? '条件满足' : '条件未满足'}</small>` : ''}</td>
        <td class="task-actions"><button class="text-btn" data-plan-action="history" data-id="${esc(plan.id)}">运行历史</button>${plan.deleted_at ? '' : `<button class="text-btn" data-plan-action="edit" data-id="${esc(plan.id)}">编辑</button><button class="text-btn" data-plan-action="toggle" data-id="${esc(plan.id)}">${plan.enabled ? '暂停' : '启用'}</button><button class="text-btn" data-plan-action="run" data-id="${esc(plan.id)}" ${plan.enabled ? '' : 'disabled'}>${plan.schedule?.kind === 'unread_inventory' ? '检查并运行' : '运行一次'}</button>${plan.schedule?.kind === 'webhook' ? `<button class="text-btn" data-plan-action="key" data-id="${esc(plan.id)}">触发凭据${plan.trigger_key_set ? '（已设置）' : ''}</button>` : ''}<button class="text-btn danger" data-plan-action="delete" data-id="${esc(plan.id)}">归档</button>`}</td>
      </tr>`).join('')}</tbody></table>` : empty('还没有采集计划', '新建计划，选择单次定时、周期或 Webhook 触发。')}</div>
      <p class="task-boundary">${icon('shield')}默认不重叠执行；停机后合并补跑一次。暂停或归档不取消当前任务，历史始终保留。</p></section>`;
  }

  async function load(isCurrent = () => state.authenticated) {
    const seq = ++serial;
    const result = await request(base() + '?include_deleted=true');
    if (seq !== serial || !isCurrent()) return;
    plans = Array.isArray(result) ? result : [];
    plansEpoch = typeof authEpoch === 'undefined' ? 0 : authEpoch;
    plansRole = state.role;
    render();
  }

  function editorFields(plan) {
    const zone = plan?.schedule?.timezone || Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
    return `<input type="hidden" name="plan_id" value="${esc(plan?.id || '')}"><input type="hidden" name="plan_revision" value="${Number(plan?.revision || 0)}"><input type="hidden" name="plan_max_attempts" value="${Number(plan?.task?.max_attempts || 3)}">
      <div class="collection-plan-config"><label class="task-field">计划名称<span class="task-required">*</span><input name="plan_name" maxlength="120" value="${esc(plan?.name || '')}" placeholder="例如：每天收集官方博客"></label>
      <div class="task-form-grid"><label class="task-field">触发方式<select name="schedule_kind">${Object.entries(KINDS).map(([key,label]) => `<option value="${key}" ${key === (plan?.schedule?.kind || 'manual') ? 'selected' : ''}>${label}</option>`).join('')}</select></label><label class="task-field">计划时区<input name="plan_timezone" list="collection-timezones" value="${esc(zone)}"><datalist id="collection-timezones">${['UTC','Asia/Shanghai','America/New_York','Europe/London'].map(value => `<option value="${value}"></option>`).join('')}</datalist></label></div>
      <label class="task-field" data-plan-field="once">单次执行时间（计划时区）<input name="schedule_at" type="datetime-local" step="1" max="9999-12-31T23:59:59" aria-describedby="collection-once-hint" value="${esc(pickerValue(plan?.schedule?.at,zone))}"><small id="collection-once-hint" data-plan-once-hint aria-live="polite">选择日期和时间，系统会按计划时区自动处理 UTC 偏移。</small></label>
      <label class="task-field" data-plan-once-offset hidden>UTC 偏移（该时刻重复出现）<select name="schedule_at_choice" data-original-at="${esc(plan?.schedule?.at || '')}"></select><small>夏令时结束时可能出现两个相同的当地时间，请明确选择一次。</small></label>
      <label class="task-field" data-plan-field="interval">间隔（分钟）<input name="interval_minutes" type="number" min="1" max="10080" value="${Number(plan?.schedule?.interval_minutes || 60)}"></label>
      <div class="task-form-grid" data-plan-field="unread_inventory"><label class="task-field">未读存量条件<select name="unread_operator">${Object.entries(UNREAD_OPERATORS).map(([key,label]) => `<option value="${key}" ${key === (plan?.schedule?.unread_condition?.operator || 'lt') ? 'selected' : ''}>${label}</option>`).join('')}</select></label><label class="task-field">数量阈值（条）<input name="unread_threshold" type="number" min="0" max="1000000" step="1" value="${Number(plan?.schedule?.unread_condition?.threshold ?? 20)}"></label></div>
      <small data-plan-field="unread_inventory" data-plan-unread-hint>按当前账号的未读状态统计本工作区公开资料；浏览过、已有阅读进度或已标记已读的资料不计入。检查间隔可在高级设置中调整。</small>
      <label class="task-field" data-plan-field="calendar">执行时间（计划时区）<input name="time_of_day" type="time" value="${esc(plan?.schedule?.time_of_day || '09:00')}"></label>
      <fieldset data-plan-field="weekly" class="collection-weekdays"><legend>每周执行日</legend>${Array.from('日一二三四五六').map((day,index) => `<label><input type="checkbox" name="weekday" value="${index}" ${(plan?.schedule?.weekdays || [1]).includes(index) ? 'checked' : ''}>周${day}</label>`).join('')}</fieldset>
      <div data-plan-field="webhook" class="info-box collection-hook-options"><p>保存后在计划列表生成专用触发凭据。外部请求不能覆盖来源、预算、域名或工作区。</p><label><input type="checkbox" name="allow_url" ${plan?.allow_url ? 'checked' : ''}>允许传入链接（仍受域名限制）</label><label><input type="checkbox" name="allow_keyword" ${plan?.allow_keyword ? 'checked' : ''}>允许传入关键词</label></div>
      <details class="collection-plan-advanced"><summary><span class="collection-advanced-heading"><strong>高级设置</strong><small>调度策略、启用与时间预览</small></span>${icon('chevronDown')}</summary><div class="collection-advanced-body">
        <label class="task-field" data-plan-field="unread_inventory">检查间隔（分钟）<input name="unread_check_minutes" type="number" min="1" max="10080" step="1" value="${Number(plan?.schedule?.kind === 'unread_inventory' ? plan.schedule.interval_minutes : 10)}"><small>条件持续满足时按此间隔继续检查；同一计划有未结束任务时不重复派发。</small></label>
        <div class="task-form-grid"><label class="task-field">排队有效期（秒）<input name="queue_ttl_seconds" type="number" min="60" max="604800" value="${Number(plan?.queue_ttl_seconds || 3600)}"></label><label class="task-field">补跑窗口（秒）<input name="catch_up_seconds" type="number" min="30" max="604800" value="${Number(plan?.catch_up_seconds || 3600)}"></label></div>
        <label><input name="plan_enabled" type="checkbox" ${plan?.enabled === false ? '' : 'checked'}>保存后启用（定时计划不会立即执行）</label>
        <div class="collection-preview"><button type="button" class="btn small-btn" data-plan-action="preview">预览后续执行时间</button><div data-plan-preview aria-live="polite"></div></div>
        <p class="task-hint">配置和任务模板按快照保存；同一计划不重叠执行。编辑只影响未来任务，保存不会主动运行采集端。</p>
      </div></details></div>`;
  }

  function refreshEditor(form) {
    const kind = form.elements.namedItem('schedule_kind')?.value;
    form.querySelectorAll('[data-plan-field]').forEach(node => { const key = node.dataset.planField; node.hidden = key === 'calendar' ? !['daily','weekly'].includes(kind) : key !== kind; });
    const preview = form.querySelector('[data-plan-preview]'); if (preview) preview.textContent = '';
    for (const name of ['unread_operator','unread_threshold','unread_check_minutes']) {
      const field = form.elements.namedItem(name);
      if (field) { field.disabled = kind !== 'unread_inventory'; field.required = kind === 'unread_inventory'; }
    }
    const previewButton = form.querySelector('[data-plan-action="preview"]');
    if (previewButton) previewButton.textContent = kind === 'unread_inventory' ? '检查当前存量并预览' : '预览后续执行时间';
    const input = form.elements.namedItem('schedule_at');
    const choice = form.elements.namedItem('schedule_at_choice');
    const offsetField = form.querySelector('[data-plan-once-offset]');
    const hint = form.querySelector('[data-plan-once-hint]');
    if (!input || !choice || !offsetField || !hint) return;
    input.required = kind === 'once';
    input.disabled = kind !== 'once';
    offsetField.hidden = true; choice.required = false; choice.disabled = true;
    hint.classList.remove('collection-time-error'); input.removeAttribute('aria-invalid');
    const zone = form.elements.namedItem('plan_timezone')?.value.trim() || 'UTC';
    hint.textContent = `请选择日期和时间，按 ${zone} 解释，不使用浏览器本地时区。`;
    if (kind !== 'once' || !input.value) return;
    try {
      const candidates = pickerCandidates(input.value,zone);
      if (!candidates.length) throw new Error(`该时间在 ${zone} 不存在（夏令时或时区切换），请选择其他时间。`);
      if (candidates.length === 1) { hint.textContent = `按 ${zone}（${candidates[0].label}）解释，保存时自动转换为明确时间。`; return; }
      const previous = choice.value;
      const original = choice.dataset.originalAt ? new Date(choice.dataset.originalAt).toISOString() : '';
      choice.innerHTML = '<option value="">请选择本次执行对应的 UTC 偏移</option>' + candidates.map((candidate,index) => `<option value="${esc(candidate.at)}">${esc(candidate.label)}（第 ${index + 1} 次出现）</option>`).join('');
      choice.value = candidates.some(candidate => candidate.at === previous) ? previous : candidates.some(candidate => candidate.at === original) ? original : '';
      offsetField.hidden = false; choice.required = true; choice.disabled = false;
      const selected = candidates.find(candidate => candidate.at === choice.value);
      hint.textContent = selected ? `按 ${zone}（${selected.label}）解释，已明确重复时刻对应的执行时间。` : '该时刻因夏令时切换出现两次，请在下方选择 UTC 偏移。';
    } catch (error) { hint.textContent = error.message; hint.classList.add('collection-time-error'); input.setAttribute('aria-invalid','true'); }
  }

  function fillEditor(form, plan) {
    if (plan?.task) {
      const task = plan.task;
      const values = { source_id: task.source_id, title: task.title, goal: task.goal, assigned_agent_id: task.assigned_agent_id,
        max_items: task.constraints?.max_items, content_type: task.constraints?.content_type, duplicate_policy: task.constraints?.duplicate_policy || 'skip', language: task.constraints?.language, category: task.constraints?.category,
        tags: (task.constraints?.tags || []).join(', '), allow_domains: (task.constraints?.allow_domains || []).join(', '), max_rounds: task.budget?.max_rounds, max_tool_calls: task.budget?.max_tool_calls, timeout_seconds: task.budget?.timeout_seconds, priority: task.priority };
      for (const [name,value] of Object.entries(values)) {
        const input = form.elements.namedItem(name); if (!input) continue;
        if (input.tagName === 'SELECT' && value && !Array.from(input.options).some(option => option.value === String(value))) { const option = document.createElement('option'); option.value = String(value); option.textContent = '原配置（当前不可用）'; input.appendChild(option); }
        input.value = String(value ?? '');
      }
    }
    refreshEditor(form);
  }

  function readSchedule(data) {
    const kind = String(data.get('schedule_kind') || 'manual');
    const schedule = { kind, timezone: String(data.get('plan_timezone') || '').trim() || 'UTC' };
    if (kind === 'once') {
      const value = String(data.get('schedule_at') || '').trim();
      const candidates = pickerCandidates(value,schedule.timezone);
      if (!candidates.length) throw new Error(`该时间在 ${schedule.timezone} 不存在（夏令时或时区切换），请选择其他时间。`);
      const selected = candidates.length === 1 ? candidates[0] : candidates.find(candidate => candidate.at === String(data.get('schedule_at_choice') || ''));
      if (!selected) throw new Error('该时刻因夏令时切换出现两次，请选择对应的 UTC 偏移。');
      schedule.at = selected.at;
    } else if (kind === 'interval') schedule.interval_minutes = Number(data.get('interval_minutes'));
    else if (kind === 'unread_inventory') {
      const operator = String(data.get('unread_operator') || '');
      const raw = String(data.get('unread_threshold') ?? '').trim();
      const threshold = Number(raw), minutes = Number(data.get('unread_check_minutes'));
      if (!Object.keys(UNREAD_OPERATORS).includes(operator)) throw new Error('请选择有效的未读存量比较条件。');
      if (!raw || !Number.isInteger(threshold) || threshold < 0 || threshold > 1000000) throw new Error('未读数量阈值须为 0～1000000 的整数。');
      if (!Number.isInteger(minutes) || minutes < 1 || minutes > 10080) throw new Error('请在高级设置中填写 1～10080 分钟的检查间隔。');
      schedule.interval_minutes = minutes;
      schedule.unread_condition = { operator, threshold };
    }
    else if (['daily','weekly'].includes(kind)) {
      schedule.time_of_day = String(data.get('time_of_day') || '');
      if (kind === 'weekly') schedule.weekdays = data.getAll('weekday').map(Number);
    }
    return schedule;
  }

  function validateAdvanced(form) {
    const invalid = form.querySelector('.collection-plan-advanced input:invalid');
    if (!invalid) return;
    invalid.closest('.collection-plan-advanced').open = true;
    invalid.focus(); invalid.reportValidity();
    throw new Error('请检查高级设置中的数值范围。');
  }

  async function submit(form, task) {
    validateAdvanced(form);
    const data = new FormData(form);
    const schedule = readSchedule(data);
    task.max_attempts = Number(data.get('plan_max_attempts') || 3);
    const body = { name: String(data.get('plan_name') || '').trim(), task, schedule, enabled: data.has('plan_enabled'), queue_ttl_seconds: Number(data.get('queue_ttl_seconds')), catch_up_seconds: Number(data.get('catch_up_seconds')), allow_url: schedule.kind === 'webhook' && data.has('allow_url'), allow_keyword: schedule.kind === 'webhook' && data.has('allow_keyword') };
    if (!body.name) throw new Error('请填写计划名称。');
    const id = String(data.get('plan_id') || ''); if (id) body.revision = Number(data.get('plan_revision'));
    const button = $('[data-task-action="dispatch-submit"]'); if (button) button.disabled = true;
    form.dataset.submitting = 'true';
    try { await request(id ? endpoint(id) : base(), { method: id ? 'PUT' : 'POST', body }); delete form.dataset.submitting; closeModal(); toast('计划已保存，未立即运行；可在列表中手动运行一次。'); }
    finally { delete form.dataset.submitting; if (button?.isConnected) button.disabled = false; }
  }

  async function showHistory(plan, offset = 0) {
    const seq = ++historySerial;
    const result = await request(endpoint(plan.id) + '/runs?limit=20&offset=' + offset);
    if (seq !== historySerial || !state.authenticated) return;
    const rows = result.runs || [];
    openModal('运行历史 · ' + plan.name, '所有时间按 ' + (plan.schedule?.timezone || 'UTC') + ' 展示；失败和部分入库结果不会被计划重跑覆盖。', `<div class="collection-history">${rows.length ? rows.map(run => `<article class="collection-run"><div><strong>${esc(run.trigger_source === 'condition' ? '未读存量条件' : run.trigger_source === 'schedule' ? '定时触发' : run.trigger_source === 'webhook' ? 'Webhook 触发' : run.unread_check ? '手动检查' : '手动触发')}</strong><span class="task-badge ${run.dispatch_status === 'queued' ? 'info' : 'warn'}">${esc(run.dispatch_status === 'queued' ? ({pending:'待领取',claimed:'已领取',running:'执行中',succeeded:'已完成',failed:'失败',cancelled:'已取消',expired:'已过期'}[run.task_status] || '已派发') : run.dispatch_status === 'skipped' ? '已跳过' : '被阻断')}</span></div><p>${esc(formatTime(run.scheduled_for,plan.schedule?.timezone))} · 配置版本 ${Number(run.plan_revision)}</p>${run.unread_check ? `<p class="collection-condition-preview ${run.unread_check.matched ? 'matched' : ''}">检查时未读 ${Number(run.unread_check.unread_count)} 条；${esc(unreadRule(run.unread_check))} · ${run.unread_check.matched ? '条件满足' : '条件未满足'}<small>检查于 ${esc(formatTime(run.unread_check.checked_at,plan.schedule?.timezone))}</small></p>` : ''}<p>${esc(REASONS[run.reason] || run.result?.error?.message || run.result?.summary || '')}</p><small>新增 ${Number(run.stats?.items_created || 0)} · 更新 ${Number(run.stats?.items_updated || 0)} · 跳过 ${Number(run.stats?.items_skipped || 0)}</small>${run.task_id && !run.task_deleted ? `<button class="text-btn" data-task-action="detail" data-id="${esc(run.task_id)}">执行详情</button>` : ''}<details><summary>本次配置快照</summary><pre>${esc(JSON.stringify(run.task_config,null,2))}</pre></details></article>`).join('') : empty('暂无运行记录','计划保存不会自动立即运行。')}<div class="collection-history-pages"><span>共 ${Number(result.total || 0)} 次</span><button class="btn small-btn" data-plan-action="history" data-id="${esc(plan.id)}" data-offset="${Math.max(0,offset-20)}" ${offset ? '' : 'disabled'}>上一页</button><button class="btn small-btn" data-plan-action="history" data-id="${esc(plan.id)}" data-offset="${offset+20}" ${offset+20 < result.total ? '' : 'disabled'}>下一页</button></div></div>`, '<button class="btn" data-action="close-modal">关闭</button>', { className:'collection-history-dialog' });
  }

  async function showKey(plan) {
    openModal('外部触发凭据 · ' + plan.name, '凭据只能触发此计划，不能查询资料、任务或日志。请通过 HTTPS 使用，勿放在 URL 参数中。', `<div class="task-form"><p>接口：<code>/api/v1/collection-triggers/${esc(plan.id)}</code></p><p>请求头：<code>Authorization: Bearer &lt;专用凭据&gt;</code></p><p>状态：${plan.trigger_key_set ? '已设置（不能再次读取）' : '未设置'}</p><pre>${esc(JSON.stringify({event_id:'调用方生成且重试时不变的事件标识',sent_at:new Date().toISOString(),...(plan.allow_url ? {url:'https://go.dev/blog/'} : {}),...(plan.allow_keyword ? {keyword:'关键词'} : {})},null,2))}</pre><p>sent_at 需在服务器时间前后 5 分钟内；相同事件不能更改参数。每分钟最多接受一个新事件，相同事件重试不受此限制。</p></div>`, `<button class="btn" data-action="close-modal">关闭</button>${plan.trigger_key_set ? `<button class="btn" data-plan-action="revoke-key" data-id="${esc(plan.id)}">撤销凭据</button>` : ''}<button class="btn primary" data-plan-action="rotate-key" data-id="${esc(plan.id)}">${plan.trigger_key_set ? '轮换凭据' : '生成凭据'}</button>`);
  }

  document.addEventListener('invalid', event => {
    const advanced = event.target.closest?.('.collection-plan-advanced');
    if (advanced) advanced.open = true;
  }, true);
  document.addEventListener('change', event => {
    if (event.target.matches?.('[data-plan-archived]')) { showArchived = event.target.checked; render(); }
    const form = event.target.closest?.('form[data-plan-editor]');
    if (form && ['schedule_kind','plan_timezone','schedule_at','schedule_at_choice','interval_minutes','unread_operator','unread_threshold','unread_check_minutes','time_of_day','weekday'].includes(event.target.name)) refreshEditor(form);
  });
  document.addEventListener('click', async event => {
    const button = event.target.closest?.('[data-plan-action]'); if (!button || button.disabled) return;
    const action = button.dataset.planAction; const plan = plans.find(item => item.id === button.dataset.id);
    button.disabled = true;
    try {
      if (action === 'create') tasksView.openPlanEditor({});
      else if (action === 'reload') await tasksView.load();
      else if (action === 'preview') {
        const form = button.closest('form'); validateAdvanced(form); const schedule = readSchedule(new FormData(form));
        const out = await request(base() + '/preview', { method:'POST',body:{schedule} });
        const target = form.querySelector('[data-plan-preview]');
        if (target) {
          const check = out.unread_check;
          const observation = check ? `<p class="collection-condition-preview ${check.matched ? 'matched' : ''}">当前未读 ${Number(check.unread_count)} 条；${esc(unreadRule(check))}，${check.matched ? '条件满足' : '条件未满足'}。</p><p class="task-hint">以下为后续检查时间，不保证届时执行；仅条件满足且无未结束任务时派发。未读按当前账号的浏览、进度和已读标记判断，不包含草稿或其他工作区资料。</p>` : '';
          target.innerHTML = observation + (out.times.length ? `<ol>${out.times.map(value => `<li>${esc(formatTime(value,schedule.timezone))} ${esc(schedule.timezone)}</li>`).join('')}</ol>` : '<p>此触发方式没有后续定时时间。</p>');
        }
      } else if (plan) {
        if (action === 'edit') tasksView.openPlanEditor(plan);
        else if (action === 'history') await showHistory(plan,Number(button.dataset.offset || 0));
        else if (action === 'key') await showKey(plan);
        else if (action === 'run') {
          if (!window.confirm(plan.schedule?.kind === 'unread_inventory' ? `现在检查「${plan.name}」的未读存量？仅条件满足且无未结束任务时派发，不改变下次检查时间。` : `立即为「${plan.name}」运行一次？不会改变其定时安排；已有未结束任务时跳过。`)) return;
          const out = await request(endpoint(plan.id) + '/run', { method:'POST',body:{event_id:newEventID()} });
          toast(out.dispatch_status === 'queued' ? '已生成执行任务，等待采集端领取。' : REASONS[out.reason] || '本次未生成任务。'); await tasksView.load();
        } else if (action === 'toggle') {
          await request(endpoint(plan.id) + '/state',{method:'POST',body:{revision:plan.revision,enabled:!plan.enabled}}); toast(plan.enabled ? '计划已暂停，当前任务保持不变。' : '计划已启用。'); await tasksView.load();
        } else if (action === 'delete') {
          if (!window.confirm(`归档「${plan.name}」？停止后续触发并撤销凭据，保留运行历史；当前任务不取消。`)) return;
          await request(endpoint(plan.id) + '/delete',{method:'POST',body:{revision:plan.revision}}); toast('计划已归档。'); await tasksView.load();
        } else if (action === 'rotate-key' || action === 'revoke-key') {
          if (!window.confirm(action === 'revoke-key' ? '撤销凭据后，现有外部调用会立即失效。继续？' : '生成新凭据会使旧凭据立即失效，新凭据只显示一次。继续？')) return;
          const out = await request(endpoint(plan.id) + '/trigger-key',{method:'POST',body:{revision:plan.revision,revoke:action === 'revoke-key'}});
          plans = plans.map(item => item.id === plan.id ? out.plan : item);
          if (action === 'revoke-key') { closeModal(); toast('触发凭据已撤销。'); }
          else openModal('触发凭据（仅显示一次）','请安全保存；关闭后不能再次读取。不要发送到聊天或公开日志。',`<label class="task-field">专用凭据<textarea readonly rows="3" autocomplete="off" spellcheck="false">${esc(out.access_key)}</textarea></label>`,'<button class="btn primary" data-action="close-modal">已安全保存</button>');
          render();
        }
      }
    } catch (error) { toast(error.status === 409 ? '记录已变化，请刷新后重试；同一事件不能修改参数。' : error.message,true); }
    finally { if (button.isConnected) button.disabled = false; }
  });

  return { tabs, load, render, editorFields, fillEditor, submit, readSchedule };
})();
