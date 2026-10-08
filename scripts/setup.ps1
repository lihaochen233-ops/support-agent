$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
$configPath = Join-Path $projectRoot '.env'
if (Test-Path -LiteralPath $configPath) { Write-Host '.env 已存在，保留现有配置。'; exit 0 }
function New-LocalPassword {
    $bytes = New-Object byte[] 18
    $generator = [Security.Cryptography.RandomNumberGenerator]::Create()
    try { $generator.GetBytes($bytes) } finally { $generator.Dispose() }
    return [BitConverter]::ToString($bytes).Replace('-', '').ToLowerInvariant()
}
$databasePassword = New-LocalPassword
$cachePassword = New-LocalPassword
$adminPassword = New-LocalPassword
$agentPassword = New-LocalPassword
$template = Get-Content -Raw -LiteralPath (Join-Path $projectRoot '.env.example')
$template = $template.Replace('replace_with_a_long_random_password', $databasePassword).Replace('replace_with_another_random_password', $cachePassword).Replace('replace_with_a_password_at_least_12_characters', $adminPassword).Replace('replace_with_another_password_at_least_12_characters', $agentPassword)
[IO.File]::WriteAllText($configPath, $template, [Text.UTF8Encoding]::new($false))
Write-Host '已生成 .env 和随机密码。管理员与客服密码可在该文件中查看；填写两项模型 API Key 后启用 AI。'
