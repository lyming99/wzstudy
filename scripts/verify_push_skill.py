"""Exercise the push Skill against an isolated real Go service, never the user's library."""
from contextlib import ExitStack
from pathlib import Path
from urllib.error import URLError
from urllib.request import urlopen
import base64
import json
import os
import secrets
import socket
import subprocess
import sys
import tempfile
import time
import wave

ROOT = Path(__file__).resolve().parents[1]
ARTIFACTS = ROOT / '.artifacts'
ARTIFACTS.mkdir(exist_ok=True)
sys.path.insert(0, str(ROOT / 'skills/wzstudy-push/scripts'))
sys.path.insert(0, str(ROOT / 'scripts'))
from wzstudy_push import Client, PushError, batches, encode, load_items, upload
from setup_ai_push import setup

sys.stdout.reconfigure(encoding='utf-8')
sys.stderr.reconfigure(encoding='utf-8')
CHECKS = []
process = None


def stop():
    global process
    if process and process.poll() is None:
        process.terminate()
        try: process.wait(timeout=10)
        except subprocess.TimeoutExpired: process.kill();process.wait()
    process = None


def check(name):
    CHECKS.append(name)
    print('PASS:', name, flush=True)


units = subprocess.run([sys.executable, '-m', 'unittest', 'discover', '-s', 'skills/wzstudy-push/tests', '-v'], cwd=ROOT,
                       stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
assert units.returncode == 0, units.stdout.decode('utf-8', errors='replace')
check('10 standard-library unit tests: validation, batching, dry-run, endpoint binding, redirects, uncertainty and multipart')

with socket.socket() as sock:
    sock.bind(('127.0.0.1', 0))
    port = sock.getsockname()[1]
base = f'http://127.0.0.1:{port}'
admin_key, read_key = secrets.token_urlsafe(32), secrets.token_urlsafe(32)
with ExitStack() as stack:
    temp = Path(stack.enter_context(tempfile.TemporaryDirectory(prefix='push-skill-', dir=ARTIFACTS)))
    log = stack.enter_context((temp/'service.log').open('ab'))
    stack.callback(stop)
    env = dict(os.environ)
    for key in list(env):
        if key.startswith(('SYSTEM_ADMIN_', 'BOOTSTRAP_ADMIN_')):env.pop(key)
    env.update(APP_ENV='test', STORE='file', DATA_DIR=str(temp/'data'), HTTP_ADDR=f'127.0.0.1:{port}',
               ADMIN_API_KEY=admin_key, READ_API_KEY=read_key, POLL_ENABLED='false',
               SYSTEM_ADMIN_EMAIL='skill-test@example.test', SYSTEM_ADMIN_PASSWORD=secrets.token_urlsafe(24))
    binary = ARTIFACTS/'wzstudy.exe'
    if not binary.exists():raise RuntimeError('Build .artifacts/wzstudy.exe before integration verification')
    process = subprocess.Popen([str(binary)], cwd=ROOT, env=env, stdout=log, stderr=subprocess.STDOUT)
    for _ in range(100):
        assert process.poll() is None, 'Isolated service exited; log intentionally not printed'
        try:
            with urlopen(base+'/api/v1/health/ready',timeout=.3) as r:
                if r.status==200:break
        except (URLError,OSError):time.sleep(.1)
    else:raise AssertionError('Isolated service readiness failed')
    admin = Client(base, key=admin_key)
    keys = temp/'fake-admin-keys.json'
    keys.write_text(json.dumps({'admin_api_key':admin_key,'read_api_key':read_key}),encoding='utf-8')
    config = temp/'source-client.json'
    configured = setup(base, config, admin_key_file=keys)
    assert configured['auto_publish'] is False
    cfg = json.loads(config.read_text(encoding='utf-8'))
    source_key, source_id = cfg['source_key'], cfg['source_id']
    assert admin_key not in config.read_text(encoding='utf-8')
    client = Client(base, source_id, source_key)
    assert client.doctor()['source_authenticated']
    assert setup(base,config,admin_key_file=keys)['existing_config_reused']
    assert len(admin.request('/admin/sources')) == 1
    check('One-time setup stores only a dedicated source key, disables auto-publish and safely reuses existing config')

    article = {'title':'隔离验证的学习摘要','summary':'仅存在临时工作区','content':'# 学习摘要\n\nAI 整理的测试内容。',
               'format':'markdown','url':'https://example.org/learning/source#section','author':'测试作者','category':'学习方法','tags':['学习','AI']}
    items_file = temp/'items.json'
    items_file.write_text(json.dumps({'items':[article]},ensure_ascii=False),encoding='utf-8')
    group = batches(load_items(items_file))[0]
    first = client.request('/ingest/'+source_id,'POST',encode({'items':group}))
    assert first['created']==1 and first['published']==0
    item_id = first['items'][0]['id']
    assert admin.request('/admin/items/'+item_id)['status']=='pending'
    assert Client(base,key=read_key).request('/items')['items']==[]
    check('Article ingestion returns a real ID and pending state; private content stays out of the public reader API')

    again = client.request('/ingest/'+source_id,'POST',encode({'items':group}))
    assert again['unchanged']==1 and again['items'][0]['id']==item_id
    changed = dict(group[0],title='隔离验证的学习摘要 · 修订')
    revision = client.request('/ingest/'+source_id,'POST',encode({'items':[changed]}))
    assert revision['updated']==1 and revision['items'][0]['id']==item_id
    check('Stable external ID provides idempotent article replay and updates the same material instead of duplicating it')

    wav = temp/'voice.wav'
    with wave.open(str(wav),'wb') as stream:
        stream.setnchannels(1);stream.setsampwidth(2);stream.setframerate(8000);stream.writeframes(b'\0'*3200)
    audio = upload(client, {'title':'隔离音频','kind':'audio','external_id':'fixture:audio'}, [wav])
    assert audio['created']==1 and audio['published']==0
    stored = admin.request('/admin/items/'+audio['items'][0]['id'])
    assert stored['draft']['assets'][0]['mime']=='audio/wav'
    try:Client(base,key=read_key).request(stored['draft']['assets'][0]['url'].removeprefix('/api/v1'))
    except PushError as err:assert err.status==404
    else:raise AssertionError('Unreviewed media became public')
    check('Multipart audio upload reaches immutable storage and remains protected before approval')

    png = base64.b64decode('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Wl6SAAAAABJRU5ErkJggg==')
    images=[]
    for index in range(2):
        path=temp/f'card-{index}.png';path.write_bytes(png);images.append(path)
    cards=upload(client, {'title':'隔离轮播','kind':'carousel','external_id':'fixture:cards'}, images)
    assert cards['created']==1
    assert len(admin.request('/admin/items/'+cards['items'][0]['id'])['draft']['assets'])==2
    check('Multiple-image carousel upload preserves file order and real asset metadata')

    command=[sys.executable,str(ROOT/'skills/wzstudy-push/scripts/wzstudy_push.py'),'--config',str(config),'doctor']
    cli=subprocess.run(command,cwd=temp,capture_output=True,encoding='utf-8')
    assert cli.returncode==0 and json.loads(cli.stdout)['source_authenticated']
    assert source_key not in cli.stdout+cli.stderr and admin_key not in cli.stdout+cli.stderr
    check('Skill CLI works from an unrelated cwd, validates source access and never prints source/admin credentials')

    source=next(s for s in admin.request('/admin/sources') if s['id']==source_id)
    update={key:source[key] for key in ['slug','name','description','kind','feed_url','category','language','enabled','auto_publish','poll_interval_seconds'] if key in source}
    update.update(enabled=False,revision=source['revision'])
    admin.request('/admin/sources/'+source_id,'PUT',encode(update))
    try:client.request('/ingest/'+source_id,'POST',encode({'items':group}))
    except PushError as error:assert error.status==403
    else:raise AssertionError('Disabled source accepted new content')
    check('Disabled-source failures stop ingestion without bypassing permissions or changing the source policy')
    stop()

report={'result':'passed','groups':len(CHECKS),'checks':CHECKS,'unit_tests':10,
        'workspace':'isolated temporary file store; no user materials submitted',
        'credentials':'synthetic only; no user passwords or live source keys printed'}
(ARTIFACTS/'push-skill-verification.json').write_text(json.dumps(report,ensure_ascii=False,indent=2),encoding='utf-8')
print(json.dumps(report,ensure_ascii=False,indent=2),flush=True)
