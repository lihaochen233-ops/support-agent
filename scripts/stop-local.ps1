$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
$pidFile = Join-Path $projectRoot '.local/server.pid'
$binary = Join-Path $projectRoot '.local/luma.exe'
if (Test-Path -LiteralPath $pidFile) {
    $process = Get-Process -Id (Get-Content -LiteralPath $pidFile) -ErrorAction SilentlyContinue
    if ($process -and $process.Path -eq $binary) { Stop-Process -Id $process.Id; Write-Host 'Luma 应用已停止，数据库保留运行。' }
}
