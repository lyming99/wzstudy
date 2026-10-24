"""Smoke-test the portable Windows archive without reading existing credentials.
Extracts to a private temporary directory, starts its own process tree, and
always stops only that owned tree. Generated test keys are never printed.
"""
from pathlib import Path
from urllib.request import Request, urlopen
from urllib.error import URLError
import hashlib
import argparse
import json
import os
import shutil
import socket
import secrets
import subprocess
import sys
import tempfile
import time
import zipfile
from contextlib import ExitStack

sys.stdout.reconfigure(encoding='utf-8')
sys.stderr.reconfigure(encoding='utf-8')
ROOT=Path(__file__).resolve().parents[1]
ARCHIVE=ROOT/'dist/wzstudy-v0.3.0-windows-amd64.zip'
REPORT=ROOT/'.artifacts/package-verification.json'
assert os.name=='nt','This smoke test targets the Windows portable package'
parser=argparse.ArgumentParser(description=__doc__)
parser.add_argument('--source',action='store_true',help='Also start the repository launcher with an isolated profile and verify bin/dist separation')
options=parser.parse_args()
with socket.socket() as s:
    s.bind(('127.0.0.1',0));port=s.getsockname()[1]
base=f'http://127.0.0.1:{port}'
process=None
checks=[]
bootstrap_email="portable@example.test"
bootstrap_password=secrets.token_urlsafe(24)
child_env={**os.environ,"SYSTEM_ADMIN_EMAIL":bootstrap_email,"SYSTEM_ADMIN_PASSWORD":bootstrap_password,"BOOTSTRAP_ADMIN_EMAIL":"","BOOTSTRAP_ADMIN_PASSWORD":""}

def stop_owned():
    global process
    if process is not None:
        subprocess.run(['taskkill','/PID',str(process.pid),'/T','/F'],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,check=False)
        try: process.wait(timeout=10)
        except subprocess.TimeoutExpired: process.kill();process.wait()
        if process.stdout: process.stdout.close()
        process=None

try:
    with ExitStack() as stack:
        temp=stack.enter_context(tempfile.TemporaryDirectory(prefix='package-smoke-',dir=ROOT/'.artifacts'))
        stack.callback(stop_owned)
        target=Path(temp)
        with zipfile.ZipFile(ARCHIVE) as archive:
            for name in archive.namelist():
                parts=Path(name).parts
                assert not Path(name).is_absolute() and '..' not in parts
                assert '.local' not in parts and '.env' not in parts
            assert archive.testzip() is None
            archive.extractall(target)
        manifest=(target/'SHA256SUMS.txt').read_text(encoding='utf-8').splitlines()
        for line in manifest:
            digest,name=line.split('  ',1)
            assert hashlib.sha256((target/name).read_bytes()).hexdigest()==digest,name
        checks.append('Archive CRC and every manifest checksum match; no local data or credentials packaged')
        assert not (target/'go.mod').exists() and not (target/'internal/adminui').exists(), 'Smoke test must use the packaged binary, not Go source'
        powershell=shutil.which('powershell.exe')
        assert powershell
        process=subprocess.Popen([powershell,'-NoLogo','-NoProfile','-NonInteractive','-ExecutionPolicy','RemoteSigned','-File',str(target/'start-local.ps1'),'-Address',f'127.0.0.1:{port}','-NoEnvFile'],cwd=target.parent,env=child_env,stdout=subprocess.PIPE,stderr=subprocess.STDOUT)
        ready=False
        for _ in range(150):
            if process.poll() is not None:
                raise RuntimeError('Portable launcher exited early; startup output intentionally not printed because it can contain generated keys')
            try:
                with urlopen(base+'/api/v1/health/ready',timeout=.4) as r:
                    if r.status==200:
                        ready=True;break
            except (URLError,OSError):
                time.sleep(.1)
        assert ready,'Portable service did not become ready'
        checks.append('Extracted portable launcher starts without Go project sources, Node, or database setup')
        credentials=json.loads((target/'.local/access-keys.json').read_text(encoding='utf-8-sig'))
        admin=credentials['admin_api_key'];read=credentials['read_api_key']
        assert len(admin)>=32 and len(read)>=32 and admin!=read
        login_body=json.dumps({'email':bootstrap_email,'password':bootstrap_password}).encode()
        req=Request(base+'/api/v1/auth/login',method='POST',data=login_body,headers={'Content-Type':'application/json'})
        with urlopen(req,timeout=5) as r:assert json.load(r)['data']['administrator']['email']==bootstrap_email
        checks.append('Configured email/password initialize the administrator; browser login does not use the API key')
        req=Request(base+'/api/v1/admin/workspace',headers={'Authorization':'Bearer '+admin})
        with urlopen(req,timeout=5) as r:
            workspace=json.load(r)['data']
        assert workspace['summary']['total']==0
        settings=json.dumps({'name':'Portable smoke test','compact':False,'revision':0}).encode()
        req=Request(base+'/api/v1/admin/settings',method='PUT',data=settings,headers={'Authorization':'Bearer '+admin,'Content-Type':'application/json'})
        with urlopen(req,timeout=5) as r:
            assert r.status==200
        assert (target/'.local/data/workspace.json').exists()
        checks.append('Fresh random keys authenticate and real file storage is created inside the extracted package')
        for path,content in [('/admin/','/admin/app.js'),('/docs/management','v0.3.0'),('/openapi.json','0.3.0')]:
            with urlopen(base+path,timeout=5) as r:
                assert r.status==200 and content in r.read().decode('utf-8')
        checks.append('Embedded management page and complete API documentation are available from the packaged executable')
        acl=subprocess.run([powershell,'-NoProfile','-NonInteractive','-Command',f"(Get-Acl -LiteralPath '{target / '.local/access-keys.json'}').AreAccessRulesProtected"],stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=10)
        assert acl.returncode==0 and acl.stdout.decode(errors='replace').strip().lower()=='true'
        checks.append('Generated Windows credential file has inheritance disabled by the startup script')
        stop_owned()
        for _ in range(30):
            try:
                urlopen(base+'/api/v1/health/live',timeout=.2).close()
                time.sleep(.1)
            except (URLError,OSError):
                break
        else:
            raise AssertionError('Owned service tree did not stop')
        checks.append('Only the smoke-test process tree is stopped and its listener is released')
        if options.source:
            source_profile=target/'isolated-source-profile'
            release_binary=ROOT/'dist/wzstudy-v0.3.0-windows-amd64.exe'
            release_hash=hashlib.sha256(release_binary.read_bytes()).hexdigest()
            process=subprocess.Popen([powershell,'-NoLogo','-NoProfile','-NonInteractive','-ExecutionPolicy','RemoteSigned','-File',str(ROOT/'start-local.ps1'),'-Address',f'127.0.0.1:{port}','-ProfileDirectory',str(source_profile),'-NoEnvFile'],cwd=target.parent,env=child_env,stdout=subprocess.PIPE,stderr=subprocess.STDOUT)
            ready=False
            for _ in range(300):
                if process.poll() is not None:raise RuntimeError('Source launcher exited; secret-bearing startup output intentionally not printed')
                try:
                    with urlopen(base+'/api/v1/health/ready',timeout=.4) as r:
                        if r.status==200:ready=True;break
                except (URLError,OSError):time.sleep(.1)
            assert ready,'Source launcher readiness failed'
            source_keys=json.loads((source_profile/'access-keys.json').read_text(encoding='utf-8-sig'))
            req=Request(base+'/api/v1/admin/workspace',headers={'Authorization':'Bearer '+source_keys['admin_api_key']})
            with urlopen(req,timeout=5) as r:assert json.load(r)['data']['summary']['total']==0
            assert (ROOT/'bin/wzstudy-dev.exe').exists()
            assert hashlib.sha256(release_binary.read_bytes()).hexdigest()==release_hash
            stop_owned()
            checks.append('Repository source launcher works from an unrelated cwd with an isolated profile; development bin output does not overwrite the verified release')
        report={'result':'passed','version':'0.3.0','archive':ARCHIVE.name,'sha256':hashlib.sha256(ARCHIVE.read_bytes()).hexdigest(),'checks':checks,'generated_credentials':'isolated, not printed, removed with the temporary directory'}
        REPORT.write_text(json.dumps(report,ensure_ascii=False,indent=2),encoding='utf-8')
        print(json.dumps(report,ensure_ascii=False,indent=2),flush=True)
finally:
    stop_owned()
