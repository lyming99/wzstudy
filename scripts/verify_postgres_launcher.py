"""Mocked Windows regression for the local PostgreSQL launcher.

Uses a temporary project, fake Docker and fake Go build commands. It never
starts a container, touches the real .env/.local, or connects to a database.
A real Docker/PostgreSQL integration check is still required separately.
"""
from pathlib import Path
from contextlib import ExitStack
from urllib.parse import quote, urlsplit, unquote
from verify_package import read_fixture_env
import uuid
import json
import hashlib
import os
import shutil
import socket
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[1]
PS = shutil.which('powershell.exe')
REAL_GO = shutil.which('go')
if hasattr(sys.stdout, 'reconfigure'):
    sys.stdout.reconfigure(encoding='utf-8')

STUB_GO = r'''package main
import (
 "crypto/sha256"
 "encoding/json"
 "fmt"
 "net/url"
 "os"
 "path/filepath"
 "strings"
)
type State struct { Volumes map[string]string `json:"volumes"`; Admin bool `json:"admin"`; Calls []string `json:"calls"`; PasswordHash string `json:"password_hash"`; DatabaseUser string `json:"database_user"`; DatabaseName string `json:"database_name"`; DataDirectory string `json:"data_directory"`; AdminEmail string `json:"admin_email"`; AdminPasswordHash string `json:"admin_password_hash"`; AdminDisplayName string `json:"admin_display_name"` }
func main() {
 path:=os.Getenv("WZ_FAKE_STATE"); raw,err:=os.ReadFile(path); if err!=nil {os.Exit(2)}
 var state State; if json.Unmarshal(raw,&state)!=nil {os.Exit(3)}
 u,err:=url.Parse(os.Getenv("DATABASE_URL")); if err!=nil || u.Hostname()!="127.0.0.1" || u.User==nil || u.User.Username()!=state.DatabaseUser || u.Path!="/"+state.DatabaseName {fmt.Fprintln(os.Stderr,"bad local URL");os.Exit(4)}
 pgpass,ok:=u.User.Password(); passhash:=sha256.Sum256([]byte(pgpass)); if !ok || fmt.Sprintf("%x",passhash)!=state.PasswordHash {fmt.Fprintln(os.Stderr,"service password differs from Docker");os.Exit(11)}
 name:=strings.ToLower(filepath.Base(os.Args[0]))
 if strings.Contains(name,"migrate") { if os.Getenv("SYSTEM_ADMIN_PASSWORD")!="" || os.Getenv("BOOTSTRAP_ADMIN_PASSWORD")!="" {fmt.Fprintln(os.Stderr,"migration inherited bootstrap secret");os.Exit(10)}; state.Calls=append(state.Calls,"migrate"); data,_:=json.Marshal(state); os.WriteFile(path,data,0600); if os.Getenv("WZ_FAKE_FAIL_MIGRATION")=="1" {os.Exit(5)}; return }
 if os.Getenv("STORE")!="postgres" || !strings.HasPrefix(os.Getenv("HTTP_ADDR"),"127.0.0.1:") || len(os.Getenv("ADMIN_API_KEY"))<32 || len(os.Getenv("READ_API_KEY"))<32 {fmt.Fprintln(os.Stderr,"bad service environment");os.Exit(6)}
 if state.Admin && os.Getenv("SYSTEM_ADMIN_PASSWORD")!="" {fmt.Fprintln(os.Stderr,"existing admin password was passed");os.Exit(7)}
 if state.Admin && os.Getenv("SYSTEM_ADMIN_EMAIL")!="" && !strings.EqualFold(strings.TrimSpace(os.Getenv("SYSTEM_ADMIN_EMAIL")),state.AdminEmail) {fmt.Fprintln(os.Stderr,"configured system administrator email does not match the stored administrator");os.Exit(12)}
 if !state.Admin && (os.Getenv("SYSTEM_ADMIN_EMAIL")=="" || len(os.Getenv("SYSTEM_ADMIN_PASSWORD"))<8) {fmt.Fprintln(os.Stderr,"missing initial admin");os.Exit(8)}
 if os.Getenv("WZ_FAKE_FAIL_API")=="1" {state.Calls=append(state.Calls,"api-failed");data,_:=json.Marshal(state);os.WriteFile(path,data,0600);os.Exit(9)}
 if !state.Admin { password:=sha256.Sum256([]byte(os.Getenv("SYSTEM_ADMIN_PASSWORD"))); state.AdminEmail=os.Getenv("SYSTEM_ADMIN_EMAIL"); state.AdminPasswordHash=fmt.Sprintf("%x",password); state.AdminDisplayName=os.Getenv("SYSTEM_ADMIN_DISPLAY_NAME") }
 state.Admin=true; state.DataDirectory=os.Getenv("DATA_DIR"); state.Calls=append(state.Calls,"api"); data,_:=json.Marshal(state); os.WriteFile(path,data,0600)
 fmt.Println("Fixture API completed")
}
'''

FAKE_DOCKER = r'''import hashlib,json,os,sys
if any(os.environ.get(k) for k in ('SYSTEM_ADMIN_PASSWORD','BOOTSTRAP_ADMIN_PASSWORD')):
 print('docker inherited bootstrap secret', file=sys.stderr); sys.exit(71)
path=os.environ['WZ_FAKE_STATE']
with open(path,encoding='utf-8') as f: s=json.load(f)
a=sys.argv[1:]
def save():
 with open(path,'w',encoding='utf-8') as f: json.dump(s,f)
if a[:2]==['context','show']: print('fixture-local'); sys.exit(0)
if a[:2]==['context','inspect']:
 endpoint='ssh://remote.example' if os.environ.get('WZ_FAKE_REMOTE_CONTEXT')=='1' else 'npipe:////./pipe/docker_engine'
 print(json.dumps([{'Endpoints':{'docker':{'Host':endpoint}}}])); sys.exit(0)
if a and a[0]=='version':
 if os.environ.get('WZ_FAKE_NO_DAEMON')=='1': sys.exit(1)
 print('27.0.0'); sys.exit(0)
if a[:2]==['compose','version']: print('2.0.0'); sys.exit(0)
if a[:2]==['volume','ls']:
 wanted=next((x[5:] for x in a if x.startswith('name=')),'')
 for name in s['volumes']:
  if wanted in name: print(name)
 sys.exit(0)
if a[:2]==['volume','create']:
 name=a[-1]; label=next(x.split('=',1)[1] for x in a if x.startswith('org.wzstudy.local-instance='))
 s['volumes'].setdefault(name,label); s['calls'].append('volume-create'); save(); print(name); sys.exit(0)
if a[:2]==['volume','inspect']:
 name=a[-1]
 if name not in s['volumes']: sys.exit(1)
 print(json.dumps([{'Name':name,'Labels':{'org.wzstudy.local-instance':s['volumes'][name]}}]));sys.exit(0)
if a and a[0]=='ps':
 if s.get('own_container'): print('fixture-container')
 sys.exit(0)
if a and a[0]=='port':
 if s.get('own_container'):
  print('127.0.0.1:'+os.environ['WZ_FAKE_BOUND_PORT']);sys.exit(0)
 sys.exit(1)
if a and a[0]=='compose':
 if 'up' in a:
  password=os.environ.get('WZSTUDY_LOCAL_PG_PASSWORD',''); h=hashlib.sha256(password.encode()).hexdigest()
  user=os.environ.get('WZSTUDY_LOCAL_PG_USER',''); database=os.environ.get('WZSTUDY_LOCAL_PG_DATABASE','')
  if not user or not database: sys.exit(1)
  if s.get('password_hash') and (s['password_hash']!=h or s.get('database_user')!=user or s.get('database_name')!=database): sys.exit(1)
  s['password_hash']=h; s['database_user']=user; s['database_name']=database; s['calls'].append('compose-up'); save()
  if os.environ.get('WZ_FAKE_FAIL_UP')=='1': sys.exit(1)
  sys.exit(0)
 if 'exec' in a and 'pg_isready' in a: sys.exit(0)
 if 'exec' in a and 'psql' in ' '.join(a):
  sql=sys.stdin.read()
  if 'system_administrator' not in sql or 'singleton' not in sql: sys.exit(1)
  print('t' if s.get('admin') else 'f');sys.exit(0)
sys.exit(2)
'''

FAKE_GO = r'''import os,shutil,sys
if any(os.environ.get(k) for k in ('SYSTEM_ADMIN_PASSWORD','BOOTSTRAP_ADMIN_PASSWORD')): sys.exit(71)
args=sys.argv[1:]
if not args or args[0]!='build' or '-o' not in args: sys.exit(2)
if os.environ.get('WZ_FAKE_FAIL_BUILD')=='1': sys.exit(7)
out=args[args.index('-o')+1]
shutil.copy2(os.environ['WZ_FAKE_STUB'],out)
sys.exit(0)
'''


def free_port(excluded=0):
    while True:
        with socket.socket() as sock:
            sock.bind(('127.0.0.1', 0))
            port = sock.getsockname()[1]
        if port != excluded:
            return port


def save_state(path, state):
    path.write_text(json.dumps(state), encoding='utf-8')


def run_launcher(project, env, *args, success=True, entry='start-local.ps1'):
    command = [PS, '-NoLogo', '-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass', '-File', str(project / entry), *map(str, args)]
    result = subprocess.run(command, cwd=project, env=env, text=True, encoding='utf-8', errors='replace', capture_output=True, timeout=90)
    if (result.returncode == 0) != success:
        raise AssertionError(f'launcher status {result.returncode}, expected success={success}\n{result.stdout[-4000:]}\n{result.stderr[-4000:]}')
    return result.stdout + result.stderr


def copy_launcher(project):
    (project / 'scripts').mkdir(parents=True, exist_ok=True)
    for name in ['start-local.ps1', 'start-postgres-local.ps1', 'scripts/docker-compose-local-postgres.yml', 'scripts/local-service-lifecycle.ps1', 'scripts/admin-environment.ps1', 'scripts/local-env.ps1', 'scripts/local-postgres-state.ps1']:
        shutil.copy2(ROOT / name, project / name)


def main():
    assert os.name == 'nt' and PS and REAL_GO, 'Windows PowerShell and Go are required'
    tmp_parent = ROOT / '.tmp'
    tmp_parent.mkdir(exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='postgres-launcher-test-', dir=tmp_parent) as raw:
        tmp = Path(raw)
        fakebin = tmp / 'fake bin'
        fakebin.mkdir()
        stub_src = tmp / 'stub.go'
        stub_src.write_text(STUB_GO, encoding='utf-8')
        stub_exe = tmp / 'stub.exe'
        subprocess.run([REAL_GO, 'build', '-o', str(stub_exe), str(stub_src)], cwd=tmp, check=True, capture_output=True)
        for name, source in [('fake_docker.py', FAKE_DOCKER), ('fake_go.py', FAKE_GO)]:
            (fakebin / name).write_text(source, encoding='utf-8')
        for name, script in [('docker.cmd', 'fake_docker.py'), ('go.cmd', 'fake_go.py')]:
            (fakebin / name).write_text(f'@echo off\r\n"{sys.executable}" "{fakebin / script}" %*\r\nexit /b %ERRORLEVEL%\r\n', encoding='utf-8')
        base_env = {key: value for key, value in os.environ.items()
                    if not key.startswith(('SYSTEM_ADMIN_', 'BOOTSTRAP_ADMIN_', 'WZ_FAKE_', 'WZSTUDY_LOCAL_'))
                    and key not in ('DOCKER_HOST', 'DOCKER_CONTEXT', 'DATA_DIR', 'DATABASE_URL', 'POSTGRES_PASSWORD', 'ADMIN_API_KEY', 'READ_API_KEY')}
        base_env.update(PATH=str(fakebin)+os.pathsep+base_env['PATH'], WZ_FAKE_STUB=str(stub_exe), PYTHONUTF8='1')

        def fixture(name, contents=None):
            project = tmp / name
            copy_launcher(project)
            (project / 'go.mod').write_text('module fixture\ngo 1.25\n', encoding='utf-8')
            if contents is not None:
                (project / '.env').write_text(contents, encoding='utf-8', newline='')
            state_path = tmp / (name + '.json')
            save_state(state_path, {'volumes':{}, 'admin':False, 'calls':[], 'password_hash':''})
            return project, state_path, dict(base_env, WZ_FAKE_STATE=str(state_path))

        def creds(project):
            return read_fixture_env(project / '.env')

        def state(path):
            return json.loads(path.read_text(encoding='utf-8'))

        def protect(path):
            helper = str(ROOT / 'scripts/local-env.ps1').replace("'", "''")
            path = str(path).replace("'", "''")
            command = f"$ErrorActionPreference='Stop'; . '{helper}'; $s=Read-LocalEnvSnapshot '{path}'; Protect-LocalEnv $s"
            subprocess.run([PS, '-NoProfile', '-NonInteractive', '-Command', command], check=True, capture_output=True, timeout=30)

        def assert_private(path):
            quoted = str(path).replace("'", "''")
            command = (f"$a=Get-Acl -LiteralPath '{quoted}'; $s=[Security.Principal.WindowsIdentity]::GetCurrent().User.Value; "
                       "if (-not $a.AreAccessRulesProtected -or $a.GetOwner([Security.Principal.SecurityIdentifier]).Value -ne $s) {exit 1}; "
                       "foreach($r in $a.GetAccessRules($true,$true,[Security.Principal.SecurityIdentifier])) "
                       "{if($r.AccessControlType -eq 'Allow' -and $r.IdentityReference.Value -ne $s){exit 2}}")
            subprocess.run([PS, '-NoProfile', '-NonInteractive', '-Command', command], check=True, capture_output=True, timeout=30)

        def deny_acl_right(path, right, include_owner_rights=False):
            # Synthetic NTFS file: the current user remains owner and can read;
            # explicitly deny the targeted right to exercise a non-admin token.
            quoted = str(path).replace("'", "''")
            subjects = "@([Security.Principal.WindowsIdentity]::GetCurrent().User)"
            if include_owner_rights:
                subjects = "@([Security.Principal.WindowsIdentity]::GetCurrent().User,[Security.Principal.SecurityIdentifier]::new('S-1-3-4'))"
            command = (f"$ErrorActionPreference='Stop'; $p='{quoted}'; "
                       "$a=[IO.File]::GetAccessControl($p,[Security.AccessControl.AccessControlSections]::Access); "
                       f"foreach($s in {subjects}) {{ "
                       f"$null=$a.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($s,[Security.AccessControl.FileSystemRights]::{right},[Security.AccessControl.AccessControlType]::Deny)) }}; "
                       "[IO.File]::SetAccessControl($p,$a)")
            subprocess.run([PS, '-NoProfile', '-NonInteractive', '-Command', command], check=True, capture_output=True, timeout=30)

        project, state_path, env = fixture('fresh-project', '# preserve comment\r\nDATA_DIR="media/附件"\r\nPOSTGRES_PASSWORD=\r\nOTHER_SETTING=keep\r\n')
        api, db = free_port(), free_port()
        while api == db: db = free_port()
        checked = json.loads(run_launcher(project, env, '-Check', '-ApiPort', api, '-DatabasePort', db))
        assert checked['store'] == 'postgres' and Path(checked['data_directory']) == project / 'media/附件'
        assert not (project / '.local').exists()
        # Owner can edit the DACL but is explicitly denied WRITE_OWNER, matching
        # the reported non-admin failure mode rather than an elevated fixture.
        deny_acl_right(project / '.env', 'TakeOwnership')
        output = run_launcher(project, env, '-ApiPort', api, '-DatabasePort', db)
        values = creds(project)
        assert all(values.get(k) for k in ('DATABASE_URL','POSTGRES_PASSWORD','ADMIN_API_KEY','READ_API_KEY','WZSTUDY_LOCAL_INSTANCE_ID','SYSTEM_ADMIN_EMAIL','SYSTEM_ADMIN_PASSWORD'))
        assert values['WZSTUDY_LOCAL_VOLUME_STATE'] == 'created'
        assert len(values['POSTGRES_PASSWORD']) >= 32 and len(values['SYSTEM_ADMIN_PASSWORD']) >= 32
        url = urlsplit(values['DATABASE_URL'])
        assert url.hostname == '127.0.0.1' and url.port == db and unquote(url.password) == values['POSTGRES_PASSWORD']
        assert values['SYSTEM_ADMIN_EMAIL'] == 'admin@wzstudy.local'
        assert state(state_path)['admin_password_hash'] == hashlib.sha256(values['SYSTEM_ADMIN_PASSWORD'].encode()).hexdigest()
        assert all(values[k] not in output for k in ('POSTGRES_PASSWORD','DATABASE_URL','ADMIN_API_KEY','READ_API_KEY','SYSTEM_ADMIN_PASSWORD'))
        env_bytes = (project / '.env').read_bytes()
        assert b'\r\n' in env_bytes and b'\r\r\n' not in env_bytes
        assert '# preserve comment' in (project / '.env').read_text(encoding='utf-8') and values['OTHER_SETTING'] == 'keep'
        profile = project / '.local/postgres-local'
        assert all(not (profile / name).exists() for name in ('connection.json','admin-initial.json','volume-created.txt','volume-pending.txt','startup.lock'))
        assert_private(project / '.env')
        before = (project / '.env').read_bytes()
        output = run_launcher(project, env, '-ApiPort', api)
        assert (project / '.env').read_bytes() == before and 'no password was reset' in output
        assert state(state_path)['calls'].count('volume-create') == 1
        print('PASS: 拒绝 WRITE_OWNER 的普通权限下仍可初始化、保护 .env 并重启复用')

        denied, denied_state, denied_env = fixture('acl-denied-project', '# synthetic configuration\n')
        denied_file = denied / '.env'
        denied_before = denied_file.read_bytes()
        deny_acl_right(denied_file, 'ChangePermissions', include_owner_rights=True)
        output = run_launcher(denied, denied_env, '-ApiPort', free_port(), success=False)
        assert 'Cannot secure .env with the current Windows account' in output
        assert denied_file.read_bytes() == denied_before and state(denied_state)['calls'] == []
        assert not list(denied.glob('.env-*.tmp'))
        print('PASS: 无权修改 DACL 时明确报错，不写入密码、不创建数据库')

        new_file, _, new_file_env = fixture('missing-env-file')
        nofile_api = free_port()
        run_launcher(new_file, new_file_env, '-ApiPort', nofile_api, '-DatabasePort', free_port(nofile_api))
        assert creds(new_file)['WZSTUDY_LOCAL_VOLUME_STATE'] == 'created'
        assert_private(new_file / '.env')
        assert not (new_file / '.local/postgres-local/connection.json').exists()
        print('PASS: 不存在 .env 时创建唯一配置文件并保护其 ACL')

        process_env = dict(env, **{k: values[k] for k in ('DATABASE_URL','POSTGRES_PASSWORD','ADMIN_API_KEY','READ_API_KEY','WZSTUDY_LOCAL_INSTANCE_ID','WZSTUDY_LOCAL_VOLUME_STATE')})
        run_launcher(project, process_env, '-NoEnvFile', '-ApiPort', api)
        assert (project / '.env').read_bytes() == before
        empty, empty_state, empty_env = fixture('no-env-project')
        output = run_launcher(empty, empty_env, '-NoEnvFile', '-ApiPort', free_port(), success=False)
        assert 'use .env for first initialization' in output and not (empty / '.env').exists() and state(empty_state)['calls'] == []
        print('PASS: -NoEnvFile 仅支持完整进程配置和已登记数据卷，不生成不可恢复的凭据')

        lines = (project / '.env').read_text(encoding='utf-8').splitlines()
        changed = ["SYSTEM_ADMIN_EMAIL=changed@example.test" if line.startswith('SYSTEM_ADMIN_EMAIL=') else line for line in lines]
        (project / '.env').write_text('\n'.join(changed) + '\n', encoding='utf-8')
        changed_bytes = (project / '.env').read_bytes()
        output = run_launcher(project, env, '-ApiPort', api, success=False)
        assert 'configured system administrator email does not match' in output
        assert (project / '.env').read_bytes() == changed_bytes
        assert state(state_path)['admin_email'] == values['SYSTEM_ADMIN_EMAIL'] and state(state_path)['admin']
        (project / '.env').write_bytes(before)
        print('PASS: 已有管理员与配置邮箱不一致时停止，不重置账号或覆盖配置')

        original_state = state(state_path)
        volume = next(iter(original_state['volumes']))
        wrong = json.loads(json.dumps(original_state)); wrong['volumes'][volume] = '0'*32
        save_state(state_path, wrong)
        output = run_launcher(project, env, '-ApiPort', api, success=False)
        assert 'does not belong' in output and (project / '.env').read_bytes() == before
        missing = json.loads(json.dumps(original_state)); del missing['volumes'][volume]
        save_state(state_path, missing)
        output = run_launcher(project, env, '-ApiPort', api, success=False)
        assert 'data volume is missing' in output and (project / '.env').read_bytes() == before
        save_state(state_path, original_state)
        backup = project / 'fixture-env-backup'
        (project / '.env').rename(backup)
        try:
            output = run_launcher(project, env, '-ApiPort', api, success=False)
            assert 'without recoverable configuration' in output and not (project / '.env').exists()
        finally:
            backup.rename(project / '.env')
        assert (project / '.env').read_bytes() == before
        print('PASS: 标签不匹配、数据卷丢失或 .env 丢失时拒绝重建或接管')

        lines = (project / '.env').read_text(encoding='utf-8').splitlines()
        (project / '.env').write_text('\n'.join(x for x in lines if not x.startswith('DATABASE_URL='))+'\n', encoding='utf-8')
        incomplete_before = (project / '.env').read_bytes()
        output = run_launcher(project, env, '-ApiPort', api, success=False)
        assert 'credentials are incomplete' in output and (project / '.env').read_bytes() == incomplete_before
        (project / '.env').write_bytes(before)
        print('PASS: 已初始化实例的连接配置不完整时不生成替代密码')

        dbpass = '  Db=@:/%#密码$  '
        custom_port = free_port()
        dburl = f'postgresql://custom_user:{quote(dbpass, safe="")}@localhost:{custom_port}/custom_data?sslmode=disable'
        custom, custom_state, custom_env = fixture('custom-project', f'DATABASE_URL={dburl}\nPOSTGRES_PASSWORD="{dbpass}"\n')
        custom_env.update(POSTGRES_PASSWORD='ignore-process-password', DATABASE_URL='postgres://ignored@remote.example/db')
        output = run_launcher(custom, custom_env, '-ApiPort', free_port(custom_port))
        configured = creds(custom)
        assert configured['DATABASE_URL'] == dburl and configured['POSTGRES_PASSWORD'] == dbpass
        assert state(custom_state)['database_user'] == 'custom_user' and state(custom_state)['database_name'] == 'custom_data'
        assert dbpass not in output and dburl not in output
        print('PASS: .env 自定义连接和密码用于 Docker/API，特殊字符保留；进程数据库配置不覆盖 .env')

        only_port = free_port()
        partial, _, partial_env = fixture('partial-url', f'DATABASE_URL=postgres://other_user@127.0.0.1:{only_port}/other_db\n')
        run_launcher(partial, partial_env, '-ApiPort', free_port(only_port))
        partial_values = creds(partial)
        assert len(partial_values['POSTGRES_PASSWORD']) >= 32
        assert unquote(urlsplit(partial_values['DATABASE_URL']).password) == partial_values['POSTGRES_PASSWORD']
        print('PASS: 连接串没有密码时生成密码并回填 .env 中一致的连接串')

        invalid, invalid_state, invalid_env = fixture('invalid-project', '')
        bad_api, bad_db = free_port(), free_port()
        if bad_api == bad_db: bad_db = free_port(bad_api)
        cases = [
            (f'DATABASE_URL=postgres://wzstudy:password-one@localhost:{bad_db}/wzstudy\nPOSTGRES_PASSWORD=password-two\n', [], 'different passwords'),
            (f'DATABASE_URL=postgres://wzstudy:password-one@remote.example:{bad_db}/wzstudy\n', [], 'local PostgreSQL URL'),
            (f'DATABASE_URL=postgres://wzstudy:%FF@localhost:{bad_db}/wzstudy\n', [], 'valid UTF-8 and percent-encoding'),
            ('POSTGRES_PASSWORD=\nPOSTGRES_PASSWORD=password-one\n', [], 'appears more than once'),
            (f'ADMIN_API_KEY=" {"A"*32}"\nREAD_API_KEY={"R"*40}\n', [], 'ADMIN_API_KEY and READ_API_KEY'),
            (f'ADMIN_API_KEY={"密"*90}\nREAD_API_KEY={"R"*40}\n', [], 'ADMIN_API_KEY and READ_API_KEY'),
            (f'DATABASE_URL=postgres://wzstudy:password-one@localhost:{bad_db}/wzstudy\n', ['-DatabasePort', free_port(bad_db)], 'different ports'),
        ]
        for content, extra, expected in cases:
            (invalid / '.env').write_text(content, encoding='utf-8')
            original = (invalid / '.env').read_bytes()
            output = run_launcher(invalid, invalid_env, '-ApiPort', bad_api, *extra, success=False)
            assert expected in output and 'password-one' not in output and 'password-two' not in output
            assert (invalid / '.env').read_bytes() == original and state(invalid_state)['calls'] == []
        print('PASS: 重复字段、密码不一致、非法 URL、远程地址和端口冲突被安全拒绝')

        race, _, _ = fixture('env-race', '# original\n')
        helper = str(ROOT / 'scripts/local-env.ps1').replace("'", "''")
        path = str(race / '.env').replace("'", "''")
        race_command = (f"$ErrorActionPreference='Stop'; . '{helper}'; $s=Read-LocalEnvSnapshot '{path}'; "
                        f"[IO.File]::AppendAllText('{path}', '# external edit'); "
                        "try { $null=Save-LocalEnvSnapshot $s @{POSTGRES_PASSWORD='fixture-secret'}; exit 3 } "
                        "catch { if ($_.Exception.Message -notmatch 'changed during initialization') { exit 4 }; exit 0 }")
        subprocess.run([PS, '-NoProfile', '-NonInteractive', '-Command', race_command], check=True, capture_output=True, timeout=30)
        race_text = (race / '.env').read_text(encoding='utf-8')
        assert '# external edit' in race_text and 'fixture-secret' not in race_text and not list(race.glob('.env-*.tmp'))
        print('PASS: .env 并发修改时不覆盖用户编辑且不遗留明文临时文件')

        recovery, recovery_state, recovery_env = fixture('recovery-project', '# recovery\n')
        recovery_api, recovery_db = free_port(), free_port()
        while recovery_api == recovery_db: recovery_db = free_port()
        run_launcher(recovery, dict(recovery_env, WZ_FAKE_FAIL_BUILD='1'), '-ApiPort', recovery_api, '-DatabasePort', recovery_db, success=False)
        pending = creds(recovery); assert pending['WZSTUDY_LOCAL_VOLUME_STATE'] == 'pending' and state(recovery_state)['volumes'] == {}
        run_launcher(recovery, dict(recovery_env, WZ_FAKE_FAIL_UP='1'), '-ApiPort', recovery_api, success=False)
        created = creds(recovery); assert created['WZSTUDY_LOCAL_VOLUME_STATE'] == 'created'
        assert created['POSTGRES_PASSWORD'] == pending['POSTGRES_PASSWORD'] and created['WZSTUDY_LOCAL_INSTANCE_ID'] == pending['WZSTUDY_LOCAL_INSTANCE_ID']
        run_launcher(recovery, dict(recovery_env, WZ_FAKE_FAIL_API='1'), '-ApiPort', recovery_api, success=False)
        retry = creds(recovery); assert retry['SYSTEM_ADMIN_PASSWORD'] and not state(recovery_state)['admin']
        saved = (recovery / '.env').read_bytes()
        run_launcher(recovery, recovery_env, '-ApiPort', recovery_api)
        assert (recovery / '.env').read_bytes() == saved and state(recovery_state)['admin']
        print('PASS: 构建、容器及服务失败重试复用 .env 中的同一组凭据与数据卷状态')

        def legacy_fixture(name, conflict=False):
            legacy, legacy_state, legacy_env = fixture(name, '# existing config\n' + ('POSTGRES_PASSWORD=not-the-old-password\n' if conflict else ''))
            oldprofile = legacy / '.local/postgres-local'; oldprofile.mkdir(parents=True)
            project_id = 'wzstudy-local-' + hashlib.sha256(str(legacy.resolve()).lower().encode()).hexdigest()[:12]
            volume_name = project_id + '-pgdata'; instance = uuid.uuid4().hex; port = free_port()
            pw = 'legacy-database-password-abcdef0123456789'
            admin_key = 'A'*40; read_key = 'R'*40
            old = {'version':1,'project':project_id,'volume':volume_name,'instance_id':instance,
                   'database_host':'127.0.0.1','database_port':port,'database_name':'wzstudy','database_user':'wzstudy',
                   'database_password':pw,'database_url':f'postgres://wzstudy:{pw}@127.0.0.1:{port}/wzstudy?sslmode=disable',
                   'admin_api_key':admin_key,'read_api_key':read_key}
            connection = oldprofile / 'connection.json'; connection.write_text(json.dumps(old),encoding='utf-8'); protect(connection)
            (oldprofile / 'volume-created.txt').write_text(instance,encoding='utf-8')
            admin_file = oldprofile / 'admin-initial.json'; admin_file.write_text(json.dumps({'email':'legacy@example.test','password':'legacy-admin-password'}),encoding='utf-8'); protect(admin_file)
            save_state(legacy_state, {'volumes':{volume_name:instance},'admin':False,'calls':[],
                                     'password_hash':hashlib.sha256(pw.encode()).hexdigest(),'database_user':'wzstudy','database_name':'wzstudy'})
            return legacy, legacy_state, legacy_env, old, oldprofile, admin_file

        legacy, legacy_state, legacy_env, old, oldprofile, oldadmin = legacy_fixture('legacy-project')
        old_connection = (oldprofile / 'connection.json').read_bytes(); old_admin_bytes = oldadmin.read_bytes()
        output = run_launcher(legacy, legacy_env, '-ApiPort', free_port(old['database_port']))
        migrated = creds(legacy)
        assert migrated['POSTGRES_PASSWORD'] == old['database_password'] and migrated['ADMIN_API_KEY'] == old['admin_api_key']
        assert migrated['READ_API_KEY'] == old['read_api_key'] and migrated['WZSTUDY_LOCAL_INSTANCE_ID'] == old['instance_id']
        assert migrated['SYSTEM_ADMIN_EMAIL'] == 'legacy@example.test' and migrated['SYSTEM_ADMIN_PASSWORD'] == 'legacy-admin-password'
        assert old['database_password'] not in output and migrated['SYSTEM_ADMIN_PASSWORD'] not in output
        assert (oldprofile / 'connection.json').read_bytes() == old_connection and oldadmin.read_bytes() == old_admin_bytes
        (oldprofile / 'connection.json').unlink(); oldadmin.unlink(); (oldprofile / 'volume-created.txt').unlink()
        saved = (legacy / '.env').read_bytes()
        run_launcher(legacy, legacy_env, '-ApiPort', free_port(old['database_port']))
        assert (legacy / '.env').read_bytes() == saved and not (oldprofile / 'connection.json').exists()
        print('PASS: 旧数据库及初始管理员迁入 .env；旧文件保留，移除测试副本后仍可重启')

        conflict, conflict_state, conflict_env, old, oldprofile, _ = legacy_fixture('legacy-conflict', True)
        original = (conflict / '.env').read_bytes(); original_legacy = (oldprofile / 'connection.json').read_bytes()
        output = run_launcher(conflict, conflict_env, '-ApiPort', free_port(old['database_port']), success=False)
        assert 'conflicts with the legacy configuration' in output
        assert (conflict / '.env').read_bytes() == original and (oldprofile / 'connection.json').read_bytes() == original_legacy
        assert state(conflict_state)['calls'] == []
        print('PASS: 旧配置与 .env 冲突时不迁移、不修改原数据或凭据')

        pending_old, pending_state, pending_env, old, oldprofile, _ = legacy_fixture('legacy-pending')
        (oldprofile / 'volume-created.txt').rename(oldprofile / 'volume-pending.txt')
        run_launcher(pending_old, pending_env, '-ApiPort', free_port(old['database_port']))
        pending_migrated = creds(pending_old)
        assert pending_migrated['WZSTUDY_LOCAL_INSTANCE_ID'] == old['instance_id']
        assert pending_migrated['WZSTUDY_LOCAL_VOLUME_STATE'] == 'created'
        assert (oldprofile / 'volume-pending.txt').exists() and state(pending_state)['admin']
        bad_old, bad_state, bad_env, old, oldprofile, _ = legacy_fixture('legacy-bad-marker')
        (oldprofile / 'volume-created.txt').write_text('0'*32, encoding='utf-8')
        original = (bad_old / '.env').read_bytes()
        output = run_launcher(bad_old, bad_env, '-ApiPort', free_port(old['database_port']), success=False)
        assert 'legacy volume marker does not match' in output and (bad_old / '.env').read_bytes() == original
        assert state(bad_state)['calls'] == []
        print('PASS: 旧版 pending 状态安全恢复；旧标记损坏时停止而不猜测')
        print('PASS: 以上为隔离模拟测试；未启动真实 Docker/PostgreSQL')


if __name__ == '__main__':
    main()
