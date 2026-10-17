param(
  [string]$Version = "latest",
  [string]$Prefix = (Join-Path $env:LOCALAPPDATA "LuckyAgent"),
  [string]$Repo = "yurika0211/lucky-agent",
  [string]$RepoRef = ""
)

$ErrorActionPreference = "Stop"

$os = "windows"
$arch = "amd64"
$archiveName = "lh-$os-$arch.zip"

$tmpDir = Join-Path $env:TEMP ("lh-" + [guid]::NewGuid().ToString())
New-Item -ItemType Directory -Force -Path $tmpDir | Out-Null

$manifestUrl = if ($Version -eq "latest") {
  "https://github.com/$Repo/releases/latest/download/update.json"
} else {
  $tag = if ($Version.StartsWith("v")) { $Version } else { "v$Version" }
  "https://github.com/$Repo/releases/download/$tag/update.json"
}

$manifestPath = Join-Path $tmpDir "update.json"
Invoke-WebRequest -Uri $manifestUrl -OutFile $manifestPath
$manifest = Get-Content -Raw -Path $manifestPath | ConvertFrom-Json
$asset = $manifest.assets | Where-Object { $_.name -eq $archiveName } | Select-Object -First 1

if (-not $asset -or [string]::IsNullOrWhiteSpace($asset.download_url)) {
  throw "could not find release asset: $archiveName"
}

$archivePath = Join-Path $tmpDir $archiveName
Invoke-WebRequest -Uri $asset.download_url -OutFile $archivePath
Expand-Archive -Path $archivePath -DestinationPath $tmpDir -Force
$installer = Join-Path $tmpDir "Install-Portable.ps1"
if (-not (Test-Path $installer)) {
  throw "release asset is missing Install-Portable.ps1"
}
& $installer -SourceRoot $tmpDir -InstallDir $Prefix
