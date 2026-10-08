$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
Set-Location -LiteralPath $projectRoot
$runtimeFile = Join-Path $projectRoot '.local/runtime-path'
if (!(Test-Path -LiteralPath $runtimeFile)) { throw '本机可移植运行时不存在，请使用 README 中的 Docker Compose 启动方式。' }
$taskRuntime = (Get-Content -Raw -LiteralPath $runtimeFile).Trim()
$pgControl = Join-Path $taskRuntime 'pgsql/bin/pg_ctl.exe'
$pgData = Join-Path $taskRuntime 'pgdata'
if (!(Test-Path -LiteralPath $pgControl)) { throw '临时运行时已被清理，请使用 Docker Compose。' }
& $pgControl -D $pgData status | Out-Null
if ($LASTEXITCODE -ne 0) {
    & $pgControl -D $pgData -l (Join-Path $taskRuntime 'postgres.log') -o '-h 127.0.0.1 -p 55432' -w start
    if ($LASTEXITCODE -ne 0) { throw 'PostgreSQL 启动失败' }
}
$redisDir = Join-Path $taskRuntime 'redis'
& (Join-Path $redisDir 'redis-cli.exe') -p 56379 ping 2>$null | Out-Null
if ($LASTEXITCODE -ne 0) {
    $cacheProcess = Start-Process -FilePath (Join-Path $redisDir 'redis-server.exe') -ArgumentList 'local.conf' -WorkingDirectory $redisDir -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $taskRuntime 'redis.stdout.log') -RedirectStandardError (Join-Path $taskRuntime 'redis.stderr.log')
    $cacheProcess.Id | Set-Content -LiteralPath '.local/redis.pid'
}
$env:GOCACHE = Join-Path $projectRoot '.local/gocache'
$env:GOMODCACHE = Join-Path $projectRoot '.local/gomodcache'
if (!(Test-Path -LiteralPath '.env')) { & (Join-Path $PSScriptRoot 'setup.ps1') }
if (!(Test-Path -LiteralPath 'frontend/dist/index.html')) {
    npm --prefix frontend ci --cache .local/npm-cache
    if ($LASTEXITCODE -ne 0) { throw '前端依赖安装失败' }
    npm --prefix frontend run build
    if ($LASTEXITCODE -ne 0) { throw '前端构建失败' }
}
$binary = Join-Path $projectRoot '.local/luma.exe'
if (Test-Path -LiteralPath '.local/server.pid') {
    $existing = Get-Process -Id (Get-Content -LiteralPath '.local/server.pid') -ErrorAction SilentlyContinue
    if ($existing -and $existing.Path -eq $binary) { Write-Host 'Luma 已在运行：http://localhost:8090'; exit 0 }
}
go build -o $binary ./cmd/server
if ($LASTEXITCODE -ne 0) { throw 'Go 构建失败' }
$databasePassword = (Get-Content -Raw -LiteralPath '.local/db-password').Trim()
$env:DATABASE_URL = "postgres://luma:${databasePassword}@127.0.0.1:55432/luma?sslmode=disable"
$env:REDIS_URL = 'redis://127.0.0.1:56379/0'
$env:APP_ADDR = ':8090'
$env:INSTANCE_ID = 'local-a'
$process = Start-Process -FilePath $binary -WorkingDirectory $projectRoot -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $projectRoot '.local/server.stdout.log') -RedirectStandardError (Join-Path $projectRoot '.local/server.stderr.log')
$process.Id | Set-Content -LiteralPath '.local/server.pid'
Write-Host 'Luma 正在启动：http://localhost:8090。账号密码位于项目 .env。'
