"""Isolated collection-plan UI contract. Invoked by TestCollectionPlansBrowser."""
import sys
from pathlib import Path
from datetime import datetime, timezone
from playwright.sync_api import sync_playwright, expect

ROOT = Path(__file__).resolve().parents[1]
URL = sys.argv[1].rstrip('/')


def login(page, email, password):
    page.goto(URL + '/workspace/#tasks')
    page.locator('#login-root input[type=email]').fill(email)
    page.locator('#login-root input[type=password]').fill(password)
    page.locator('#login-root form').evaluate('(form) => form.requestSubmit()')
    expect(page.locator('body')).not_to_have_class('unauthenticated')
    page.locator('[data-route=tasks]').click()
    page.locator('[data-task-view=plans]').click()
    expect(page.locator('#main-content h1')).to_have_text('采集计划')


def row(page, name):
    return page.locator('#main-content tbody tr').filter(has_text=name)


def close_modal(page):
    page.locator('#modal-root [data-action=close-modal]').last.click()


def expand_plan_settings(form):
    advanced = form.locator('.collection-plan-advanced')
    if not advanced.evaluate('el => el.open'):
        advanced.locator(':scope > summary').click()
    return advanced


def preview_plan(form):
    expand_plan_settings(form)
    form.locator('[data-plan-action=preview]').click()


def check_unread_inventory(page):
    name = '浏览器-未读存量条件'
    page.locator('[data-plan-action=create]').click()
    form = page.locator('form[data-plan-editor]')
    form.locator('[name=plan_name]').fill(name)
    form.locator('[name=schedule_kind]').select_option('unread_inventory')
    expect(form.locator('[name=unread_operator]')).to_be_visible()
    expect(form.locator('[name=unread_threshold]')).to_have_value('20')
    expect(form.locator('[name=unread_check_minutes]')).to_have_value('10')
    expect(form.locator('[name=unread_check_minutes]')).to_be_hidden()
    expect(form.locator('[name=schedule_at]')).to_be_disabled()
    assert not form.locator('.collection-plan-advanced').evaluate('el => el.open')
    form.locator('[name=goal]').fill('未读存量不足时采集 Go 官方文章')
    form.locator('[name=allow_domains]').fill('go.dev')
    form.locator('[name=max_items]').fill('2')
    preview_plan(form)
    expect(form.locator('.collection-condition-preview')).to_contain_text('当前未读 0 条')
    expect(form.locator('.collection-condition-preview')).to_contain_text('条件满足')
    expect(form.locator('[data-plan-preview] li')).to_have_count(5)
    expect(form.locator('[data-plan-preview]')).to_contain_text('后续检查时间，不保证届时执行')
    form.locator('[name=unread_threshold]').fill('0')
    preview_plan(form)
    expect(form.locator('.collection-condition-preview')).to_contain_text('条件未满足')
    form.locator('[name=unread_operator]').select_option('eq')
    form.locator('[name=unread_check_minutes]').fill('7')
    preview_plan(form)
    expect(form.locator('.collection-condition-preview')).to_contain_text('条件满足')
    advanced = form.locator('.collection-plan-advanced')
    advanced.locator(':scope > summary').click()
    expect(page.locator('.toast-stack > *')).to_have_count(0, timeout=10000)
    artifacts = ROOT / '.artifacts' / 'collection-unread'
    artifacts.mkdir(parents=True, exist_ok=True)
    form.locator('.collection-plan-config').screenshot(path=str(artifacts / 'condition-desktop.png'))
    for width in [390, 320]:
        page.set_viewport_size({'width': width, 'height': 844})
        assert page.evaluate('document.documentElement.scrollWidth <= innerWidth + 2')
        assert page.locator('.dialog').evaluate('el => el.scrollWidth <= el.clientWidth + 2')
        form.locator('.task-form-grid[data-plan-field=unread_inventory]').screenshot(path=str(artifacts / f'condition-mobile-{width}.png'))
    page.set_viewport_size({'width': 1440, 'height': 1000})
    with page.expect_response(lambda response: response.request.method == 'POST'
                             and response.url.endswith('/admin/agent/collection-plans')) as saved:
        page.locator('[data-task-action=dispatch-submit]').click()
    schedule = saved.value.request.post_data_json['schedule']
    assert schedule['kind'] == 'unread_inventory' and schedule['interval_minutes'] == 7, schedule
    assert schedule['unread_condition'] == {'operator': 'eq', 'threshold': 0}, schedule
    expect(row(page, name)).to_contain_text('未读存量 = 0 条')
    expect(row(page, name)).to_contain_text('每 7 分钟检查')
    expect(row(page, name).locator('[data-plan-action=run]')).to_have_text('检查并运行')
    row(page, name).locator('[data-plan-action=run]').click()
    expect(page.locator('.toast-stack')).to_contain_text('已生成执行任务')
    expect(row(page, name)).to_contain_text('未读 0 条')
    row(page, name).locator('[data-plan-action=history]').click()
    expect(page.locator('.collection-run')).to_have_count(1)
    expect(page.locator('.collection-run')).to_contain_text('检查时未读 0 条')
    expect(page.locator('.collection-run')).to_contain_text('未读存量 = 0 条')
    close_modal(page)
    row(page, name).locator('[data-plan-action=edit]').click()
    form = page.locator('form[data-plan-editor]')
    expect(form.locator('[name=unread_threshold]')).to_have_value('0')
    expect(form.locator('[name=unread_operator]')).to_have_value('eq')
    expect(form.locator('[name=unread_check_minutes]')).to_have_value('7')
    expand_plan_settings(form)
    form.locator('[name=unread_check_minutes]').fill('0')
    form.locator('.collection-plan-advanced > summary').click()
    page.locator('[data-task-action=dispatch-submit]').click()
    expect(form.locator('[name=unread_check_minutes]')).to_be_focused()
    assert form.locator('.collection-plan-advanced').evaluate('el => el.open')
    form.locator('[name=unread_check_minutes]').fill('7')
    form.locator('[name=unread_operator]').select_option('lt')
    page.locator('[data-task-action=dispatch-submit]').click()
    expect(page.locator('#modal-root')).to_be_empty()
    row(page, name).locator('[data-plan-action=run]').click()
    expect(page.locator('.toast-stack')).to_contain_text('未读存量条件未满足')
    row(page, name).locator('[data-plan-action=history]').click()
    expect(page.locator('.collection-run')).to_have_count(2)
    expect(page.locator('.collection-history')).to_contain_text('未读存量 < 0 条')
    expect(page.locator('.collection-history')).to_contain_text('未读存量 = 0 条')
    expect(page.locator('.collection-history')).to_contain_text('条件未满足')
    close_modal(page)
    row(page, name).locator('[data-plan-action=edit]').click()
    form = page.locator('form[data-plan-editor]')
    form.locator('[name=unread_operator]').select_option('gte')
    page.locator('[data-task-action=dispatch-submit]').click()
    expect(page.locator('#modal-root')).to_be_empty()
    row(page, name).locator('[data-plan-action=run]').click()
    expect(page.locator('.toast-stack')).to_contain_text('已有未结束任务')
    row(page, name).locator('[data-plan-action=toggle]').click()
    expect(row(page, name)).to_contain_text('已暂停')
    expect(row(page, name).locator('[data-plan-action=run]')).to_be_disabled()
    print('PASS: unread inventory preview/boundaries, create/edit/retained interval, conditional manual run/history/overlap/pause, and mobile')


def check_advanced_settings(page):
    name = '浏览器-折叠高级设置'
    page.locator('[data-plan-action=create]').click()
    form = page.locator('form[data-plan-editor]')
    advanced = form.locator('.collection-plan-advanced')
    form.locator('[name=plan_name]').fill(name)
    form.locator('[name=schedule_kind]').select_option('interval')
    form.locator('[name=plan_timezone]').fill('Asia/Shanghai')
    form.locator('[name=goal]').fill('采集 Go 官方博客文章并附原文链接')
    form.locator('[name=allow_domains]').fill('go.dev')
    assert not advanced.evaluate('el => el.open')
    expect(form.locator('[name=interval_minutes]')).to_be_visible()
    expect(form.locator('[name=queue_ttl_seconds]')).to_be_hidden()
    expect(form.locator('[name=catch_up_seconds]')).to_be_hidden()
    expect(form.locator('[name=plan_enabled]')).to_be_hidden()
    expect(form.locator('[data-plan-action=preview]')).to_be_hidden()
    expect(form.locator('[name=queue_ttl_seconds]')).to_have_value('3600')
    expect(form.locator('[name=catch_up_seconds]')).to_have_value('3600')
    expect(form.locator('[name=plan_enabled]')).to_be_checked()
    expect(page.locator('.toast-stack > *')).to_have_count(0, timeout=10000)
    artifacts = ROOT / '.artifacts' / 'collection-advanced'
    artifacts.mkdir(parents=True, exist_ok=True)
    form.locator('.collection-plan-config').screenshot(path=str(artifacts / 'collapsed-desktop.png'))
    for width in [390, 320]:
        page.set_viewport_size({'width': width, 'height': 844})
        assert not advanced.evaluate('el => el.open')
        assert page.evaluate('document.documentElement.scrollWidth <= innerWidth + 2')
        assert page.locator('.dialog').evaluate('el => el.scrollWidth <= el.clientWidth + 2')
        form.locator('.collection-plan-config').screenshot(path=str(artifacts / f'collapsed-mobile-{width}.png'))
    page.set_viewport_size({'width': 1440, 'height': 1000})
    summary = advanced.locator(':scope > summary')
    summary.focus()
    summary.press('Enter')
    assert advanced.evaluate('el => el.open')
    expect(form.locator('[name=queue_ttl_seconds]')).to_be_visible()
    summary.press('Enter')
    assert not advanced.evaluate('el => el.open')
    with page.expect_response(lambda response: response.request.method == 'POST'
                             and response.url.endswith('/admin/agent/collection-plans')) as created:
        page.locator('[data-task-action=dispatch-submit]').click()
    payload = created.value.request.post_data_json
    assert (payload['queue_ttl_seconds'], payload['catch_up_seconds'], payload['enabled']) == (3600, 3600, True), payload
    expect(row(page, name)).to_have_count(1)
    plan_id = row(page, name).locator('[data-plan-action=edit]').get_attribute('data-id')
    endpoint = '/admin/agent/collection-plans/' + plan_id
    row(page, name).locator('[data-plan-action=edit]').click()
    form = page.locator('form[data-plan-editor]')
    assert not form.locator('.collection-plan-advanced').evaluate('el => el.open')
    advanced = expand_plan_settings(form)
    form.locator('[name=queue_ttl_seconds]').fill('7200')
    form.locator('[name=catch_up_seconds]').fill('1800')
    form.locator('[name=plan_enabled]').uncheck()
    advanced.screenshot(path=str(artifacts / 'expanded-desktop.png'))
    advanced.locator(':scope > summary').click()
    with page.expect_response(lambda response: response.request.method == 'PUT'
                             and response.url.endswith(endpoint)) as customized:
        page.locator('[data-task-action=dispatch-submit]').click()
    payload = customized.value.request.post_data_json
    assert (payload['queue_ttl_seconds'], payload['catch_up_seconds'], payload['enabled']) == (7200, 1800, False), payload
    expect(row(page, name)).to_contain_text('已暂停')
    row(page, name).locator('[data-plan-action=edit]').click()
    form = page.locator('form[data-plan-editor]')
    advanced = form.locator('.collection-plan-advanced')
    assert not advanced.evaluate('el => el.open')
    expect(form.locator('[name=queue_ttl_seconds]')).to_have_value('7200')
    expect(form.locator('[name=catch_up_seconds]')).to_have_value('1800')
    expect(form.locator('[name=plan_enabled]')).not_to_be_checked()
    # A save without opening advanced settings must not reset custom values.
    with page.expect_response(lambda response: response.request.method == 'PUT'
                             and response.url.endswith(endpoint)) as retained:
        page.locator('[data-task-action=dispatch-submit]').click()
    payload = retained.value.request.post_data_json
    assert (payload['queue_ttl_seconds'], payload['catch_up_seconds'], payload['enabled']) == (7200, 1800, False), payload
    expect(page.locator('#modal-root')).to_be_empty()
    row(page, name).locator('[data-plan-action=edit]').click()
    form = page.locator('form[data-plan-editor]')
    advanced = expand_plan_settings(form)
    form.locator('[name=queue_ttl_seconds]').fill('59')
    advanced.locator(':scope > summary').click()
    mutations = []
    def track_save(request):
        if request.method == 'PUT' and request.url.endswith(endpoint):
            mutations.append(request.url)
    page.on('request', track_save)
    page.locator('[data-task-action=dispatch-submit]').click()
    expect(page.locator('.toast-stack')).to_contain_text('请检查高级设置')
    assert advanced.evaluate('el => el.open')
    expect(form.locator('[name=queue_ttl_seconds]')).to_be_focused()
    assert not mutations, mutations
    form.locator('[name=queue_ttl_seconds]').fill('7200')
    form.locator('[name=catch_up_seconds]').fill('29')
    advanced.locator(':scope > summary').click()
    assert not form.evaluate('el => el.reportValidity()')
    assert advanced.evaluate('el => el.open')
    expect(form.locator('[name=catch_up_seconds]')).to_be_focused()
    form.locator('[name=catch_up_seconds]').fill('1800')
    advanced.locator(':scope > summary').click()
    with page.expect_response(lambda response: response.request.method == 'PUT'
                             and response.url.endswith(endpoint)) as corrected:
        page.locator('[data-task-action=dispatch-submit]').click()
    assert corrected.value.ok, corrected.value.text()
    assert len(mutations) == 1, mutations
    page.remove_listener('request', track_save)
    expect(page.locator('#modal-root')).to_be_empty()
    print('PASS: advanced settings default-collapse, keyboard toggle, mobile, retained defaults/custom values, disabled state, and automatic error reveal')


def check_datetime_picker(page):
    # Browser timezone is Honolulu; the selected plan timezone must win.
    name = '浏览器-单次日期时间选择器'
    page.locator('[data-plan-action=create]').click()
    form = page.locator('form[data-plan-editor]')
    form.locator('[name=plan_name]').fill(name)
    form.locator('[name=schedule_kind]').select_option('once')
    zone = form.locator('[name=plan_timezone]')
    picker = form.locator('[name=schedule_at]')
    expect(picker).to_have_attribute('type', 'datetime-local')
    expect(picker).to_have_attribute('step', '1')
    expect(picker).to_be_enabled()
    zone.fill('Asia/Shanghai')
    picker.fill('2031-01-02T09:15:30')
    picker.press('Tab')
    expect(form.locator('[data-plan-once-hint]')).to_contain_text('UTC+08:00')
    expect(form.locator('[data-plan-once-offset]')).to_be_hidden()
    form.locator('[name=goal]').fill('采集 Go 官方博客文章并附原文链接')
    form.locator('[name=allow_domains]').fill('go.dev')
    form.locator('[name=max_items]').fill('2')
    preview_plan(form)
    expect(form.locator('[data-plan-preview] li')).to_have_count(1)
    expect(form.locator('[data-plan-preview] li')).to_contain_text('09:15:30')
    expect(form.locator('[data-plan-preview]')).to_contain_text('Asia/Shanghai')
    artifacts = ROOT / '.artifacts' / 'collection-picker'
    artifacts.mkdir(parents=True, exist_ok=True)
    picker.scroll_into_view_if_needed()
    form.locator('[data-plan-field=once]').screenshot(path=str(artifacts / 'picker-desktop.png'))
    with page.expect_response(lambda response: response.request.method == 'POST'
                             and response.url.endswith('/admin/agent/collection-plans')) as saved:
        page.locator('[data-task-action=dispatch-submit]').click()
    payload = saved.value.request.post_data_json
    assert payload['schedule']['at'] == '2031-01-02T01:15:30.000Z', payload['schedule']
    expect(row(page, name)).to_have_count(1)
    plan_id = row(page, name).locator('[data-plan-action=edit]').get_attribute('data-id')
    endpoint = '/admin/agent/collection-plans/' + plan_id
    row(page, name).locator('[data-plan-action=edit]').click()
    form = page.locator('form[data-plan-editor]')
    expect(form.locator('[name=schedule_at]')).to_have_value('2031-01-02T09:15:30')
    form.locator('[name=plan_timezone]').fill('Asia/Kathmandu')
    form.locator('[name=plan_timezone]').press('Tab')
    expect(form.locator('[name=schedule_at]')).to_have_value('2031-01-02T09:15:30')
    expect(form.locator('[data-plan-once-hint]')).to_contain_text('UTC+05:45')
    with page.expect_response(lambda response: response.request.method == 'PUT'
                             and response.url.endswith(endpoint)) as updated:
        page.locator('[data-task-action=dispatch-submit]').click()
    assert updated.value.request.post_data_json['schedule']['at'] == '2031-01-02T03:30:30.000Z'
    expect(page.locator('#modal-root')).to_be_empty()
    row(page, name).locator('[data-plan-action=edit]').click()
    form = page.locator('form[data-plan-editor]')
    form.locator('[name=plan_timezone]').fill('America/New_York')
    picker = form.locator('[name=schedule_at]')
    picker.fill('2030-03-10T02:30')
    picker.press('Tab')
    expect(form.locator('[data-plan-once-hint]')).to_contain_text('不存在')
    expect(picker).to_have_attribute('aria-invalid', 'true')
    previews = []
    def track_preview(request):
        if request.method == 'POST' and request.url.endswith('/collection-plans/preview'):
            previews.append(request.url)
    page.on('request', track_preview)
    preview_plan(form)
    expect(page.locator('.toast-stack')).to_contain_text('不存在')
    assert not previews, previews
    picker.fill('2030-11-03T01:30')
    picker.press('Tab')
    choice = form.locator('[name=schedule_at_choice]')
    expect(form.locator('[data-plan-once-offset]')).to_be_visible()
    expect(choice.locator('option')).to_have_count(3)
    expect(choice).to_have_value('')
    preview_plan(form)
    expect(page.locator('.toast-stack')).to_contain_text('出现两次')
    assert not previews, previews
    choice.select_option('2030-11-03T06:30:00.000Z')
    expect(form.locator('[data-plan-once-hint]')).to_contain_text('UTC-05:00')
    preview_plan(form)
    expect(form.locator('[data-plan-preview] li')).to_have_count(1)
    expect(form.locator('[data-plan-preview] li')).to_contain_text('01:30:00')
    assert len(previews) == 1, previews
    page.remove_listener('request', track_preview)
    with page.expect_response(lambda response: response.request.method == 'PUT'
                             and response.url.endswith(endpoint)) as folded:
        page.locator('[data-task-action=dispatch-submit]').click()
    assert folded.value.request.post_data_json['schedule']['at'] == '2030-11-03T06:30:00.000Z'
    expect(page.locator('#modal-root')).to_be_empty()
    row(page, name).locator('[data-plan-action=edit]').click()
    form = page.locator('form[data-plan-editor]')
    expect(form.locator('[name=schedule_at]')).to_have_value('2030-11-03T01:30')
    expect(form.locator('[name=schedule_at_choice]')).to_have_value('2030-11-03T06:30:00.000Z')
    expect(page.locator('.toast-stack > *')).to_have_count(0, timeout=10000)
    for width in [390, 320]:
        page.set_viewport_size({'width': width, 'height': 844})
        form.locator('[name=schedule_at]').scroll_into_view_if_needed()
        assert page.evaluate('document.documentElement.scrollWidth <= innerWidth + 2')
        assert page.locator('.dialog').evaluate('el => el.scrollWidth <= el.clientWidth + 2')
        form.locator('[data-plan-field=once]').screenshot(path=str(artifacts / f'picker-mobile-{width}.png'))
    page.set_viewport_size({'width': 1440, 'height': 1000})
    form.locator('[name=plan_timezone]').fill('Mars/Olympus')
    form.locator('[name=plan_timezone]').press('Tab')
    expect(form.locator('[data-plan-once-hint]')).to_contain_text('计划时区无效')
    preview_plan(form)
    expect(page.locator('.toast-stack')).to_contain_text('计划时区无效')
    form.locator('[name=plan_timezone]').fill('UTC')
    form.locator('[name=schedule_at]').fill('')
    preview_plan(form)
    expect(page.locator('.toast-stack')).to_contain_text('请选择有效')
    form.locator('[name=schedule_kind]').select_option('manual')
    expect(form.locator('[name=schedule_at]')).to_be_disabled()
    expect(form.locator('[data-plan-once-offset]')).to_be_hidden()
    close_modal(page)
    print('PASS: once picker create/edit/preview, explicit timezone conversion, DST gap/fold, invalid input, and 320/390px layouts')


def main():
    with sync_playwright() as p:
        browser = p.chromium.launch(headless=True)
        context = browser.new_context(viewport={'width': 1440, 'height': 1000},
                                      timezone_id='Pacific/Honolulu', reduced_motion='reduce')
        page = context.new_page()
        errors = []
        page.on('pageerror', lambda error: errors.append(str(error)))
        page.on('dialog', lambda dialog: dialog.accept())
        login(page, 'admin@example.com', 'administrator-test-password')
        page.locator('[data-plan-action=create]').click()
        form = page.locator('form[data-plan-editor]')
        form.locator('[name=plan_name]').fill('浏览器-周期计划')
        form.locator('[name=schedule_kind]').select_option('daily')
        form.locator('[name=plan_timezone]').fill('Asia/Shanghai')
        form.locator('[name=time_of_day]').fill('09:00')
        form.locator('[name=goal]').fill('采集 Go 官方博客文章并附原文链接')
        form.locator('[name=allow_domains]').fill('go.dev')
        form.locator('[name=max_items]').fill('2')
        preview_plan(form)
        expect(form.locator('[data-plan-preview] li')).to_have_count(5)
        expect(form.locator('[data-plan-preview] li').first).to_contain_text('09:00:00')
        page.locator('[data-task-action=dispatch-submit]').click()
        expect(row(page, '浏览器-周期计划')).to_have_count(1)
        expect(row(page, '浏览器-周期计划')).to_contain_text('每天 09:00')
        row(page, '浏览器-周期计划').locator('[data-plan-action=edit]').click()
        form = page.locator('form[data-plan-editor]')
        expect(form.locator('[name=max_items]')).to_have_value('2')
        expect(form.locator('[name=allow_domains]')).to_have_value('go.dev')
        form.locator('[name=schedule_kind]').select_option('weekly')
        form.locator('[name=goal]').fill('修改后：采集 Go 官方博客文章')
        preview_plan(form)
        expect(form.locator('[data-plan-preview] li')).to_have_count(5)
        page.locator('[data-task-action=dispatch-submit]').click()
        expect(row(page, '浏览器-周期计划')).to_contain_text('每周')
        row(page, '浏览器-周期计划').locator('[data-plan-action=run]').click()
        expect(page.locator('.toast-stack')).to_contain_text('已生成执行任务')
        row(page, '浏览器-周期计划').locator('[data-plan-action=history]').click()
        expect(page.locator('.collection-run')).to_have_count(1)
        expect(page.locator('.collection-run')).to_contain_text('待领取')
        page.locator('.collection-run details').evaluate('(node) => node.open=true')
        expect(page.locator('.collection-run pre')).to_contain_text('修改后')
        close_modal(page)
        row(page, '浏览器-周期计划').locator('[data-plan-action=run]').click()
        expect(page.locator('.toast-stack')).to_contain_text('已有未结束任务')
        row(page, '浏览器-周期计划').locator('[data-plan-action=history]').click()
        expect(page.locator('.collection-run')).to_have_count(2)
        expect(page.locator('.collection-history')).to_contain_text('已跳过')
        close_modal(page)
        row(page, '浏览器-周期计划').locator('[data-plan-action=toggle]').click()
        expect(row(page, '浏览器-周期计划')).to_contain_text('已暂停')
        expect(row(page, '浏览器-周期计划').locator('[data-plan-action=run]')).to_be_disabled()
        row(page, '浏览器-周期计划').locator('[data-plan-action=toggle]').click()
        expect(row(page, '浏览器-周期计划')).to_contain_text('已启用')
        check_advanced_settings(page)
        check_datetime_picker(page)
        check_unread_inventory(page)
        # Create a hook through the same configuration editor.
        page.locator('[data-plan-action=create]').click()
        form = page.locator('form[data-plan-editor]')
        form.locator('[name=plan_name]').fill('浏览器-Webhook计划')
        form.locator('[name=schedule_kind]').select_option('webhook')
        form.locator('[name=allow_url]').check()
        form.locator('[name=goal]').fill('采集事件链接中的官方文章')
        form.locator('[name=allow_domains]').fill('go.dev')
        page.locator('[data-task-action=dispatch-submit]').click()
        expect(row(page, '浏览器-Webhook计划')).to_have_count(1)
        hook_row = row(page, '浏览器-Webhook计划')
        plan_id = hook_row.locator('[data-plan-action=key]').get_attribute('data-id')
        hook_row.locator('[data-plan-action=key]').click()
        page.locator('[data-plan-action=rotate-key]').click()
        key = page.locator('#modal-root textarea[readonly]').input_value()
        assert key.startswith('wztg_')
        close_modal(page)
        hook_url = URL + '/api/v1/collection-triggers/' + plan_id
        headers = {'Authorization': 'Bearer ' + key}
        payload = {'event_id': 'browser-hook-1', 'sent_at': datetime.now(timezone.utc).isoformat(), 'url': 'https://go.dev/blog/'}
        result = context.request.post(hook_url, headers=headers, data=payload)
        assert result.status == 200, result.text()
        first = result.json()['data']
        assert first['task_id'] and 'task_config' not in first
        duplicate = context.request.post(hook_url, headers=headers, data=payload)
        assert duplicate.status == 200 and duplicate.json()['data']['run_id'] == first['run_id']
        hook_row = row(page, '浏览器-Webhook计划')
        hook_row.locator('[data-plan-action=key]').click()
        page.locator('[data-plan-action=revoke-key]').click()
        expect(page.locator('#modal-root')).to_be_empty()
        assert context.request.post(hook_url, headers=headers, data=payload).status == 401
        hook_row.locator('[data-plan-action=delete]').click()
        expect(row(page, '浏览器-Webhook计划')).to_have_count(0)
        page.locator('[data-plan-archived]').check()
        expect(row(page, '浏览器-Webhook计划')).to_contain_text('已归档')
        row(page, '浏览器-Webhook计划').locator('[data-plan-action=history]').click()
        expect(page.locator('.collection-run')).to_have_count(1)
        close_modal(page)
        artifacts = ROOT / '.artifacts'
        artifacts.mkdir(exist_ok=True)
        page.screenshot(path=str(artifacts / 'collection-plans-ui.png'), full_page=True)
        # Mobile layout and plain-HTTP fallback for randomUUID.
        page.set_viewport_size({'width': 390, 'height': 844})
        assert page.evaluate('document.documentElement.scrollWidth <= window.innerWidth + 2')
        context.close()
        user_context = browser.new_context(viewport={'width': 1280, 'height': 900})
        user_context.add_init_script("Object.defineProperty(window.crypto, 'randomUUID', {value: undefined})")
        user_page = user_context.new_page()
        user_page.on('pageerror', lambda error: errors.append(str(error)))
        user_page.on('dialog', lambda dialog: dialog.accept())
        login(user_page, 'collection-browser@example.com', 'collection-browser-test-password')
        expect(row(user_page, '浏览器-周期计划')).to_have_count(0)
        expect(row(user_page, '浏览器-未读存量条件')).to_have_count(0)
        user_page.locator('[data-plan-action=create]').click()
        form = user_page.locator('form[data-plan-editor]')
        form.locator('[name=plan_name]').fill('普通用户独立计划')
        form.locator('[name=goal]').fill('采集普通用户自己的资料')
        user_page.locator('[data-task-action=dispatch-submit]').click()
        expect(row(user_page, '普通用户独立计划')).to_have_count(1)
        assert user_page.evaluate('typeof crypto.randomUUID') == 'undefined'
        row(user_page, '普通用户独立计划').locator('[data-plan-action=run]').click()
        expect(user_page.locator('.toast-stack')).to_contain_text('已生成执行任务')
        assert not errors, errors
        user_context.close()
        browser.close()
        print('PASS: browser create/edit/preview/run/overlap/pause/history/archive, hook key/revocation, mobile and user isolation')


if __name__ == '__main__':
    main()
