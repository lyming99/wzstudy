"""Package the already built and browser-verified Windows executable.
The archive is an explicit whitelist: no user data, credentials or test logs.
"""
from pathlib import Path
import hashlib
import json
import os
import sys
import zipfile
import subprocess

ROOT=Path(__file__).resolve().parents[1]
DIST=ROOT/'dist'
ARTIFACTS=ROOT/'.artifacts'
VERSION='0.3.0'
EXE=DIST/f'wzstudy-v{VERSION}-windows-amd64.exe'
ARCHIVE=DIST/f'wzstudy-v{VERSION}-windows-amd64.zip'
sys.stdout.reconfigure(encoding='utf-8')
sha=lambda path:hashlib.sha256(path.read_bytes()).hexdigest()
live=json.loads((ARTIFACTS/'live-verification.json').read_text(encoding='utf-8'))
go=json.loads((ARTIFACTS/'go-verification.json').read_text(encoding='utf-8'))
structure=json.loads((ARTIFACTS/'structure-verification.json').read_text(encoding='utf-8'))
race=json.loads((ARTIFACTS/'go-race-verification.json').read_text(encoding='utf-8'))
admin=json.loads((ARTIFACTS/'admin-verification.json').read_text(encoding='utf-8'))
assert all(report['result']=='passed' for report in [live,go,structure,race,admin])
assert EXE.exists() and sha(EXE)==sha(ARTIFACTS/'wzstudy.exe')==live['binary_sha256'], 'Package must use the exact browser-tested binary'
assert (ARTIFACTS/'live-verification.json').stat().st_mtime>=EXE.stat().st_mtime
info=subprocess.run(['go','version','-m',str(EXE)],capture_output=True,text=True,check=True).stdout
settings={}
for line in info.splitlines():
    parts=line.split()
    if len(parts)>=2 and parts[0]=='build' and '=' in parts[1]:
        key,value=parts[1].split('=',1);settings[key]=value
assert settings.get('CGO_ENABLED')=='0' and settings.get('GOOS')=='windows' and settings.get('GOARCH')=='amd64', 'Portable target must be pure Go Windows amd64'
# The PostgreSQL launcher needs the embedded-migrations executable even when
# the archive has no source tree or Go installation.
MIGRATE=ARTIFACTS/'wzstudy-migrate-windows-amd64.exe'
build_env=dict(os.environ, GOOS='windows', GOARCH='amd64', CGO_ENABLED='0')
subprocess.run(['go','build','-trimpath','-o',str(MIGRATE),'./cmd/migrate'],cwd=ROOT,env=build_env,check=True)
files={
    f'dist/{EXE.name}':EXE,
    'dist/wzstudy-migrate.exe':MIGRATE,
    'start-local.ps1':ROOT/'start-local.ps1',
    'start-local.cmd':ROOT/'start-local.cmd',
    'start-postgres-local.ps1':ROOT/'start-postgres-local.ps1',
    'start-postgres-local.cmd':ROOT/'start-postgres-local.cmd',
    '.env.example':ROOT/'.env.example',
    'scripts/docker-compose-local-postgres.yml':ROOT/'scripts/docker-compose-local-postgres.yml',
    'README.md':ROOT/'README.md',
    'LICENSE':ROOT/'LICENSE',
    'docs/VERIFICATION_AUTH.md':ROOT/'docs/VERIFICATION_AUTH.md',
    'docs/ADMIN_AUTH.md':ROOT/'docs/ADMIN_AUTH.md',
    'docs/RUNNING.md':ROOT/'docs/RUNNING.md',
    'docs/PROJECT_LAYOUT.md':ROOT/'docs/PROJECT_LAYOUT.md',
    'docs/VERIFICATION.md':ROOT/'docs/VERIFICATION.md',
    'docs/VERIFICATION_MANAGEMENT.md':ROOT/'docs/VERIFICATION_MANAGEMENT.md',
    'scripts/admin-environment.ps1':ROOT/'scripts/admin-environment.ps1',
    'scripts/local-env.ps1':ROOT/'scripts/local-env.ps1',
    'scripts/local-postgres-state.ps1':ROOT/'scripts/local-postgres-state.ps1',
    'scripts/start-local.ps1':ROOT/'scripts/start-local.ps1',
    'scripts/local-service-lifecycle.ps1':ROOT/'scripts/local-service-lifecycle.ps1',
    'scripts/restart-local.ps1':ROOT/'scripts/restart-local.ps1',
    'scripts/restart-local.cmd':ROOT/'scripts/restart-local.cmd',
    'verification/VERIFICATION.md':ROOT/'docs/VERIFICATION_AUTH.md',
    'docs/API.md':ROOT/'docs/API.md',
    'docs/MANAGEMENT.md':ROOT/'docs/MANAGEMENT.md',
    'docs/openapi.json':ROOT/'docs/openapi.json',
    'verification/admin-verification.json':ARTIFACTS/'admin-verification.json',
    'verification/live-verification.json':ARTIFACTS/'live-verification.json',
    'verification/go-verification.json':ARTIFACTS/'go-verification.json',
    'verification/go-race-verification.json':ARTIFACTS/'go-race-verification.json',
    'verification/structure-verification.json':ARTIFACTS/'structure-verification.json',
    'verification/login-desktop.png':ARTIFACTS/'login-email-desktop.png',
    'verification/login-mobile.png':ARTIFACTS/'login-email-mobile.png',
    'verification/preview-desktop.png':ARTIFACTS/'management-desktop.png',
    'verification/preview-mobile.png':ARTIFACTS/'management-mobile.png',
}
manifest=''.join(f'{sha(path)}  {name}\n' for name,path in sorted(files.items()))
notes=(f'# Verification / 验证说明\n\n版本 {VERSION}。\n\n'
       f'- {live["groups"]} 组真实 Go 服务与浏览器验证通过。\n'
       f'- {go["passed_test_cases"]} 个 Go 测试结果通过；{len(go["skipped_tests"])} 个 PostgreSQL 条件用例未执行，详见 JSON。\n'
       '- `go vet` 与开启 CGO 后的 Go race 检查通过。\n'
       '- 此隔离文件模式验证不包含 PostgreSQL 实库或 Docker 容器运行验证。\n'
       '- 截图中的内容为隔离测试时真正入库的测试资料；正式首次启动为空工作空间。\n'
       '- 不包含预置账号、密钥、`.local` 数据目录或测试服务日志。\n'
       '- 压缩包解压后运行 `start-local.ps1`；需要本机 Docker Desktop 和 Compose，PostgreSQL 数据保存在独立 Docker 数据卷。包内包含 API 和迁移程序，无需 Go、Node 或 FFmpeg。\n'
       '- Windows 便携包未签名，不是安装器，也没有执行公网发布。\n')
with zipfile.ZipFile(ARCHIVE,'w',compression=zipfile.ZIP_DEFLATED,compresslevel=7) as archive:
    for name,path in files.items():archive.write(path,name)
    archive.writestr('SHA256SUMS.txt',manifest)
    archive.writestr('verification/README.md',notes)
(DIST/'SHA256SUMS.txt').write_text(manifest,encoding='utf-8')
release={'version':VERSION,'platform':'windows','arch':'amd64','archive':ARCHIVE.name,'archive_bytes':ARCHIVE.stat().st_size,'archive_sha256':sha(ARCHIVE),'executable':EXE.name,'executable_bytes':EXE.stat().st_size,'executable_sha256':sha(EXE),'browser_check_groups':live['groups'],'go_passed_test_cases':go['passed_test_cases'],'postgres_test_cases_skipped':len(go['skipped_tests'])}
(DIST/'release.json').write_text(json.dumps(release,ensure_ascii=False,indent=2),encoding='utf-8')
print(json.dumps(release,ensure_ascii=False,indent=2),flush=True)
