"""Windows foreground restart regression using a copied binary and synthetic profile.

Build first: go build -o .artifacts/wzstudy-launcher-test.exe ./cmd/api
Run: python scripts/verify_local_service.py
Never starts, stops, or reads the real local workspace or its credentials.
"""
from contextlib import ExitStack
from pathlib import Path
import argparse
import json
import os
import secrets
import shutil
import socket
import subprocess
import tempfile
import time
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
ARTIFACTS = ROOT / '.artifacts'
PS = shutil.which('powershell.exe')


def quoted(value):
    return "'" + str(value).replace("'", "''") + "'"


def powershell(command):
    result = subprocess.run(
        [PS, '-NoLogo', '-NoProfile', '-NonInteractive', '-Command',
         "$ErrorActionPreference='Stop'; " + command + '; exit 0'],
        cwd=ROOT, capture_output=True, text=True, errors='replace', timeout=30)
    assert result.returncode == 0, result.stderr[-3000:]
    return result.stdout.strip()


def listener(port, expected):
    result = powershell(
        f"$connections=@(Get-NetTCPConnection -State Listen -LocalPort {port} "
        "-ErrorAction SilentlyContinue); "
        # Go may use a dual-stack [::] socket for an all-interface TCP listener.
        # The readiness request above must also succeed over IPv4 loopback.
        "if(@($connections | Where-Object {$_.LocalAddress -in @('0.0.0.0','::')}).Count -lt 1)"
        "{throw ('Expected wildcard listener; got: '+($connections.LocalAddress -join ','))}; "
        "$ids=@($connections | Select-Object -ExpandProperty OwningProcess -Unique); "
        "if($ids.Count -ne 1){throw 'Expected one isolated listener'}; "
        "$service=Get-Process -Id $ids[0]; "
        f"if($service.Path -ne {quoted(expected)}){{throw 'Unexpected isolated process path'}}; $service.Id")
    return int(result)


def wait_ready(port, expected, launcher, previous=None):
    deadline = time.monotonic() + 30
    last_error = None
    while time.monotonic() < deadline:
        assert launcher.poll() is None, f'Isolated launcher exited early: {launcher.returncode}'
        try:
            with urllib.request.urlopen(
                    f'http://127.0.0.1:{port}/api/v1/health/ready', timeout=1) as response:
                if response.status == 200:
                    service_pid = listener(port, expected)
                    if service_pid != previous:
                        return service_pid
        except (OSError, AssertionError) as error:
            last_error = error
        time.sleep(0.2)
    raise AssertionError(f'Isolated service did not become ready; last check: {last_error}')


def verify_lan_access(host, port):
    # Ignore machine proxy settings when checking this computer's own LAN IP.
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    for route in ['/api/v1/health/ready', '/admin/']:
        with opener.open(f'http://{host}:{port}{route}', timeout=5) as response:
            assert response.status == 200, (route, response.status)


def stop(service_pid, expected):
    powershell(
        f"$service=Get-Process -Id {service_pid} -ErrorAction SilentlyContinue; "
        f"if($service){{if($service.Path -ne {quoted(expected)}){{throw 'Refusing unrelated process'}}; "
        "Stop-Process -InputObject $service -Force}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, default=ARTIFACTS / 'wzstudy-launcher-test.exe')
    parser.add_argument('--lan-host', help='Optional local LAN IPv4 address to verify without a proxy')
    args = parser.parse_args()
    assert os.name == 'nt' and PS, 'Windows PowerShell is required'
    assert args.binary.is_file(), 'Build .artifacts/wzstudy-launcher-test.exe first'
    ARTIFACTS.mkdir(exist_ok=True)
    checks = []
    report = {'scope': 'Copied binary, temporary synthetic profile and random port only; no real keys/data read'}
    with tempfile.TemporaryDirectory(prefix='local-service-', dir=ARTIFACTS) as temp:
        project = Path(temp) / 'project with spaces'
        (project / 'scripts').mkdir(parents=True)
        (project / 'dist').mkdir()
        shutil.copy2(ROOT / 'start-local.ps1', project / 'start-local.ps1')
        for name in ['start-local.ps1', 'restart-local.ps1', 'admin-environment.ps1',
                     'local-service-lifecycle.ps1']:
            shutil.copy2(ROOT / 'scripts' / name, project / 'scripts' / name)
        executable = project / 'dist' / 'wzstudy-v0.3.0-windows-amd64.exe'
        shutil.copy2(args.binary, executable)
        with socket.socket() as sock:
            sock.bind(('127.0.0.1', 0))
            port = sock.getsockname()[1]
        env = {key: value for key, value in os.environ.items()
               if not key.startswith(('SYSTEM_ADMIN_', 'BOOTSTRAP_ADMIN_'))}
        env.update(SYSTEM_ADMIN_EMAIL='launcher-test@example.test',
                   SYSTEM_ADMIN_PASSWORD=secrets.token_urlsafe(24))
        common = ['-Address', f'0.0.0.0:{port}', '-SkipBuild', '-NoEnvFile']
        processes = []
        with ExitStack() as stack:
            def launch(entry, log_name):
                log = project / log_name
                handle = stack.enter_context(log.open('w', encoding='utf-8'))
                process = subprocess.Popen(
                    [PS, '-NoLogo', '-NoProfile', '-NonInteractive', '-ExecutionPolicy',
                     'RemoteSigned', '-File', str(project / entry), *common],
                    cwd=project, env=env, stdout=handle, stderr=subprocess.STDOUT)
                processes.append(process)
                return process, log

            try:
                first, first_log = launch('start-local.ps1', 'first.log')
                first_pid = wait_ready(port, executable, first)
                time.sleep(3)
                assert first.poll() is None, 'Foreground service exited without a stop request'
                checks.append('Root launcher binds all IPv4 interfaces and remains ready without an external stop')
                first_output = first_log.read_text(encoding='utf-8', errors='replace')
                assert f'Management (this computer): http://127.0.0.1:{port}/admin/' in first_output
                assert f'http://0.0.0.0:{port}/admin/' not in first_output
                checks.append('Wildcard binding is shown separately from the usable local management URL')
                if args.lan_host:
                    verify_lan_access(args.lan_host, port)
                    checks.append('Health and management page are reachable through the local LAN IP')
                replacement, replacement_log = launch('scripts/restart-local.ps1', 'replacement.log')
                replacement_pid = wait_ready(port, executable, replacement, first_pid)
                first.wait(timeout=15)
                first_output = first_log.read_text(encoding='utf-8', errors='replace')
                assert first.returncode == 0, first_output[-3000:]
                assert 'was stopped by restart-local.ps1' in first_output, first_output[-3000:]
                assert 'The service stopped with exit code' not in first_output
                checks.append('Explicit restart acknowledges the old foreground launcher without a false failure')
                assert replacement_pid != first_pid
                checks.append('Replacement service has a different PID, binds all IPv4 interfaces and passes readiness')
                if args.lan_host:
                    verify_lan_access(args.lan_host, port)
                    checks.append('LAN IP access remains available after restart')
                stop(replacement_pid, executable)
                replacement.wait(timeout=15)
                replacement_output = replacement_log.read_text(encoding='utf-8', errors='replace')
                assert replacement.returncode != 0, replacement_output[-3000:]
                for expected in ['exit code -1', '0xFFFFFFFF', 'does not identify the cause']:
                    assert expected in replacement_output, replacement_output[-3000:]
                checks.append('Unacknowledged external termination still fails with actionable exit diagnostics')
                report.update(result='passed', port=port, listen_host='0.0.0.0', lan_host=args.lan_host,
                              first_service_pid=first_pid,
                              replacement_service_pid=replacement_pid,
                              first_launcher_exit_code=first.returncode,
                              external_stop_launcher_exit_code=replacement.returncode,
                              checks=checks)
            except Exception:
                for log_name in ['first.log', 'replacement.log']:
                    log = project / log_name
                    if log.exists():
                        print(f'--- Isolated {log_name} ---', flush=True)
                        print(log.read_text(encoding='utf-8', errors='replace')[-4000:], flush=True)
                raise
            finally:
                # Only the copied executable in this exact disposable fixture can
                # be stopped. This also covers a failure before PID discovery.
                powershell(
                    "$services=@(Get-Process -Name 'wzstudy-v0.3.0-windows-amd64' "
                    "-ErrorAction SilentlyContinue); foreach($service in $services){"
                    f"if($service.Path -eq {quoted(executable)}){{Stop-Process -InputObject $service -Force}}}}")
                for process in processes:
                    if process.poll() is None:
                        try:
                            process.wait(timeout=10)
                        except subprocess.TimeoutExpired:
                            process.terminate()
                            process.wait(timeout=10)
    (ARTIFACTS / 'local-service-verification.json').write_text(
        json.dumps(report, ensure_ascii=False, indent=2), encoding='utf-8')
    print(json.dumps(report, ensure_ascii=False, indent=2), flush=True)


if __name__ == '__main__':
    main()
