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
    'start-local.ps1', 'scripts/start-local.ps1', 'scripts/admin-environment.ps1',
    'scripts/local-service-lifecycle.ps1', 'internal/start-local.ps1',
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
launcher_files = {'start-local.ps1', 'scripts/start-local.ps1', 'scripts/admin-environment.ps1',
                  'scripts/local-service-lifecycle.ps1', 'scripts/restart-local.ps1',
                  'scripts/restart-local.cmd'}
assert launcher_files <= package_keys, launcher_files - package_keys
for key, value in zip(package_files.keys, package_files.values):
    if isinstance(key, ast.Constant) and key.value in launcher_files:
        assert (isinstance(value, ast.BinOp) and isinstance(value.op, ast.Div)
                and isinstance(value.left, ast.Name) and value.left.id == 'ROOT'
                and isinstance(value.right, ast.Constant) and value.right.value == key.value), key.value
check('Portable archive whitelist includes every local launcher runtime dependency and restart entry point')

powershell = shutil.which('powershell.exe') if os.name == 'nt' else None
if powershell:
    paths = [ROOT / name for name in ['start-local.ps1', 'scripts/start-local.ps1',
             'scripts/start-dev.ps1', 'scripts/restart-local.ps1', 'scripts/init-env.ps1',
             'scripts/admin-environment.ps1', 'scripts/local-service-lifecycle.ps1', 'internal/start-local.ps1']]
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
        for name in ['start-local.ps1', 'scripts/start-local.ps1', 'scripts/admin-environment.ps1', 'scripts/local-service-lifecycle.ps1',
                     'scripts/start-dev.ps1', 'scripts/restart-local.ps1', 'scripts/restart-local.cmd',
                     'internal/start-local.ps1']:
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
        assert initial['address'] == '0.0.0.0:8091'
        restart = run('scripts/restart-local.ps1', '-Check', '-SkipBuild', '-NoEnvFile')
        assert restart['data_directory'] == initial['data_directory']
        assert restart['address'] == initial['address']
        assert Path(restart['executable']) == project / 'dist/wzstudy-v0.3.0-windows-amd64.exe'
        batch = subprocess.run(['cmd.exe', '/d', '/c', str(project / 'scripts/restart-local.cmd'), '-Check'],
                               cwd=caller, capture_output=True, encoding='utf-8', errors='replace', timeout=30)
        assert batch.returncode == 0, batch.stderr[-1500:]
        assert json.loads(batch.stdout.lstrip('\ufeff'))['data_directory'] == initial['data_directory']
        assert not (project / '.local').exists()
        check('Root and restart launchers (including double-click wrapper) support unrelated cwd, spaces and read-only -Check')

        entries = ['start-local.ps1', 'scripts/start-local.ps1', 'scripts/start-dev.ps1',
                   'scripts/restart-local.ps1', 'internal/start-local.ps1']
        for entry in entries:
            config = run(entry, '-Check')
            assert config['data_directory'] == initial['data_directory']
            assert config['address'] == '0.0.0.0:8091', entry
            for address in ['127.0.0.1:8092', '192.0.2.10:8092']:
                assert run(entry, '-Check', '-Address', address)['address'] == address, entry
        check('All launchers share the durable workspace and default LAN listener; explicit loopback/IP addresses are preserved')

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
        assert pg['address'] == memory['address'] == '0.0.0.0:8091'
        for mode in ['-Postgres', '-Memory']:
            assert run('scripts/start-dev.ps1', mode, '-Address', '127.0.0.1:8092',
                       '-Check')['address'] == '127.0.0.1:8092'
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

        # Only a leaf marker is needed: the foreign listener must be refused before launch.
        packaged = project / 'dist/wzstudy-v0.3.0-windows-amd64.exe'
        packaged.parent.mkdir()
        packaged.write_bytes(b'synthetic executable marker; never run')
        foreign = subprocess.Popen([sys.executable, '-c',
            "import socket,time; s=socket.socket(); s.bind(('127.0.0.1',0)); s.listen(); "
            "print(s.getsockname()[1],flush=True); time.sleep(60)"], stdout=subprocess.PIPE, text=True)
        try:
            port = int(foreign.stdout.readline().strip())
            result = run('scripts/restart-local.ps1', '-Address', f'127.0.0.1:{port}',
                         '-ProfileDirectory', str(old_profile), '-SkipBuild', fail=True)
            assert 'not a recognized executable' in result.stderr, result.stderr[-1500:]
            assert foreign.poll() is None
        finally:
            foreign.terminate()
            foreign.wait(timeout=10)
            foreign.stdout.close()
        check('Restart refuses an unrelated port owner without stopping it or reading fixture keys')

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
