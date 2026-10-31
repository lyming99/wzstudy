"""Check repository layout and launchers using disposable, synthetic profiles.
Never reads real credentials or starts the user's workspace. No third-party modules.
"""
from pathlib import Path
import ast
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
    'start-local.ps1', 'start-local.cmd', 'start-postgres-local.ps1', 'start-postgres-local.cmd',
    'scripts/docker-compose-local-postgres.yml', 'scripts/start-local.ps1', 'scripts/admin-environment.ps1',
    'scripts/local-env.ps1', 'scripts/local-postgres-state.ps1', 'scripts/local-service-lifecycle.ps1', 'internal/start-local.ps1',
    'scripts/start-dev.ps1', 'scripts/restart-local.ps1', 'scripts/restart-local.cmd',
    'scripts/verify_go.py', 'scripts/verify_live.py',
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
package_tree = ast.parse((ROOT / 'scripts/package_release.py').read_text(encoding='utf-8'))
package_files = next(node.value for node in package_tree.body
                     if isinstance(node, ast.Assign)
                     and any(isinstance(target, ast.Name) and target.id == 'files'
                             for target in node.targets))
package_keys = {key.value for key in package_files.keys if isinstance(key, ast.Constant)}
launcher_files = {'start-local.ps1', 'start-local.cmd', 'start-postgres-local.ps1', 'start-postgres-local.cmd',
                  'scripts/docker-compose-local-postgres.yml', 'scripts/start-local.ps1', 'scripts/admin-environment.ps1',
                  'scripts/local-env.ps1', 'scripts/local-postgres-state.ps1', 'scripts/local-service-lifecycle.ps1', 'scripts/restart-local.ps1', 'scripts/restart-local.cmd'}
assert launcher_files <= package_keys, launcher_files - package_keys
for key, value in zip(package_files.keys, package_files.values):
    if isinstance(key, ast.Constant) and key.value in launcher_files:
        assert (isinstance(value, ast.BinOp) and isinstance(value.op, ast.Div)
                and isinstance(value.left, ast.Name) and value.left.id == 'ROOT'
                and isinstance(value.right, ast.Constant) and value.right.value == key.value), key.value
check('Portable archive whitelist includes every local launcher runtime dependency and restart entry point')

powershell = shutil.which('powershell.exe') if os.name == 'nt' else None
if powershell:
    paths = [ROOT / name for name in ['start-local.ps1', 'start-postgres-local.ps1', 'scripts/start-local.ps1',
             'scripts/start-dev.ps1', 'scripts/restart-local.ps1', 'scripts/init-env.ps1',
             'scripts/admin-environment.ps1', 'scripts/local-env.ps1', 'scripts/local-postgres-state.ps1', 'scripts/local-service-lifecycle.ps1', 'internal/start-local.ps1']]
    quoted = ','.join("'" + str(p).replace("'", "''") + "'" for p in paths)
    syntax = "$ErrorActionPreference='Stop';foreach($p in @(" + quoted + ")){$t=$null;$e=$null;[void][System.Management.Automation.Language.Parser]::ParseFile($p,[ref]$t,[ref]$e);if($e.Count){throw ('PowerShell syntax error: '+$p)}}"
    subprocess.run([powershell, '-NoProfile', '-NonInteractive', '-Command', syntax],
                   check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=30)
    check('All PowerShell entry points parse successfully')
    lifecycle_path = str(ROOT / 'scripts/local-service-lifecycle.ps1').replace("'", "''")
    lifecycle_checks = r'''
$ErrorActionPreference = 'Stop'
. '__HELPER__'
Assert-LocalServiceExit -ExitCode 0 -ServiceAddress '127.0.0.1:1'
Assert-LocalServiceExit -ExitCode -1 -ServiceAddress '127.0.0.1:1' -RestartRequested $true
foreach ($case in @(
    @{ Code = -1; Restart = $false; Expected = '0xFFFFFFFF' },
    @{ Code = 1; Restart = $true; Expected = '0x00000001' },
    @{ Code = -1073741510; Restart = $false; Expected = 'console interruption' }
)) {
    try {
        Assert-LocalServiceExit -ExitCode $case.Code -ServiceAddress '127.0.0.1:1' -RestartRequested $case.Restart
        throw 'Expected an error for this exit code'
    } catch {
        if ($_.Exception.Message -notlike ('*' + $case.Expected + '*')) { throw }
    }
}
$signal = New-LocalRestartSignal -Port 65531
$opened = $null
try {
    if ($signal.WaitOne(0)) { throw 'A fresh restart signal must not be set' }
    $self = Get-Process -Id $PID
    $name = Get-LocalRestartSignalName -LauncherProcessId $PID -LauncherStartTime $self.StartTime -Port 65531
    $otherPort = Get-LocalRestartSignalName -LauncherProcessId $PID -LauncherStartTime $self.StartTime -Port 65530
    $otherStart = Get-LocalRestartSignalName -LauncherProcessId $PID -LauncherStartTime ($self.StartTime.AddTicks(1)) -Port 65531
    if ($name -eq $otherPort -or $name -eq $otherStart) { throw 'Restart signals must be scoped to port and launcher lifetime' }
    $opened = [Threading.EventWaitHandle]::OpenExisting($name)
    $null = $opened.Set()
    if (-not $signal.WaitOne(0)) { throw 'Expected explicit restart acknowledgement' }
} finally {
    if ($null -ne $opened) { $opened.Dispose() }
    $signal.Dispose()
}
'''.replace('__HELPER__', lifecycle_path)
    subprocess.run([powershell, '-NoProfile', '-NonInteractive', '-Command', lifecycle_checks],
                   check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=30)
    check('Only explicitly acknowledged restart exits are accepted; unknown failures remain errors and signals are isolated')
    with tempfile.TemporaryDirectory(prefix='structure-', dir=ARTIFACTS) as temp:
        project = Path(temp) / 'project with spaces'
        caller = Path(temp) / 'unrelated caller'
        project.mkdir()
        caller.mkdir()
        for name in ['start-local.ps1', 'start-local.cmd', 'start-postgres-local.ps1', 'start-postgres-local.cmd',
                     'scripts/docker-compose-local-postgres.yml', 'scripts/start-local.ps1',
                     'scripts/admin-environment.ps1', 'scripts/local-env.ps1', 'scripts/local-postgres-state.ps1', 'scripts/local-service-lifecycle.ps1',
                     'scripts/start-dev.ps1', 'scripts/restart-local.ps1', 'scripts/restart-local.cmd',
                     'internal/start-local.ps1']:
            destination = project / name
            destination.parent.mkdir(exist_ok=True)
            shutil.copyfile(ROOT / name, destination)

        def run(entry, *args, fail=False, extra_env=None):
            env = dict(os.environ)
            env.pop('DATA_DIR', None)
            env.update(extra_env or {})
            result = subprocess.run([powershell, '-NoLogo', '-NoProfile', '-NonInteractive',
                        '-ExecutionPolicy', 'RemoteSigned', '-File', str(project / entry), *args],
                        cwd=caller, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                        encoding='utf-8', errors='replace', timeout=30)
            if fail:
                assert result.returncode != 0, entry
                return result
            assert result.returncode == 0, entry + ': ' + result.stderr[-1500:]
            return json.loads(result.stdout.lstrip('\ufeff'))

        initial = run('start-local.ps1', '-Check')
        assert Path(initial['project_root']) == project
        assert Path(initial['data_directory']) == project / '.local/postgres-local/data'
        assert initial['address'] == '0.0.0.0:8091' and initial['store'] == 'postgres'
        entries = ['start-local.ps1', 'start-postgres-local.ps1', 'scripts/start-local.ps1',
                   'scripts/start-dev.ps1', 'scripts/restart-local.ps1', 'internal/start-local.ps1']
        for entry in entries:
            config = run(entry, '-Check')
            assert config['store'] == 'postgres' and config['data_directory'] == initial['data_directory'], entry
            assert run(entry, '-Check', '-Address', '0.0.0.0:8092')['address'] == '0.0.0.0:8092', entry
            assert run(entry, '-Check', '-Address', '127.0.0.1:8091')['address'] == '127.0.0.1:8091', entry
            lan = run(entry, '-Check', '-Lan')
            assert lan['address'] == '0.0.0.0:8091' and lan['database_host'] == '127.0.0.1', entry
            assert 'either -Lan or -Address' in run(
                entry, '-Check', '-Lan', '-Address', '127.0.0.1:8091', fail=True).stderr, entry
            if entry != 'scripts/start-dev.ps1':
                assert run(entry, '-Check', '-Lan', '-ApiPort', '8092')['address'] == '0.0.0.0:8092', entry
        batch = subprocess.run(['cmd.exe', '/d', '/c', str(project / 'scripts/restart-local.cmd'), '-Check'],
                               cwd=caller, capture_output=True, encoding='utf-8', errors='replace', timeout=30,
                               env={key: value for key, value in os.environ.items() if key != 'DATA_DIR'})
        assert batch.returncode == 0, batch.stderr[-1500:]
        assert json.loads(batch.stdout.lstrip('\ufeff'))['store'] == 'postgres'
        assert not (project / '.local').exists()
        check('All default launch and restart entries resolve to PostgreSQL and support read-only checks from unrelated cwd')

        modern = run('start-local.ps1', '-Check', '-DataDirectory', 'custom data')
        legacy = run('internal/start-local.ps1', '-Check', '-DataDirectory', 'custom data')
        assert Path(modern['data_directory']) == project / 'custom data'
        assert Path(legacy['data_directory']) == project / 'internal/custom data'
        (project / '.env').write_text('DATA_DIR="folder with spaces"\n', encoding='utf-8')
        configured = run('start-local.ps1', '-Check')
        assert Path(configured['data_directory']) == project / 'folder with spaces'
        overridden = run('start-local.ps1', '-Check', extra_env={'DATA_DIR': 'process dir'})
        assert Path(overridden['data_directory']) == project / 'process dir'
        assert Path(run('start-local.ps1', '-Check', '-NoEnvFile')['data_directory']) == project / '.local/postgres-local/data'
        check('DATA_DIR uses command > process > .env > default; relative paths and legacy argument behavior are preserved')

        assert run('scripts/start-dev.ps1', '-Postgres', '-Check')['store'] == 'postgres'
        assert 'PostgreSQL is required' in run('scripts/start-dev.ps1', '-Memory', '-Check', fail=True).stderr
        assert 'no longer supported' in run('start-local.ps1', '-InitializeAdmin', '-Check', fail=True).stderr
        assert 'file-mode workspace' in run('start-local.ps1', '-ProfileDirectory', 'old', '-Check', fail=True).stderr
        check('Legacy file profile and memory launch are refused rather than silently bypassing PostgreSQL')

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
