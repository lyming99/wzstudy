"""Receipt-table layout regression using real receipts in an isolated file store.

Use --baseline to also capture the original app.js/workspace.css copied into
.artifacts/receipt-layout. Never reads or changes an existing running service.
"""
from pathlib import Path
from urllib.error import URLError
import argparse
import json
import os
import secrets
import socket
import subprocess
import tempfile
import time

from playwright.sync_api import expect, sync_playwright
from verify_shared_workbench import ROOT, call, login_workbench

ARTIFACTS = ROOT / '.artifacts' / 'receipt-layout'
AUDIT = """() => {
  const scroll = document.querySelector('.receipt-scroll');
  const table = scroll.querySelector('table');
  const issues = [];
  const headers = [...table.querySelectorAll('th')].map((cell, index) => {
    const rect = cell.getBoundingClientRect(), css = getComputedStyle(cell);
    const range = document.createRange(); range.selectNodeContents(cell);
    const text = range.getBoundingClientRect();
    if (range.getClientRects().length !== 1 || text.right > rect.right - parseFloat(css.paddingRight) + 1)
      issues.push({kind: 'header-overflow', index, text: cell.textContent});
    return {text: cell.textContent, width: rect.width, top: rect.top, height: rect.height};
  });
  for (const row of table.tBodies[0].rows) {
    for (const [index, cell] of [...row.cells].entries()) {
      if (cell.scrollWidth > cell.clientWidth + 1)
        issues.push({kind: 'cell-overflow', index, text: cell.textContent.slice(0, 80)});
      for (const button of cell.querySelectorAll('button')) {
        const rect = button.getBoundingClientRect(), parent = cell.getBoundingClientRect();
        if (rect.left < parent.left - 1 || rect.right > parent.right + 1)
          issues.push({kind: 'button-overlap', index});
      }
    }
  }
  return {viewport: innerWidth, pageWidth: document.documentElement.scrollWidth,
    clientWidth: scroll.clientWidth, scrollWidth: scroll.scrollWidth,
    clientHeight: scroll.clientHeight, scrollHeight: scroll.scrollHeight,
    overflowX: getComputedStyle(scroll).overflowX, overflowY: getComputedStyle(scroll).overflowY,
    headers, issues};
}"""


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--baseline', action='store_true')
    args = parser.parse_args()
    ARTIFACTS.mkdir(parents=True, exist_ok=True)
    results, errors, external = {}, [], []
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        port = sock.getsockname()[1]
    base = f'http://127.0.0.1:{port}'
    admin_key = secrets.token_urlsafe(32)
    admin_email, email = 'admin@receipt.test', 'learner@receipt.test'
    admin_password, password = secrets.token_urlsafe(24), secrets.token_urlsafe(24)
    with tempfile.TemporaryDirectory(prefix='receipt-service-', dir=ARTIFACTS) as temp:
        directory = Path(temp)
        binary = directory / ('wzstudy.exe' if os.name == 'nt' else 'wzstudy')
        subprocess.run(['go', 'build', '-o', str(binary), './cmd/api'], cwd=ROOT, check=True)
        env = dict(os.environ)
        for name in list(env):
            if name.startswith(('SYSTEM_ADMIN_', 'BOOTSTRAP_ADMIN_')):
                env.pop(name)
        env.update(APP_ENV='test', STORE='file', DATA_DIR=str(directory / 'data'),
                   HTTP_ADDR=f'127.0.0.1:{port}', ADMIN_API_KEY=admin_key,
                   READ_API_KEY=secrets.token_urlsafe(32), SYSTEM_ADMIN_EMAIL=admin_email,
                   SYSTEM_ADMIN_PASSWORD=admin_password, POLL_ENABLED='false',
                   AGENT_ENABLED='false', CORS_ORIGINS='', UPLOAD_MAX_MB='10')
        with (directory / 'service.log').open('ab') as log:
            server = subprocess.Popen([str(binary)], cwd=ROOT, env=env, stdout=log, stderr=log)
            try:
                for _ in range(100):
                    if server.poll() is not None:
                        raise RuntimeError('isolated service exited before readiness')
                    try:
                        call(base, '/health/ready')
                        break
                    except (URLError, OSError):
                        time.sleep(.1)
                else:
                    raise RuntimeError('isolated service readiness timeout')
                call(base, '/admin/users', 'POST', {'email': email, 'display_name': '回执布局测试',
                     'password': password}, admin_key)
                token = call(base, '/user/auth/login', 'POST', {'email': email,
                             'password': password, 'session_mode': 'token'})['access_token']
                sources = []
                for index, name in enumerate(['超长采集来源 · ' + 'Source' * 20, '另一个测试来源', '空来源']):
                    sources.append(call(base, '/workspace/sources', 'POST', {
                        'slug': f'layout-source-{index}', 'name': name,
                        'permissions': ['ingest', 'upload']}, token))
                receipts = []
                for batch in range(23):
                    source = sources[batch % 2]
                    items = [{'external_id': f'layout-{batch}-{index}', 'kind': 'article',
                              'title': f'隔离回执资料 {batch}-{index}',
                              'content': '隔离回执布局测试正文。用于验证真实资料详情，不写入已有业务库。'}
                             for index in range(12 if batch == 22 else 3)]
                    receipt = call(base, '/ingest/' + source['source']['id'], 'POST',
                                   {'items': items}, source['access_key'])
                    assert receipt['receipt_id'] and receipt['created'] == len(items), receipt
                    receipts.append(receipt['receipt_id'])
                listing = call(base, '/workspace/intakes?limit=20&offset=0', key=token)
                assert listing['total'] == 23 and len(listing['items']) == 20
                print('PASS: created 23 real receipts, including a 12-item batch and a long source name, in temporary storage', flush=True)
                with sync_playwright() as pw:
                    browser = pw.chromium.launch(headless=True)
                    try:
                        context = browser.new_context(viewport={'width': 1100, 'height': 900},
                                                      reduced_motion='reduce', locale='zh-CN')
                        page = context.new_page()
                        page.set_default_timeout(15000)
                        page.on('pageerror', lambda error: errors.append(str(error)))
                        page.on('request', lambda request: external.append(request.url)
                                if request.url.startswith(('http://', 'https://'))
                                and not request.url.startswith(base + '/') else None)
                        patterns = ['**/workspace/app.js', '**/workspace/workspace.css']
                        if args.baseline:
                            for name, pattern in zip(['app.js', 'workspace.css'], patterns):
                                path = str(ARTIFACTS / ('baseline-' + name))
                                mime = 'text/javascript' if name.endswith('.js') else 'text/css'
                                page.route(pattern, lambda route, request, path=path, mime=mime:
                                           route.fulfill(path=path, content_type=mime))
                        login_workbench(page, base, email, password, 'user')
                        page.goto(base + '/workspace/#receipts')
                        expect(page.locator('.receipt-table tbody tr')).to_have_count(20)
                        if args.baseline:
                            page.screenshot(path=str(ARTIFACTS / 'before.png'))
                            for pattern in patterns:
                                page.unroute(pattern)
                            page.reload()
                            expect(page.locator('.receipt-scroll')).to_be_visible()

                        def audit(name):
                            value = page.evaluate(AUDIT)
                            results[name] = value
                            assert value['pageWidth'] <= value['viewport'] + 1, (name, value)
                            assert value['overflowX'] == value['overflowY'] == 'auto', value
                            assert value['headers'][5]['width'] >= 108 - 1, value
                            assert not value['issues'], (name, value['issues'])
                            return value

                        for width in [2226, 1440, 1280, 1100, 768, 390, 320]:
                            page.set_viewport_size({'width': width, 'height': 900})
                            value = audit(str(width))
                            assert value['scrollHeight'] > value['clientHeight'], value
                            assert value['clientHeight'] <= 585 + 1, value
                            if width <= 1100:
                                assert value['scrollWidth'] > value['clientWidth'], value
                            if width in [2226, 1100, 390]:
                                page.locator('.receipt-scroll').evaluate('el => {el.scrollTop=0;el.scrollLeft=0}')
                                page.locator('.receipts-panel').screenshot(path=str(ARTIFACTS / f'after-{width}.png'))
                                if width == 1100:
                                    page.locator('.receipt-scroll').evaluate('el => {el.scrollLeft=el.scrollWidth}')
                                    page.locator('.receipts-panel').screenshot(path=str(ARTIFACTS / 'after-scrolled-right.png'))
                        assert results['2226']['scrollWidth'] <= results['2226']['clientWidth'] + 1, results['2226']
                        print('PASS: seven columns stay readable at 320-2226px; overflow scrolls inside the panel rather than the page', flush=True)

                        page.set_viewport_size({'width': 1100, 'height': 900})
                        scroll = page.locator('.receipt-scroll')
                        expect(scroll).to_have_attribute('role', 'region')
                        expect(scroll).to_have_attribute('tabindex', '0')
                        expect(page.locator('.receipt-table th[scope="col"]')).to_have_count(7)
                        scroll.focus()
                        page.keyboard.press('ArrowRight')
                        page.wait_for_function("() => document.querySelector('.receipt-scroll').scrollLeft > 0")
                        scroll.evaluate('el => {el.scrollTop=220;el.scrollLeft=0}')
                        assert scroll.evaluate('el => el.scrollTop') > 0
                        assert page.locator('.receipt-table th').first.evaluate(
                            "el => Math.abs(el.getBoundingClientRect().top - el.closest('.receipt-scroll').getBoundingClientRect().top) < 3")
                        scroll.screenshot(path=str(ARTIFACTS / 'sticky-header.png'))
                        assert page.evaluate('scrollX') == 0
                        print('PASS: keyboard horizontal scrolling and sticky headers work; document does not scroll sideways', flush=True)

                        scroll.evaluate('el => {el.scrollTop=0;el.scrollLeft=el.scrollWidth}')
                        detail = page.locator('.receipt-table tbody tr').first.locator('[data-action="detail"]').first
                        detail.click()
                        expect(page.locator('#drawer-root .drawer')).to_be_visible()
                        expect(page.locator('#drawer-root')).to_contain_text('隔离回执资料')
                        page.locator('#drawer-root [data-action="close-drawer"]').click()
                        page.locator('[data-action="intake-page"][data-value="2"]').click()
                        expect(page.locator('.receipt-table tbody tr')).to_have_count(3)
                        expect(page.locator('.receipts-panel .pagination')).to_contain_text('第 2 页')
                        page.locator('#receipt-source').select_option(sources[0]['source']['id'])
                        expect(page.locator('.receipt-table tbody tr')).to_have_count(12)
                        expect(page.locator('.receipts-panel .pagination')).to_contain_text('第 1 页')
                        audit('filtered-long-source')
                        page.locator('#receipt-source').select_option(sources[2]['source']['id'])
                        expect(page.locator('#intake-results')).to_contain_text('暂无入库回执')
                        expect(page.locator('.receipt-scroll')).to_have_count(0)
                        page.locator('#receipt-source').select_option('')
                        expect(page.locator('.receipt-table tbody tr')).to_have_count(20)
                        print('PASS: real item details, source filtering, page reset, next page, and empty-state rendering remain intact', flush=True)

                        page.locator('.sidebar [data-route="library"]').click()
                        expect(page.locator('#library-search')).to_be_visible()
                        page.locator('#library-search').click()
                        assert page.locator('#library-search').evaluate(
                            "el => getComputedStyle(el).outlineStyle === 'none' && getComputedStyle(el.closest('.search-field')).boxShadow !== 'none'")
                        assert not errors, errors
                        assert not external, external
                        (ARTIFACTS / 'measurements.json').write_text(
                            json.dumps(results, ensure_ascii=False, indent=2), encoding='utf-8')
                        print('PASS: previous focus styling is preserved; no page errors or external requests', flush=True)
                        context.close()
                    finally:
                        browser.close()
            finally:
                server.terminate()
                try:
                    server.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    server.kill()
                    server.wait(timeout=10)
    print(f'PASS: receipt layout regression complete; artifacts: {ARTIFACTS}', flush=True)


if __name__ == '__main__':
    main()
