"""Disposable browser regression for published catalogues and learning data.

Builds a temporary executable and uses a synthetic file-backed workspace. It
never reads user credentials or connects to a production instance.
"""
from pathlib import Path
import json
import os
import subprocess
import tempfile
import uuid
from urllib.parse import parse_qs, urlsplit

from playwright.sync_api import expect, sync_playwright
import verify_two_chains as fixture


def create_item(title):
    item = fixture.api('/admin/items', 'POST', {
        'status': 'pending',
        'content': {'kind': 'article', 'title': title, 'content': 'fixture body'},
    }, expected=201)
    return fixture.api('/admin/items/' + item['id'] + '/publish', 'POST', {'revision': item['revision']})


def assert_catalog_layout(page, published_id, draft_id):
    """Exercise the real catalogue UI with both short and unbroken long text."""
    for width in (1920, 1440, 1024, 1000, 768, 701, 700, 390, 320):
        page.set_viewport_size({'width': width, 'height': 1000})
        table = page.locator('.catalog-table')
        expect(page.get_by_role('table', name='合集列表')).to_be_visible()
        expect(page.get_by_role('columnheader', name='章节')).to_have_count(1)
        expect(table.locator('tbody tr')).to_have_count(2)
        expect(table.locator(f'[data-action=catalog-unpublish][data-id="{published_id}"]')).to_be_visible()
        expect(table.locator(f'[data-action=catalog-publish][data-id="{draft_id}"]')).to_have_text('发布')
        metrics = table.evaluate('''table => {
            const rect = e => {
                const r = e.getBoundingClientRect();
                return {left: r.left, right: r.right, top: r.top, width: r.width, height: r.height};
            };
            const wrapper = table.closest('.table-scroll');
            return {
                documentWidth: document.documentElement.scrollWidth,
                viewportWidth: innerWidth,
                wrapperWidth: wrapper.clientWidth,
                wrapperScrollWidth: wrapper.scrollWidth,
                rows: [...table.querySelectorAll('tbody tr')].map(row => ({
                    box: rect(row),
                    cells: [...row.cells].map(cell => ({...rect(cell), clientWidth: cell.clientWidth, scrollWidth: cell.scrollWidth})),
                    buttons: [...row.querySelectorAll('.catalog-actions button')].map(rect),
                    labels: [...row.querySelectorAll('td[data-label]')].map(cell => getComputedStyle(cell, '::before').content)
                }))
            };
        }''')
        assert metrics['documentWidth'] <= metrics['viewportWidth'] + 1, (width, metrics)
        assert metrics['wrapperScrollWidth'] <= metrics['wrapperWidth'] + 1, (width, metrics)
        for row in metrics['rows']:
            assert all(cell['scrollWidth'] <= cell['clientWidth'] + 1 for cell in row['cells']), (width, row)
            assert all(button['left'] >= row['box']['left'] - 1 and button['right'] <= row['box']['right'] + 1 for button in row['buttons']), (width, row)
            if width > 1000:
                assert row['cells'][0]['width'] >= 90, (width, row)
                assert max(button['top'] for button in row['buttons']) - min(button['top'] for button in row['buttons']) < 1, (width, row)
            else:
                assert all(label not in ('none', 'normal', '""') for label in row['labels']), (width, row)
                assert max(cell['top'] for cell in row['cells'][1:4]) - min(cell['top'] for cell in row['cells'][1:4]) < 1, (width, row)
        screenshot_dir = os.environ.get('WZSTUDY_CATALOG_SCREENSHOTS')
        if screenshot_dir and width in (1440, 390):
            Path(screenshot_dir).mkdir(parents=True, exist_ok=True)
            page.screenshot(path=str(Path(screenshot_dir) / f'catalog-{width}.png'), full_page=True)
    print('PASS: catalogue columns, readable actions and responsive cards at 320–1920 px', flush=True)


def main():
    temp_root = fixture.ROOT / '.tmp'
    temp_root.mkdir(exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='catalog-learning-', dir=temp_root) as tmp:
        root = Path(tmp)
        fixture.ARTIFACTS = root
        subprocess.run(['go', 'build', '-o', str(root / 'wzstudy.exe'), './cmd/api'], cwd=fixture.ROOT, check=True)
        errors, external = [], []
        with (root / 'service.log').open('wb') as log:
            fixture.start(root / 'data', log)
            try:
                first, second = create_item('合集测试资料甲'), create_item('合集测试资料乙')
                created = fixture.api('/admin/clients', 'POST', {
                    'slug': 'browser-learner', 'name': '浏览器测试学习者',
                    'permissions': ['read', 'sync', 'feedback'],
                }, expected=201)
                client, key = created['client'], created['access_key']
                events = [
                    {'event_id': str(uuid.uuid4()), 'item_id': first['id'], 'action': 'view'},
                    {'event_id': str(uuid.uuid4()), 'item_id': first['id'], 'action': 'review_plan', 'value': True},
                    {'event_id': str(uuid.uuid4()), 'item_id': first['id'], 'action': 'record_create',
                     'record_id': str(uuid.uuid4()), 'text': '<script>window.catalog_xss=1</script>私有记录'},
                ]
                fixture.api('/client/learning/events', 'POST', {'events': events}, key=key)
                before_learning = fixture.api('/client/learning/events?after=0', key=key)['high_watermark']
                before_content = fixture.api('/changes?after=0', key=fixture.READ)['high_watermark']
                with sync_playwright() as pw:
                    browser = pw.chromium.launch(channel='chrome', headless=True)
                    context = browser.new_context(viewport={'width': 1440, 'height': 1000}, locale='zh-CN', reduced_motion='reduce')
                    page = context.new_page()
                    page.on('pageerror', lambda error: errors.append(str(error)))
                    page.on('request', lambda request: external.append(request.url) if request.url.startswith(('http://', 'https://')) and not request.url.startswith(fixture.BASE + '/') else None)
                    page.goto(fixture.BASE + '/admin/#catalog')
                    fixture.login(page)
                    expect(page.locator('h1')).to_have_text('合集管理')
                    expect(page.locator('#catalog-results .empty h3')).to_have_text('暂无合集')
                    expect(page.locator('[data-action=catalog-page]')).to_have_count(2)
                    page.locator('[data-action=catalog-new]').click()
                    page.locator('#catalog-title').fill('浏览器合集')
                    page.locator('#catalog-summary').fill('测试简介')
                    for title, item in [('第一章', first), ('第二章', second)]:
                        page.locator('[data-action=catalog-chapter-add]').click()
                        row = page.locator('.catalog-chapter').last
                        row.locator('.catalog-chapter-title').fill(title)
                        row.locator('.catalog-chapter-item').select_option(item['id'])
                    # An older search response must not replace the latest result,
                    # and an already selected chapter keeps its readable title.
                    held = []
                    def delay_old_search(route):
                        if parse_qs(urlsplit(route.request.url).query).get('q') == ['旧搜索']:
                            held.append(route)
                        else:
                            route.continue_()
                    page.route('**/api/v1/items?*', delay_old_search)
                    search = page.locator('#catalog-search')
                    search.fill('旧搜索')
                    page.locator('[data-action=catalog-search]').click()
                    assert held
                    search.fill('资料乙')
                    search.press('Enter')
                    expect(page.locator('#catalog-search-status')).to_contain_text('找到 1 条')
                    held.pop().fulfill(status=200, content_type='application/json', body=json.dumps({'data': {'items': [first]}}))
                    page.wait_for_timeout(100)
                    expect(page.locator('#catalog-search-status')).to_contain_text('找到 1 条')
                    assert page.locator('.catalog-chapter-item').first.locator('option:checked').text_content() == '合集测试资料甲'
                    page.unroute('**/api/v1/items?*', delay_old_search)
                    page.locator('.catalog-chapter').first.locator('[data-action=catalog-chapter-down]').click()
                    assert page.locator('.catalog-chapter-title').first.input_value() == '第二章'
                    page.locator('button[form=catalog-form]').click()
                    expect(page.locator('.dialog')).to_have_count(0)
                    expect(page.locator('#catalog-results')).to_contain_text('浏览器合集')
                    collection = fixture.api('/admin/catalog/collections?limit=10&offset=0')['items'][0]
                    page.locator(f'[data-action=catalog-publish][data-id="{collection["id"]}"]').click()
                    page.locator('#confirm-operation').click()
                    expect(page.locator('.dialog')).to_have_count(0)
                    public = fixture.api('/catalog/collections/' + collection['id'], key=fixture.READ)
                    assert [c['item_id'] for c in public['chapters']] == [second['id'], first['id']]
                    page.locator(f'[data-action=catalog-edit][data-id="{collection["id"]}"]').click()
                    page.locator('#catalog-title').fill('未发布修改')
                    page.locator('button[form=catalog-form]').click()
                    expect(page.locator('.dialog')).to_have_count(0)
                    expect(page.locator('#catalog-results')).to_contain_text('草稿有未发布修改')
                    assert fixture.api('/catalog/collections/' + collection['id'], key=fixture.READ)['title'] == '浏览器合集'
                    fixture.api('/admin/items/' + second['id'] + '/unpublish', 'POST', {'revision': second['revision']})
                    page.reload()
                    expect(page.locator('#catalog-results')).to_contain_text('草稿有未发布修改')
                    public = fixture.api('/catalog/collections/' + collection['id'], key=fixture.READ)
                    assert [c['item_id'] for c in public['chapters']] == [first['id']]
                    long_title = '布局测试' + 'LONGTITLE' * 16
                    long_summary = '<img src=x onerror="window.catalog_layout_xss=1">' + '无空格长简介' * 35
                    draft = fixture.api('/admin/catalog/collections', 'POST', {
                        'revision': 0, 'title': long_title, 'summary': long_summary, 'chapters': [],
                    }, expected=201)
                    page.reload()
                    expect(page.locator('#catalog-results')).to_contain_text(long_title)
                    expect(page.locator('#catalog-results')).to_contain_text('草稿有未发布修改')
                    expect(page.locator('.catalog-summary').filter(has_text='<img src=x')).to_have_count(1)
                    assert page.locator('.catalog-table img').count() == 0
                    assert page.evaluate('window.catalog_layout_xss') is None
                    assert_catalog_layout(page, collection['id'], draft['id'])
                    # Existing actions still open the correct confirmation, including on narrow screens.
                    page.locator(f'[data-action=catalog-unpublish][data-id="{collection["id"]}"]').click()
                    expect(page.locator('#dialog-title')).to_have_text('下架合集？')
                    fixture.close_modal(page)
                    delete_button = page.locator(f'[data-action=catalog-delete][data-id="{draft["id"]}"]')
                    delete_button.focus()
                    delete_button.press('Enter')
                    expect(page.locator('#dialog-title')).to_have_text('删除合集？')
                    fixture.close_modal(page)
                    for index in range(9):
                        fixture.api('/admin/catalog/collections', 'POST', {
                            'revision': 0, 'title': f'分页测试 {index}', 'summary': '', 'chapters': [],
                        }, expected=201)
                    page.reload()
                    expect(page.locator('#catalog-results .pagination')).to_contain_text('共 11 个合集 · 第 1 页')
                    page.locator('[data-action=catalog-page][data-value="2"]').click()
                    expect(page.locator('#catalog-results .pagination')).to_contain_text('第 2 页')
                    expect(page.locator('.catalog-table tbody tr')).to_have_count(1)
                    page.locator('[data-action=catalog-page][data-value="1"]').click()
                    expect(page.locator('.catalog-table tbody tr')).to_have_count(10)
                    fixture.verify_client_navigation(page)
                    page.set_viewport_size({'width': 1440, 'height': 1000})
                    page.locator('.sidebar [data-route=clients]').click()
                    page.locator(f'[data-action=client-learning][data-id="{client["id"]}"]').click()
                    expect(page.locator('.learning-state-dialog')).to_contain_text('查看 1 次')
                    expect(page.locator('.learning-state-dialog')).to_contain_text('待复习')
                    page.locator(f'[data-action=client-learning-records][data-value="{first["id"]}"]').click()
                    expect(page.locator('.learning-state-dialog')).to_contain_text('私人学习记录')
                    page.locator('.learning-state-dialog details summary').click()
                    expect(page.locator('.learning-state-dialog .feedback-note')).to_contain_text('<script>window.catalog_xss=1</script>私有记录')
                    assert page.evaluate('window.catalog_xss') is None
                    assert page.locator('.learning-state-dialog .feedback-note script').count() == 0
                    page.locator('[data-action=client-learning]').last.click()
                    page.locator('[data-action=client-learning-events]').click()
                    expect(page.locator('.learning-state-dialog')).to_contain_text('有效查看')
                    fixture.close_modal(page)
                    assert fixture.api('/client/learning/events?after=0', key=key)['high_watermark'] == before_learning
                    assert fixture.api('/changes?after=0', key=fixture.READ)['high_watermark'] == before_content + 1
                    assert not errors, errors
                    assert not external, external
                    context.close()
                    browser.close()
                print('PASS: catalog editor, ordering, drafts, published visibility, learning data, privacy escaping and read-only administration', flush=True)
            finally:
                fixture.stop()


if __name__ == '__main__':
    main()
