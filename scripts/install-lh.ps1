param(
  [string]$Version = "latest",
  [string]$Prefix = "$HOME\.local\bin",
  [string]$Repo = "yurika0211/luckyharness",
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
New-Item -ItemType Directory -Force -Path $Prefix | Out-Null
Copy-Item -Force (Join-Path $tmpDir "lh.exe") (Join-Path $Prefix "lh.exe")

Write-Host "installed lh to $Prefix\lh.exe"

$uiSource = Join-Path $tmpDir "UI"
if (-not (Test-Path $uiSource)) {
  if ($RepoRef) {
    $sourceRef = $RepoRef
  } elseif ($release.tag_name) {
    $sourceRef = $release.tag_name
  } else {
    $sourceRef = "main"
  }
  $repoZip = Join-Path $tmpDir "repo-$sourceRef.zip"
  $repoDir = Join-Path $tmpDir "repo"
  try {
    try {
      Invoke-WebRequest -Uri "https://github.com/$Repo/archive/refs/tags/$sourceRef.zip" -OutFile $repoZip
    } catch {
      Invoke-WebRequest -Uri "https://github.com/$Repo/archive/refs/heads/$sourceRef.zip" -OutFile $repoZip
    }
    New-Item -ItemType Directory -Force -Path $repoDir | Out-Null
    Expand-Archive -Path $repoZip -DestinationPath $repoDir -Force
    $uiIndex = Get-ChildItem -Path $repoDir -Recurse -Filter "index.tsx" |
      Where-Object { $_.FullName -like "*\UI\TUI\src\index.tsx" } |
      Select-Object -First 1
    if ($uiIndex) {
      $uiSource = Split-Path -Parent (Split-Path -Parent (Split-Path -Parent $uiIndex.FullName))
    }
  } catch {
    Write-Warning "could not download TUI files from $Repo@$sourceRef"
  }
}

if (Test-Path $uiSource) {
  if ($env:LH_UI_INSTALL_DIR) {
    $uiDir = $env:LH_UI_INSTALL_DIR
  } else {
    $uiDir = Join-Path $HOME ".local\share\luckyharness\UI"
  }

  $uiParent = Split-Path -Parent $uiDir
  New-Item -ItemType Directory -Force -Path $uiParent | Out-Null
  if (Test-Path $uiDir) {
    Remove-Item -Recurse -Force $uiDir
  }
  Copy-Item -Recurse -Force $uiSource $uiDir

  $npm = Get-Command npm -ErrorAction SilentlyContinue
  if ($npm) {
    Push-Location $uiDir
    try {
      npm ci --silent --omit=optional
    } finally {
      Pop-Location
    }
  } else {
    Write-Warning "npm was not found; install Node.js/npm before running lh tui"
  }

  $runtimeDir = Join-Path $HOME ".luckyharness\runtime"
  New-Item -ItemType Directory -Force -Path $runtimeDir | Out-Null
  Set-Content -Path (Join-Path $runtimeDir "tui-ui-dir") -Value $uiDir
  Write-Host "installed TUI files to $uiDir"
}
