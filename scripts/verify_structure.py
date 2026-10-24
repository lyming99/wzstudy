"""Check repository layout and launchers using disposable, synthetic profiles.
Never reads real credentials or starts the user's workspace. No third-party modules.
"""
from pathlib import Path
import json
import os
import re
import shutil
import socket
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[1]
ARTIFACTS = ROOT / '.artifacts'
ARTIFACTS.mkdir(exist_ok=True)
CHECKS = []
sys.stdout.reconfigure(encoding='utf-8')
sys.stderr.reconfigure(encoding='utf-8')


def check(name):
    CHECKS.append(name)
    print('PASS:', name, flush=True)


def free_port():
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        return sock.getsockname()[1]


required = [
    'go.mod', 'cmd/api/main.go', 'internal/adminui/embed.go',
    'start-local.ps1', 'scripts/start-local.ps1', 'scripts/admin-environment.ps1', 'internal/start-local.ps1',
    'scripts/start-dev.ps1', 'scripts/verify_go.py', 'scripts/verify_live.py',
    'scripts/verify_package.py', 'scripts/package_release.py',
    'docs/RUNNING.md', 'docs/PROJECT_LAYOUT.md',
    'docs/VERIFICATION.md', 'docs/VERIFICATION_MANAGEMENT.md', 'design/index.html',
]
for name in required:
    assert (ROOT / name).is_file(), name
for name in ['internal/design', 'internal/dist', 'internal/.artifacts',
             'internal/verify_live.py', 'internal/package_release.py']:
    assert not (ROOT / name).exists(), name
check('Go packages remain in place; design, documentation and tools are at root-level locations')

ignore = set((ROOT / '.dockerignore').read_text(encoding='utf-8').splitlines())
for rule in ['**/.local', '**/.artifacts', '**/data', '**/dist', '**/bin']:
    assert rule in ignore, rule
assert '/.local/' in (ROOT / '.gitignore').read_text(encoding='utf-8')
check('Docker/Git exclusion rules explicitly cover root and legacy private/generated directories')

docker = (ROOT / 'Dockerfile').read_text(encoding='utf-8')
compose = (ROOT / 'docker-compose.yml').read_text(encoding='utf-8')
for literal in ['ENV DATA_DIR=/var/lib/wzstudy', 'chown wzstudy:wzstudy /var/lib/wzstudy', 'USER wzstudy']:
    assert literal in docker, literal
assert 'DATA_DIR: /var/lib/wzstudy' in compose
assert '- api_data:/var/lib/wzstudy' in compose
assert re.search(r'^  api_data:\s*$', compose, re.MULTILINE)
check('Static Docker configuration supplies a writable application directory and a named attachment volume')

for filename in ['scripts/verify_live.py', 'scripts/verify_package.py', 'scripts/package_release.py']:
    source = (ROOT / filename).read_text(encoding='utf-8')
    assert 'parents[1]' in source, filename
check('Verification and packaging tools derive their repository root from their own location')

powershell = shutil.which('powershell.exe') if os.name == 'nt' else None
if powershell:
    paths = [ROOT / name for name in ['start-local.ps1', 'scripts/start-local.ps1',
             'scripts/start-dev.ps1', 'scripts/init-env.ps1', 'scripts/admin-environment.ps1', 'internal/start-local.ps1']]
    quoted = ','.join("'" + str(p).replace("'", "''") + "'" for p in paths)
    syntax = "$ErrorActionPreference='Stop';foreach($p in @(" + quoted + ")){$t=$null;$e=$null;[void][System.Management.Automation.Language.Parser]::ParseFile($p,[ref]$t,[ref]$e);if($e.Count){throw ('PowerShell syntax error: '+$p)}}"
    subprocess.run([powershell, '-NoProfile', '-NonInteractive', '-Command', syntax],
                   check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=30)
    check('All PowerShell entry points parse successfully')
    with tempfile.TemporaryDirectory(prefix='structure-', dir=ARTIFACTS) as temp:
        project = Path(temp) / 'project with spaces'
        caller = Path(temp) / 'unrelated caller'
        project.mkdir()
        caller.mkdir()
        for name in ['start-local.ps1', 'scripts/start-local.ps1', 'scripts/admin-environment.ps1', 'scripts/start-dev.ps1', 'internal/start-local.ps1']:
            destination = project / name
            destination.parent.mkdir(exist_ok=True)
            shutil.copyfile(ROOT / name, destination)

        def run(entry, *args, fail=False):
            result = subprocess.run([powershell, '-NoLogo', '-NoProfile', '-NonInteractive',
                        '-ExecutionPolicy', 'RemoteSigned', '-File', str(project / entry), *args],
                        cwd=caller, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                        encoding='utf-8', errors='replace', timeout=30)
            if fail:
                assert result.returncode != 0, entry
                return result
            assert result.returncode == 0, entry + ': ' + result.stderr[-1500:]
            return json.loads(result.stdout.lstrip('\ufeff'))

        initial = run('start-local.ps1', '-Check')
        assert Path(initial['project_root']) == project
        assert Path(initial['data_directory']) == project / '.local/data'
        assert not (project / '.local').exists()
        check('Root launcher works from an unrelated cwd and a path containing spaces; -Check is read-only')

        for entry in ['scripts/start-local.ps1', 'scripts/start-dev.ps1', 'internal/start-local.ps1']:
            assert run(entry, '-Check')['data_directory'] == initial['data_directory']
        check('Root, scripts, development-default and legacy launchers choose the same durable workspace')

        modern = run('start-local.ps1', '-Check', '-DataDirectory', 'custom data')
        legacy = run('internal/start-local.ps1', '-Check', '-DataDirectory', 'custom data')
        assert Path(modern['data_directory']) == project / 'custom data'
        assert Path(legacy['data_directory']) == project / 'internal/custom data'
        check('New relative data paths use project root; legacy relative paths preserve their old meaning')

        old_profile = project / 'internal/.local'
        old_profile.mkdir()
        (old_profile / 'access-keys.json').write_text('synthetic sentinel, not a key', encoding='utf-8')
        old = run('start-local.ps1', '-Check')
        assert old['legacy_profile'] and Path(old['profile_directory']) == old_profile
        check('Existing legacy workspace is selected without reading or rewriting its credential file')

        root_profile = project / '.local'
        root_profile.mkdir()
        (root_profile / 'access-keys.json').write_text('another synthetic sentinel', encoding='utf-8')
        result = run('start-local.ps1', '-Check', fail=True)
        assert 'Both .local' in result.stderr
        explicit = run('start-local.ps1', '-Check', '-ProfileDirectory', str(old_profile))
        assert Path(explicit['profile_directory']) == old_profile
        check('Ambiguous workspaces are refused; an explicit profile resolves the ambiguity')

        pg = run('scripts/start-dev.ps1', '-Postgres', '-Check')
        memory = run('scripts/start-dev.ps1', '-Memory', '-Check')
        assert pg['store'] == 'postgres' and memory['store'] == 'memory'
        run('scripts/start-dev.ps1', '-Memory', '-Postgres', '-Check', fail=True)
        check('PostgreSQL and memory are explicit, mutually exclusive modes; diagnostics do not load .env')

        orphan = project / 'orphaned-data'
        orphan.mkdir()
        (orphan / 'workspace.json').write_text('{}', encoding='utf-8')
        fresh_profile = project / 'new-profile'
        result = run('start-local.ps1', '-Address', f'127.0.0.1:{free_port()}',
                     '-ProfileDirectory', str(fresh_profile), '-DataDirectory', str(orphan), '-SkipBuild', fail=True)
        assert 'Existing data was found without' in result.stderr
        assert not (fresh_profile / 'access-keys.json').exists()
        check('Existing data without original keys is not assigned automatically regenerated credentials')

        with socket.socket() as listener:
            listener.bind(('127.0.0.1', 0))
            listener.listen()
            port = listener.getsockname()[1]
            result = run('start-local.ps1', '-Address', f'127.0.0.1:{port}',
                         '-ProfileDirectory', str(old_profile), '-SkipBuild', fail=True)
            assert 'already in use' in result.stderr
        check('Occupied ports are rejected before building, reading keys or starting another service')
        # This fixture .env is synthetic; never read the real project's .env.
        (project / '.env').write_text('SYSTEM_ADMIN_EMAIL=file@example.test\nSYSTEM_ADMIN_PASSWORD="  file-password  "\nSYSTEM_ADMIN_DISPLAY_NAME=File admin\n', encoding='utf-8')
        def resolve_admin(extra, no_file=False):
            env=dict(os.environ)
            for key in list(env):
                if key.startswith(('SYSTEM_ADMIN_', 'BOOTSTRAP_ADMIN_')):env.pop(key)
            env.update(extra)
            helper=str(project/'scripts/admin-environment.ps1').replace("'", "''")
            location=str(project).replace("'", "''")
            command=f". '{helper}'; Read-AdministratorEnvironment -ProjectRoot '{location}'" + (' -NoEnvFile' if no_file else '') + ' | ConvertTo-Json -Compress'
            result=subprocess.run([powershell,'-NoProfile','-NonInteractive','-Command',command],env=env,cwd=caller,capture_output=True,encoding='utf-8',errors='replace',timeout=30)
            assert result.returncode==0, 'Synthetic administrator environment resolution failed'
            return json.loads(result.stdout)
        merged=resolve_admin({'BOOTSTRAP_ADMIN_PASSWORD':'process-password'})
        assert merged['SYSTEM_ADMIN_EMAIL']=='file@example.test' and merged['SYSTEM_ADMIN_PASSWORD']=='process-password'
        merged=resolve_admin({'BOOTSTRAP_ADMIN_EMAIL':'process@example.test'})
        assert merged['SYSTEM_ADMIN_EMAIL']=='process@example.test' and merged['SYSTEM_ADMIN_PASSWORD']=='  file-password  '
        merged=resolve_admin({},True)
        assert not merged['SYSTEM_ADMIN_EMAIL'] and not merged['SYSTEM_ADMIN_PASSWORD']
        check('Process aliases override file aliases as logical fields; quoted password whitespace and -NoEnvFile are preserved')


report = {'result': 'passed', 'groups': len(CHECKS), 'checks': CHECKS,
          'powershell_runtime': 'passed' if powershell else 'not run: Windows PowerShell unavailable',
          'docker_runtime': 'not run; configuration checked statically',
          'credentials': 'only synthetic fixture markers used; real keys were not read'}
(ARTIFACTS / 'structure-verification.json').write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding='utf-8')
print(json.dumps(report, ensure_ascii=False, indent=2), flush=True)
