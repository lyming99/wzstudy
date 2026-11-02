"""Focus-style regression with a temporary file-store service and fixture data only.

Optional --baseline-css captures the original workspace stylesheet for comparison.
No running service or existing business data is read, restarted, or changed.
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

ARTIFACTS = ROOT / '.artifacts' / 'focus-styles'
FOCUS = 'rgb(52, 114, 79)'
MEASURE = """input => {
  const outer = input.closest('.search-field, .global-search') || input;
  const css = getComputedStyle(outer), inner = getComputedStyle(input);
  const rect = outer.getBoundingClientRect();
  return {border: css.borderTopColor, borderWidth: css.borderTopWidth,
    radius: css.borderTopLeftRadius, shadow: css.boxShadow,
    outline: css.outlineStyle, outlineWidth: css.outlineWidth,
    innerOutline: inner.outlineStyle, innerOutlineWidth: inner.outlineWidth,
    duration: css.transitionDuration, focusVisible: input.matches(':focus-visible'),
    rect: {x: rect.x, y: rect.y, width: rect.width, height: rect.height}};
}"""


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--baseline-css', type=Path)
    args = parser.parse_args()
    ARTIFACTS.mkdir(parents=True, exist_ok=True)
    results, errors, external = {}, [], []
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        port = sock.getsockname()[1]
    base = f'http://127.0.0.1:{port}'
    admin_email, email = 'admin@focus.test', 'learner@focus.test'
    admin_password, password = secrets.token_urlsafe(24), secrets.token_urlsafe(24)
    admin_key = secrets.token_urlsafe(32)
    with tempfile.TemporaryDirectory(prefix='focus-service-', dir=ARTIFACTS) as temp:
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
                call(base, '/admin/users', 'POST', {'email': email, 'display_name': '聚焦测试用户',
                     'password': password}, admin_key)
                token = call(base, '/user/auth/login', 'POST', {'email': email,
                             'password': password, 'session_mode': 'token'})['access_token']
                for title in ['聚焦检查甲', '阅读资料乙', '学习笔记丙']:
                    call(base, '/workspace/items', 'POST', {'status': 'pending', 'content': {
                        'kind': 'article', 'title': title, 'category': '学习方法',
                        'tags': ['学习'], 'summary': '只用于焦点样式的隔离回归。',
                        'content': '这是临时测试数据，不写入已有业务库。', 'format': 'text'}}, token)
                with sync_playwright() as pw:
                    browser = pw.chromium.launch(headless=True)
                    try:
                        context = browser.new_context(viewport={'width': 1440, 'height': 1100},
                                                      locale='zh-CN', reduced_motion='reduce')
                        page = context.new_page()
                        page.set_default_timeout(15000)
                        page.on('pageerror', lambda error: errors.append(str(error)))
                        page.on('request', lambda request: external.append(request.url)
                                if request.url.startswith(('http://', 'https://'))
                                and not request.url.startswith(base + '/') else None)
                        pattern = '**/workspace/workspace.css'
                        if args.baseline_css:
                            baseline = args.baseline_css.resolve()
                            page.route(pattern, lambda route: route.fulfill(path=str(baseline),
                                                                           content_type='text/css'))
                        login_workbench(page, base, email, password, 'user')
                        page.locator('.sidebar [data-route="library"]').click()
                        expect(page.locator('.material-name')).to_have_count(3)
                        if args.baseline_css:
                            page.locator('#library-search').click()
                            results['baseline'] = page.locator('#library-search').evaluate(MEASURE)
                            page.locator('.library-panel').screenshot(path=str(ARTIFACTS / 'before.png'))
                            page.unroute(pattern)
                            page.reload()
                            expect(page.locator('.material-name')).to_have_count(3)

                        def focused(selector, name):
                            field = page.locator(selector)
                            expect(field).to_be_focused()
                            value = field.evaluate(MEASURE)
                            assert value['border'] == FOCUS, (name, value)
                            assert value['borderWidth'] == '1px' and value['radius'] == '8px', (name, value)
                            assert value['shadow'] != 'none' and value['shadow'].endswith('0px 0px 0px 3px'), (name, value)
                            assert value['innerOutline'] == 'none', (name, value)
                            results[name] = value
                            return value

                        search = page.locator('#library-search')
                        idle = search.evaluate(MEASURE)
                        search.click()
                        pointer = focused('#library-search', 'pointer-search')
                        assert idle['rect'] == pointer['rect'], 'focus changed the search geometry'
                        page.locator('.library-panel').screenshot(path=str(ARTIFACTS / 'after.png'))
                        page.keyboard.press('Shift+Tab')
                        page.keyboard.press('Tab')
                        keyboard = focused('#library-search', 'keyboard-search')
                        assert keyboard['focusVisible'], keyboard
                        page.keyboard.press('Tab')
                        focused('#type-filter', 'keyboard-type-filter')
                        blurred = search.evaluate(MEASURE)
                        assert blurred['shadow'] == 'none' and blurred['border'] != FOCUS, blurred
                        page.keyboard.press('Tab')
                        focused('#category-filter', 'keyboard-category-filter')
                        page.keyboard.press('Tab')
                        focused('#content-mode-filter', 'keyboard-mode-filter')
                        print('PASS: pointer/Tab focus shares one rounded outer ring; blur restores it without layout shifts', flush=True)

                        page.locator('.heading-actions [data-action="upload"]').focus()
                        assert page.locator('.heading-actions [data-action="upload"]').evaluate(
                            "el => getComputedStyle(el).outlineStyle === 'solid' && getComputedStyle(el).outlineWidth === '3px'")
                        page.locator('#global-search').click()
                        focused('#global-search', 'global-search')
                        page.locator('.global-search').screenshot(path=str(ARTIFACTS / 'global-search.png'))
                        search.fill('聚焦检查甲')
                        expect(page.locator('.material-name')).to_have_count(1)
                        expect(page.locator('.material-name')).to_have_text('聚焦检查甲')
                        search.fill('')
                        expect(page.locator('.material-name')).to_have_count(3)
                        print('PASS: global search uses the same ring; button keyboard indicators and live filtering remain intact', flush=True)

                        for width in [1440, 768, 390, 320]:
                            page.set_viewport_size({'width': width, 'height': 1100})
                            search.click()
                            value = focused('#library-search', f'responsive-{width}')
                            assert page.evaluate('document.documentElement.scrollWidth <= innerWidth'), width
                            assert value['rect']['x'] >= 3 and value['rect']['x'] + value['rect']['width'] <= width - 3, value
                            if width in [390, 320]:
                                page.locator('.library-panel').screenshot(path=str(ARTIFACTS / f'mobile-{width}.png'))
                        print('PASS: 320/390/768/1440px layouts retain visible focus and no page overflow', flush=True)

                        page.set_viewport_size({'width': 1440, 'height': 1100})
                        page.emulate_media(forced_colors='active')
                        for selector in ['#library-search', '#global-search', '#type-filter']:
                            page.locator(selector).focus()
                            value = page.locator(selector).evaluate(MEASURE)
                            assert value['outline'] == 'solid' and value['outlineWidth'] == '2px', (selector, value)
                            assert value['shadow'] == 'none', (selector, value)
                            results['forced-colors-' + selector[1:]] = value
                        page.locator('#library-search').focus()
                        page.locator('.library-panel').screenshot(path=str(ARTIFACTS / 'high-contrast.png'))
                        page.emulate_media(forced_colors='none', reduced_motion='no-preference')
                        normal_duration = search.evaluate(MEASURE)['duration']
                        assert {value.strip() for value in normal_duration.split(',')} == {'0.16s'}, normal_duration
                        page.emulate_media(reduced_motion='reduce')
                        reduced_duration = search.evaluate(MEASURE)['duration']
                        assert {value.strip() for value in reduced_duration.split(',')} == {'0s'}, reduced_duration
                        results['motion'] = {'normal': normal_duration, 'reduced': reduced_duration}
                        assert not errors, errors
                        assert not external, external
                        print('PASS: forced-colors fallback and reduced-motion preference work; no page errors or external requests', flush=True)
                        (ARTIFACTS / 'measurements.json').write_text(
                            json.dumps(results, ensure_ascii=False, indent=2), encoding='utf-8')
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
    print(f'PASS: focus regression complete; screenshots and measurements: {ARTIFACTS}', flush=True)


if __name__ == '__main__':
    main()
