"""Real-browser roadmap regression using only a disposable synthetic workspace.

Requires the same Python Playwright / Chrome setup as verify_client_navigation.py.
Screenshots are written to .artifacts/roadmaps; no live data or credentials are read.
"""
from pathlib import Path
import json
import subprocess
import tempfile
import uuid

from playwright.sync_api import expect, sync_playwright
import verify_two_chains as fixture

ARTIFACTS = fixture.ROOT / '.artifacts' / 'roadmaps'


def check(name):
    print('PASS:', name, flush=True)


def save(page):
    page.locator('[data-action=roadmap-save]').click()
    expect(page.locator('#roadmap-save-state')).to_contain_text('已保存')
    expect(page.locator('[data-action=roadmap-save]')).to_be_disabled()


def select(page, title):
    node = page.locator('.roadmap-node-main').filter(has=page.locator('strong', has_text=title))
    node.click()
    return node.get_attribute('data-node-id')


def open_saved(page, identifier):
    page.locator(f'[data-action=roadmap-edit][data-id="{identifier}"]').first.click()
    expect(page.locator('#roadmap-properties')).to_be_visible()


def assert_layout(page, name):
    for width in (1920, 1440, 1024, 1000, 768, 701, 700, 390, 320):
        page.set_viewport_size({'width': width, 'height': 1000})
        assert page.evaluate('document.documentElement.scrollWidth <= innerWidth + 1'), (name, width)
        if name == 'editor':
            expect(page.locator('#roadmap-viewport')).to_be_visible()
            page.locator('[data-action=roadmap-fit]').click()
            assert page.locator('#roadmap-viewport').evaluate('el => el.clientWidth >= 200'), width
        if width in (1440, 390):
            page.screenshot(path=str(ARTIFACTS / f'{name}-{width}.png'), full_page=True)
    page.set_viewport_size({'width': 1600, 'height': 1100})
    check(f'{name}: responsive layout at 320-1920 px and saved screenshots')


def run_browser(page, context, data_dir, log):
    page.goto(fixture.BASE + '/admin/#roadmaps')
    fixture.login(page)
    expect(page.locator('h1')).to_have_text('学习路线图')
    expect(page.locator('.section-tabs')).to_have_count(0)
    expect(page.locator('.sidebar [data-route=roadmaps]')).to_have_attribute('aria-current', 'page')
    expect(page.locator('#roadmap-results')).to_contain_text('你的下一段学习')
    for asset, content_type in [('roadmaps.js', 'text/javascript'), ('roadmaps.css', 'text/css')]:
        response = context.request.get(fixture.BASE + '/admin/' + asset)
        assert response.status == 200 and content_type in response.headers['content-type']
    check('standalone navigation, empty state and embedded assets')

    item = fixture.api('/admin/items', 'POST', {'status': 'pending', 'content': {
        'kind': 'article', 'title': '路线图关联资料', 'content': '合成测试内容'}}, expected=201)
    page.locator('[data-action=roadmap-new]').first.click()
    expect(page.locator('.roadmap-node')).to_have_count(4)
    page.locator('#roadmap-title').fill('Python 全栈学习路线')
    page.locator('#roadmap-summary').fill('从基础、练习到项目实践，合成验证数据。')
    foundation = select(page, '建立基础')
    page.locator('#roadmap-node-title').fill('语法基础')
    page.locator('#roadmap-node-note').fill('<script>window.roadmapXSS=true</script>\n先理解语言的基础概念。')
    assert page.evaluate('window.roadmapXSS') is None
    expect(page.locator('.roadmap-world script')).to_have_count(0)
    page.locator('[data-action=roadmap-child]').click()
    page.locator('#roadmap-node-title').fill('变量与函数')
    leaf = page.locator('.roadmap-node.selected [data-node-id]').get_attribute('data-node-id')
    expect(page.locator('.roadmap-node')).to_have_count(5)
    page.locator('[data-action=roadmap-sibling]').click()
    page.locator('#roadmap-node-title').fill('小项目练习')
    moved = page.locator('.roadmap-node.selected [data-node-id]').get_attribute('data-node-id')
    expect(page.locator('.roadmap-node')).to_have_count(6)
    page.locator('[data-action=roadmap-child]').click()
    expect(page.locator('.roadmap-node')).to_have_count(7)
    page.locator('[data-action=roadmap-undo]').click()
    expect(page.locator('.roadmap-node')).to_have_count(6)
    page.locator('[data-action=roadmap-redo]').click()
    expect(page.locator('.roadmap-node')).to_have_count(7)
    page.once('dialog', lambda dialog: dialog.accept())
    page.locator('[data-action=roadmap-remove]').click()
    expect(page.locator('.roadmap-node')).to_have_count(6)
    check('node editing, safe text rendering, child/sibling insertion, undo/redo and subtree deletion')

    practice = select(page, '项目实践')
    select(page, '小项目练习')
    page.locator('#roadmap-parent').select_option(practice)
    expect(page.locator('#roadmap-parent')).to_have_value(practice)
    page.locator('#roadmap-resource-query').fill('路线图关联资料')
    page.locator('#roadmap-resource-form button[type=submit]').click()
    page.locator(f'[data-action=roadmap-link][data-value="{item["id"]}"]').click()
    expect(page.locator('#roadmap-linked-resources')).to_contain_text('路线图关联资料')
    # Exercise the real drag/drop event path as well as the mobile parent selector.
    page.locator(f'[data-node-id="{moved}"]').drag_to(page.locator(f'[data-node-id="{foundation}"]'))
    expect(page.locator('#roadmap-parent')).to_have_value(foundation)
    page.locator('#roadmap-parent').select_option(practice)
    old_zoom = page.locator('#roadmap-zoom-label').inner_text()
    page.locator('[data-action=roadmap-zoom-in]').click()
    assert page.locator('#roadmap-zoom-label').inner_text() != old_zoom
    page.locator('[data-action=roadmap-fit]').click()
    save(page)
    roadmap = fixture.api('/admin/roadmaps')['items'][0]
    identifier = roadmap['id']
    stored = fixture.api('/admin/roadmaps/' + identifier)
    assert len(stored['nodes']) == 6
    assert next(n for n in stored['nodes'] if n['id'] == moved)['parent_id'] == practice
    assert next(n for n in stored['nodes'] if n['id'] == moved)['item_ids'] == [item['id']]
    assert next(n for n in stored['nodes'] if n['id'] == leaf)['parent_id'] == foundation
    check('parent changes, drag/drop, resource linking, zoom and persisted tree structure')

    select(page, '项目实践')
    page.locator('#roadmap-toolbar [data-action=roadmap-fold]').click()
    expect(page.locator('.roadmap-node')).to_have_count(5)
    save(page)
    page.reload()
    open_saved(page, identifier)
    expect(page.locator('.roadmap-node')).to_have_count(5)
    select(page, '项目实践')
    page.locator('#roadmap-toolbar [data-action=roadmap-fold]').click()
    expect(page.locator('.roadmap-node')).to_have_count(6)
    page.locator('[data-action=roadmap-up]').click()
    # Keyboard authoring is scoped to nodes and leaves normal form Tab navigation intact.
    page.locator(f'[data-node-id="{practice}"]').focus()
    page.keyboard.press('Insert')
    page.locator('#roadmap-node-title').fill('交付完整项目')
    save(page)
    stored = fixture.api('/admin/roadmaps/' + identifier)
    children = [n['id'] for n in stored['nodes'] if n['parent_id'] == stored['nodes'][0]['id']]
    assert children.index(practice) == 1
    assert len(stored['nodes']) == 7
    assert_layout(page, 'editor')
    check('collapse state survives reload, sibling order and keyboard insertion')

    page.locator('#roadmap-title').fill('不应丢失的本地修改')
    page.once('dialog', lambda dialog: dialog.dismiss())
    page.locator('.sidebar [data-route=query]').click()
    expect(page.locator('#roadmap-title')).to_have_value('不应丢失的本地修改')
    assert page.url.endswith('#roadmaps')
    # Browser hash navigation is protected too.
    page.once('dialog', lambda dialog: dialog.dismiss())
    page.evaluate("location.hash='clients'")
    expect(page.locator('#roadmap-title')).to_have_value('不应丢失的本地修改')
    page.wait_for_url('**/#roadmaps')
    page.once('dialog', lambda dialog: dialog.accept())
    page.locator('[data-action=roadmap-back]').click()
    expect(page.locator('.roadmap-grid')).to_be_visible()
    assert fixture.api('/admin/roadmaps/' + identifier)['title'] == 'Python 全栈学习路线'
    check('unsaved-change guards retain drafts during sidebar and hash navigation')

    open_saved(page, identifier)
    server = fixture.api('/admin/roadmaps/' + identifier)
    fixture.api('/admin/roadmaps/' + identifier, 'PUT', {
        'revision': server['revision'], 'title': server['title'],
        'summary': '来自其他窗口的新简介', 'nodes': server['nodes']})
    page.locator('#roadmap-summary').fill('本地冲突草稿')
    page.locator('[data-action=roadmap-save]').click()
    expect(page.locator('#roadmap-error')).to_contain_text('其他窗口')
    expect(page.locator('#roadmap-summary')).to_have_value('本地冲突草稿')
    assert fixture.api('/admin/roadmaps/' + identifier)['summary'] == '来自其他窗口的新简介'
    page.once('dialog', lambda dialog: dialog.accept())
    page.locator('[data-action=roadmap-reload]').click()
    expect(page.locator('#roadmap-summary')).to_have_value('来自其他窗口的新简介')
    check('revision conflict preserves local draft and requires explicit reload')

    pending = []
    def hold_save(route):
        if route.request.method == 'PUT':
            pending.append(route)
        else:
            route.continue_()
    page.route('**/api/v1/admin/roadmaps/' + identifier, hold_save)
    page.locator('#roadmap-summary').fill('保存中保护验证')
    page.locator('[data-action=roadmap-save]').click()
    expect(page.locator('#roadmap-save-state')).to_contain_text('正在保存')
    page.locator('.sidebar [data-route=query]').click()
    assert page.url.endswith('#roadmaps')
    expect(page.locator('#roadmap-summary')).to_be_disabled()
    assert len(pending) == 1
    pending.pop().continue_()
    expect(page.locator('#roadmap-save-state')).to_contain_text('已保存')
    page.unroute('**/api/v1/admin/roadmaps/' + identifier, hold_save)
    check('in-flight save freezes editing and navigation without losing submitted data')

    # A reload initiated earlier must not silently overwrite edits made while it waits.
    pending_get = []
    def hold_reload(route):
        if route.request.method == 'GET':
            pending_get.append(route)
        else:
            route.continue_()
    page.route('**/api/v1/admin/roadmaps/' + identifier, hold_reload)
    page.evaluate("roadmapOpen(roadmapEditor.doc.id, true); undefined")
    page.locator('#roadmap-summary').fill('读取期间新增的草稿')
    assert len(pending_get) == 1
    with page.expect_event('dialog') as prompt:
        pending_get.pop().continue_()
    prompt.value.dismiss()
    expect(page.locator('#roadmap-summary')).to_have_value('读取期间新增的草稿')
    page.unroute('**/api/v1/admin/roadmaps/' + identifier, hold_reload)
    page.once('dialog', lambda dialog: dialog.accept())
    page.locator('[data-action=roadmap-back]').click()
    check('delayed reload cannot overwrite edits made after the request started')

    # Restart the isolated file-backed service and log in again with synthetic credentials.
    fixture.stop()
    fixture.start(data_dir, log, bootstrap=False)
    page.reload()
    fixture.login(page)
    open_saved(page, identifier)
    expect(page.locator('#roadmap-summary')).to_have_value('保存中保护验证')
    expect(page.locator('.roadmap-node')).to_have_count(7)
    page.locator('[data-action=roadmap-back]').click()
    check('file-backed roadmap survives actual process restart')

    for i in range(13):
        root = str(uuid.uuid4())
        fixture.api('/admin/roadmaps', 'POST', {'revision': 0, 'title': f'分页测试 {i:02d}', 'summary': '',
            'nodes': [{'id': root, 'parent_id': '', 'title': f'分页测试 {i:02d}', 'note': '', 'item_ids': [], 'collapsed': False}]}, expected=201)
    page.reload()
    expect(page.locator('.roadmap-card')).to_have_count(12)
    page.locator('[data-action=roadmap-page][data-value="2"]').click()
    expect(page.locator('.roadmap-card')).to_have_count(2)
    page.locator('#roadmap-search').fill('Python 全栈学习路线')
    page.locator('#roadmap-search-form button[type=submit]').click()
    expect(page.locator('.roadmap-card')).to_have_count(1)
    assert_layout(page, 'list')
    page.once('dialog', lambda dialog: dialog.accept())
    page.locator(f'[data-action=roadmap-delete][data-id="{identifier}"]').click()
    expect(page.locator('.roadmap-card')).to_have_count(0)
    fixture.api('/admin/roadmaps/' + identifier, expected=404)
    assert fixture.api('/admin/items/' + item['id'])['id'] == item['id']
    check('pagination, search and deletion do not alter referenced learning materials')


def main():
    ARTIFACTS.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='roadmap-browser-') as tmp:
        root = Path(tmp)
        fixture.ARTIFACTS = root
        subprocess.run(['go', 'build', '-o', str(root / 'wzstudy.exe'), './cmd/api'], cwd=fixture.ROOT, check=True)
        errors, external = [], []
        with (root / 'service.log').open('wb') as log:
            fixture.start(root / 'data', log)
            try:
                with sync_playwright() as pw:
                    browser = pw.chromium.launch(channel='chrome', headless=True)
                    context = browser.new_context(viewport={'width': 1600, 'height': 1100}, locale='zh-CN', reduced_motion='reduce')
                    page = context.new_page()
                    page.set_default_timeout(15000)
                    page.on('pageerror', lambda error: errors.append(str(error)))
                    page.on('request', lambda request: external.append(request.url)
                            if request.url.startswith(('http://', 'https://')) and not request.url.startswith(fixture.BASE + '/') else None)
                    try:
                        run_browser(page, context, root / 'data', log)
                    except Exception:
                        page.screenshot(path=str(ARTIFACTS / 'failure.png'), full_page=True)
                        print('Browser errors:', json.dumps(errors, ensure_ascii=False), flush=True)
                        raise
                    assert not errors, errors
                    assert not external, external
                    context.close()
                    browser.close()
                    check('no JavaScript errors or external network requests')
            finally:
                fixture.stop()


if __name__ == '__main__':
    main()
