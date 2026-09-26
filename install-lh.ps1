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

if ($Version -eq "latest") {
  $apiUrl = "https://api.github.com/repos/$Repo/releases/latest"
} else {
  $apiUrl = "https://api.github.com/repos/$Repo/releases/tags/$Version"
}

$release = Invoke-RestMethod -Uri $apiUrl -Headers @{ "User-Agent" = "LuckyAgentInstaller" }
$asset = $release.assets | Where-Object { $_.name -eq $archiveName } | Select-Object -First 1

if (-not $asset) {
  throw "could not find release asset: $archiveName"
}

$tmpDir = Join-Path $env:TEMP ("lh-" + [guid]::NewGuid().ToString())
New-Item -ItemType Directory -Force -Path $tmpDir | Out-Null

$archivePath = Join-Path $tmpDir $archiveName
Invoke-WebRequest -Uri $asset.browser_download_url -OutFile $archivePath
Expand-Archive -Path $archivePath -DestinationPath $tmpDir -Force
$installer = Join-Path $tmpDir "Install-Portable.ps1"
if (-not (Test-Path $installer)) {
  throw "release asset is missing Install-Portable.ps1"
}
& $installer -SourceRoot $tmpDir -InstallDir $Prefix
