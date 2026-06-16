param(
  [string]$Version = "latest",
  [string]$Prefix = "$HOME\.local\bin",
  [string]$Repo = "yurika0211/luckyharness"
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

$release = Invoke-RestMethod -Uri $apiUrl -Headers @{ "User-Agent" = "LuckyHarnessInstaller" }
$asset = $release.assets | Where-Object { $_.name -eq $archiveName } | Select-Object -First 1

if (-not $asset) {
  throw "could not find release asset: $archiveName"
}

New-Item -ItemType Directory -Force -Path $Prefix | Out-Null
$tmpDir = Join-Path $env:TEMP ("lh-" + [guid]::NewGuid().ToString())
New-Item -ItemType Directory -Force -Path $tmpDir | Out-Null

$archivePath = Join-Path $tmpDir $archiveName
Invoke-WebRequest -Uri $asset.browser_download_url -OutFile $archivePath
Expand-Archive -Path $archivePath -DestinationPath $tmpDir -Force
Copy-Item -Force (Join-Path $tmpDir "lh.exe") (Join-Path $Prefix "lh.exe")

Write-Host "installed lh to $Prefix\lh.exe"
