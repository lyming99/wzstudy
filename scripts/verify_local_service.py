"""Isolated Windows PostgreSQL launcher/restart regression with fake Docker.

Builds a disposable synthetic API and migration executable, never starts a real
container or reads the real .env, PostgreSQL profile, credentials, or workspace.
The health endpoint and process lifecycle are real; database behavior is mocked.
"""
from contextlib import ExitStack
from pathlib import Path
from urllib.error import URLError
from urllib.request import urlopen
import json
import os
import shutil
import socket
import subprocess
import sys
import tempfile
import time

from verify_postgres_launcher import FAKE_DOCKER, FAKE_GO, STUB_GO, copy_launcher, free_port, save_state

ROOT = Path(__file__).resolve().parents[1]
PS = shutil.which('powershell.exe')
REAL_GO = shutil.which('go')


def wait_ready(port, launcher):
    deadline = time.monotonic() + 45
    while time.monotonic() < deadline:
        if launcher.poll() is not None:
            raise AssertionError(f'Isolated launcher exited early: {launcher.returncode}')
        try:
            with urlopen(f'http://127.0.0.1:{port}/api/v1/health/ready', timeout=.5) as response:
                if response.status == 200:
                    return
        except (URLError, OSError):
            time.sleep(.2)
    raise AssertionError('Isolated fake service did not become ready')


def listener(port):
    command = (f"$ErrorActionPreference='Stop'; $c=@(Get-NetTCPConnection -State Listen "
               f"-LocalPort {port} -ErrorAction Stop); "
               "$ids=@($c | Select-Object -ExpandProperty OwningProcess -Unique); "
               "if($ids.Count -ne 1){throw 'Expected one listener'}; "
               "$p=Get-Process -Id $ids[0]; Write-Output ($p.Id.ToString()+'|'+$p.Path)")
    result = subprocess.run([PS, '-NoProfile', '-NonInteractive', '-Command', command],
                            capture_output=True, text=True, errors='replace', timeout=30, check=True)
    pid, path = result.stdout.strip().split('|', 1)
    return int(pid), Path(path)


def listener_addresses(port):
    command = (f"Get-NetTCPConnection -State Listen -LocalPort {port} -ErrorAction Stop | "
               "Select-Object -ExpandProperty LocalAddress -Unique")
    result = subprocess.run([PS, '-NoProfile', '-NonInteractive', '-Command', command],
                            capture_output=True, text=True, errors='replace', timeout=30, check=True)
    return {line.strip() for line in result.stdout.splitlines() if line.strip()}


def verify_host_lan_connection(port):
    command = ("Get-NetIPAddress -AddressFamily IPv4 -ErrorAction Stop | "
               "Where-Object { $_.IPAddress -notlike '127.*' -and $_.IPAddress -notlike '169.254.*' } | "
               "Select-Object -ExpandProperty IPAddress -Unique")
    result = subprocess.run([PS, '-NoProfile', '-NonInteractive', '-Command', command],
                            capture_output=True, text=True, errors='replace', timeout=30, check=True)
    addresses = [line.strip() for line in result.stdout.splitlines() if line.strip()]
    if not addresses:
        print('SKIP: no non-loopback IPv4 address available for same-host LAN probing')
        return
    for address in addresses:
        try:
            with socket.create_connection((address, port), timeout=2) as client:
                client.sendall(b'GET /api/v1/health/ready HTTP/1.0\r\nHost: test\r\n\r\n')
                if b' 200 ' in client.recv(256):
                    print('PASS: isolated synthetic API is reachable via a non-loopback IPv4 address on this host')
                    return
        except OSError:
            pass
    raise AssertionError('Wildcard listener could not be reached using any local non-loopback IPv4 address')


def stop_fixture(pid, executable, profile):
    executable = executable.resolve()
    assert executable.name.lower() == 'wzstudy-api.exe'
    assert executable.parent.parent == profile.resolve()
    expected = str(executable).replace("'", "''")
    command = (f"$p=Get-Process -Id {pid} -ErrorAction SilentlyContinue; "
               f"if($p){{if($p.Path -ne '{expected}'){{throw 'Refusing unrelated process'}}; "
               "Stop-Process -InputObject $p -Force -ErrorAction Stop}")
    subprocess.run([PS, '-NoProfile', '-NonInteractive', '-Command', command],
                   capture_output=True, check=True, timeout=30)


def main():
    assert os.name == 'nt' and PS and REAL_GO, 'Windows PowerShell and Go are required'
    tmp_parent = ROOT / '.tmp'
    tmp_parent.mkdir(exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='postgres-restart-test-', dir=tmp_parent) as temp:
        temp = Path(temp)
        project = temp / 'project with spaces'
        copy_launcher(project)
        shutil.copy2(ROOT / 'scripts/restart-local.ps1', project / 'scripts/restart-local.ps1')
        (project / 'go.mod').write_text('module fixture\ngo 1.25\n', encoding='utf-8')
        fakebin = temp / 'fake bin'
        fakebin.mkdir()
        stub_source = STUB_GO.replace(' "strings"\n', ' "strings"\n "net/http"\n')
        stub_source = stub_source.replace(
            '!strings.HasPrefix(os.Getenv("HTTP_ADDR"),"127.0.0.1:")',
            '(!strings.HasPrefix(os.Getenv("HTTP_ADDR"),"127.0.0.1:") && !strings.HasPrefix(os.Getenv("HTTP_ADDR"),"0.0.0.0:"))')
        stub_source = stub_source.replace(' fmt.Println("Fixture API completed")',
            ' http.HandleFunc("/api/v1/health/ready",func(w http.ResponseWriter,r *http.Request){fmt.Fprint(w,"ready")});\n'
            ' if err:=http.ListenAndServe(os.Getenv("HTTP_ADDR"),nil);err!=nil{os.Exit(10)}')
        assert 'http.ListenAndServe' in stub_source
        stub_go = temp / 'stub.go'
        stub_go.write_text(stub_source, encoding='utf-8')
        stub_exe = temp / 'stub.exe'
        subprocess.run([REAL_GO, 'build', '-o', str(stub_exe), str(stub_go)],
                       cwd=temp, check=True, capture_output=True, timeout=120)
        for name, source in [('fake_docker.py', FAKE_DOCKER), ('fake_go.py', FAKE_GO)]:
            (fakebin / name).write_text(source, encoding='utf-8')
        for name, script in [('docker.cmd', 'fake_docker.py'), ('go.cmd', 'fake_go.py')]:
            (fakebin / name).write_text(
                f'@echo off\r\n"{sys.executable}" "{fakebin / script}" %*\r\nexit /b %ERRORLEVEL%\r\n', encoding='utf-8')
        state = temp / 'fake-state.json'
        save_state(state, {'volumes': {}, 'admin': False, 'calls': [], 'password_hash': ''})
        env = {key: value for key, value in os.environ.items()
               if not key.startswith(('SYSTEM_ADMIN_', 'BOOTSTRAP_ADMIN_', 'WZ_FAKE_', 'WZSTUDY_LOCAL_'))
               and key not in ('DOCKER_HOST', 'DOCKER_CONTEXT', 'DATA_DIR', 'DATABASE_URL',
                               'POSTGRES_PASSWORD', 'ADMIN_API_KEY', 'READ_API_KEY')}
        env.update(PATH=str(fakebin) + os.pathsep + env['PATH'], WZ_FAKE_STATE=str(state),
                   WZ_FAKE_STUB=str(stub_exe), PYTHONUTF8='1')
        port, db_port = free_port(), free_port()
        while port == db_port:
            db_port = free_port()
        common = ['-Lan', '-ApiPort', str(port), '-DatabasePort', str(db_port)]
        profile = project / '.local/postgres-local'
        owned = []
        with ExitStack() as stack:
            def launch(entry, name, custom_env=env):
                log = project / name
                handle = stack.enter_context(log.open('w', encoding='utf-8'))
                process = subprocess.Popen([PS, '-NoLogo', '-NoProfile', '-NonInteractive',
                    '-ExecutionPolicy', 'Bypass', '-File', str(project / entry), *common],
                    cwd=project, env=custom_env, stdout=handle, stderr=subprocess.STDOUT)
                owned.append(process)
                return process, log
            try:
                first, first_log = launch('start-local.ps1', 'first.log')
                wait_ready(port, first)
                first_pid, first_path = listener(port)
                assert first_path.parent.parent == profile.resolve()
                output = first_log.read_text(encoding='utf-8', errors='replace')
                assert f'http://127.0.0.1:{port}/workspace/' in output
                addresses = listener_addresses(port)
                # Go may implement an IPv4 wildcard with an IPv6 dual-stack
                # socket on Windows; the non-loopback IPv4 probe below is decisive.
                assert addresses.intersection({'0.0.0.0', '::'}), f'unexpected listener addresses: {addresses}'
                assert 'Candidate service origins for the QR code' in output
                verify_host_lan_connection(port)
                print('PASS: -Lan starts the isolated PostgreSQL-mode API on all IPv4 interfaces')

                unavailable = subprocess.run([PS, '-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass',
                    '-File', str(project / 'scripts/restart-local.ps1'), *common], cwd=project,
                    env=dict(env, WZ_FAKE_NO_DAEMON='1'), capture_output=True, text=True, errors='replace', timeout=45)
                assert unavailable.returncode != 0 and 'No process was stopped' in unavailable.stderr
                assert first.poll() is None and listener(port)[0] == first_pid
                print('PASS: restart refuses an unavailable Docker engine before stopping the existing service')

                replacement, replacement_log = launch('scripts/restart-local.ps1', 'replacement.log')
                first.wait(timeout=45)
                assert first.returncode == 0, first_log.read_text(encoding='utf-8', errors='replace')[-3000:]
                assert 'was stopped by restart-local.ps1' in first_log.read_text(encoding='utf-8', errors='replace')
                wait_ready(port, replacement)
                replacement_pid, replacement_path = listener(port)
                assert replacement_pid != first_pid and replacement_path.parent.parent == profile.resolve()
                print('PASS: explicit restart acknowledges the old launcher and starts a new PostgreSQL-mode process')

                stop_fixture(replacement_pid, replacement_path, profile)
                replacement.wait(timeout=30)
                output = replacement_log.read_text(encoding='utf-8', errors='replace')
                assert replacement.returncode != 0 and '0xFFFFFFFF' in output
                print('PASS: unacknowledged forced termination remains an error')

                foreign = subprocess.Popen([sys.executable, '-c',
                    "import socket,time; s=socket.socket(); s.bind(('127.0.0.1',0)); s.listen(); "
                    "print(s.getsockname()[1],flush=True); time.sleep(60)"], stdout=subprocess.PIPE, text=True)
                try:
                    foreign_port = int(foreign.stdout.readline().strip())
                    result = subprocess.run([PS, '-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass',
                        '-File', str(project / 'scripts/restart-local.ps1'), '-ApiPort', str(foreign_port),
                        '-DatabasePort', str(db_port), '-NoEnvFile'], cwd=project, env=env,
                        capture_output=True, text=True, errors='replace', timeout=45)
                    assert result.returncode != 0 and 'not a recognized executable' in result.stderr
                    assert foreign.poll() is None
                finally:
                    foreign.terminate()
                    foreign.wait(timeout=10)
                    foreign.stdout.close()
                print('PASS: restart refuses a foreign port owner without stopping it')
            finally:
                # Only terminate the known listener if its executable still belongs
                # to this exact temporary project, then reap our own launchers.
                try:
                    pid, path = listener(port)
                    if path.parent.parent == profile.resolve() and path.name.lower() == 'wzstudy-api.exe':
                        stop_fixture(pid, path, profile)
                except (subprocess.SubprocessError, OSError, ValueError):
                    pass
                for process in owned:
                    if process.poll() is None:
                        try:
                            process.wait(timeout=10)
                        except subprocess.TimeoutExpired:
                            process.terminate()
                            process.wait(timeout=10)
    print('PASS: isolated synthetic process test only; no real PostgreSQL/Docker integration was performed')


if __name__ == '__main__':
    main()
