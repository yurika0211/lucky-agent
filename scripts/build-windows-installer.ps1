param(
  [string]$Version = "dev",
  [string]$OutputDir = "dist",
  [string]$NodeRuntimeRoot = ""
)

$ErrorActionPreference = "Stop"
$RepoRoot = Split-Path -Parent $PSScriptRoot
$StageDir = Join-Path $RepoRoot "dist\windows-installer"
$OutputPath = Join-Path $RepoRoot $OutputDir

function Resolve-ElectronDist {
  param([string]$RepoRoot)
  $candidates = @(
    (Join-Path $RepoRoot "UI\node_modules\electron\dist"),
    (Join-Path $RepoRoot "UI\desktop\node_modules\electron\dist"),
    (Join-Path $RepoRoot "UI\node_modules\@luckyagent\desktop\node_modules\electron\dist")
  )
  foreach ($candidate in $candidates) {
    $exe = Join-Path $candidate "electron.exe"
    if (Test-Path $exe) {
      return $candidate
    }
  }
  return $null
}

Push-Location $RepoRoot
try {
  if (-not $NodeRuntimeRoot) {
    $NodeExe = (Get-Command node -ErrorAction Stop).Source
    $NodeRuntimeRoot = Split-Path -Parent $NodeExe
  }
  if (-not (Test-Path (Join-Path $NodeRuntimeRoot "node.exe"))) {
    throw "Node runtime was not found at $NodeRuntimeRoot"
  }
  if (-not (Test-Path "$RepoRoot\UI\GUI\dist\index.html") -or -not (Test-Path "$RepoRoot\UI\TUI\dist\tui.mjs")) {
    throw "UI release assets are missing; run npm --prefix UI run build first"
  }
  if (-not (Test-Path "$RepoRoot\dist\lh.exe")) {
    throw "Windows binary is missing: dist\lh.exe"
  }
  if (-not (Test-Path "$RepoRoot\UI\desktop\main.cjs")) {
    throw "Desktop shell is missing under UI\desktop"
  }

  $ElectronDist = Resolve-ElectronDist -RepoRoot $RepoRoot
  if (-not $ElectronDist) {
    throw "Electron runtime is missing. Run npm ci --prefix UI so UI/node_modules/electron/dist/electron.exe exists."
  }

  if (Test-Path $StageDir) {
    Remove-Item -Recurse -Force $StageDir
  }
  New-Item -ItemType Directory -Force -Path "$StageDir\UI\GUI" | Out-Null
  New-Item -ItemType Directory -Force -Path "$StageDir\UI\TUI" | Out-Null
  New-Item -ItemType Directory -Force -Path "$StageDir\runtime" | Out-Null
  New-Item -ItemType Directory -Force -Path "$StageDir\desktop\assets" | Out-Null
  New-Item -ItemType Directory -Force -Path "$StageDir\desktop\scripts" | Out-Null

  Copy-Item -Force "$RepoRoot\dist\lh.exe" "$StageDir\lh.exe"
  Copy-Item -Force "$RepoRoot\packaging\windows\ConfigurationCenter.ps1" "$StageDir\ConfigurationCenter.ps1"
  Copy-Item -Force "$RepoRoot\packaging\windows\Install-Portable.ps1" "$StageDir\Install-Portable.ps1"
  Copy-Item -Force "$RepoRoot\packaging\windows\LuckyAgent-TUI.cmd" "$StageDir\LuckyAgent-TUI.cmd"
  Copy-Item -Force "$RepoRoot\packaging\windows\LuckyAgent-GUI.cmd" "$StageDir\LuckyAgent-GUI.cmd"
  Copy-Item -Force "$RepoRoot\packaging\windows\LuckyAgent-Desktop.cmd" "$StageDir\LuckyAgent-Desktop.cmd"
  Copy-Item -Recurse -Force "$RepoRoot\UI\GUI\dist" "$StageDir\UI\GUI\dist"
  Copy-Item -Recurse -Force "$RepoRoot\UI\TUI\dist" "$StageDir\UI\TUI\dist"
  Copy-Item -Recurse -Force $NodeRuntimeRoot "$StageDir\runtime\node"

  # Desktop shell + Electron runtime
  Copy-Item -Force "$RepoRoot\UI\desktop\main.cjs" "$StageDir\desktop\main.cjs"
  Copy-Item -Force "$RepoRoot\UI\desktop\preload.cjs" "$StageDir\desktop\preload.cjs"
  Copy-Item -Force "$RepoRoot\UI\desktop\package.json" "$StageDir\desktop\package.json"
  if (Test-Path "$RepoRoot\UI\desktop\assets") {
    Copy-Item -Recurse -Force "$RepoRoot\UI\desktop\assets\*" "$StageDir\desktop\assets\"
  }
  if (Test-Path "$RepoRoot\UI\desktop\scripts\set-x11-icon.py") {
    Copy-Item -Force "$RepoRoot\UI\desktop\scripts\set-x11-icon.py" "$StageDir\desktop\scripts\set-x11-icon.py"
  }
  New-Item -ItemType Directory -Force -Path "$StageDir\runtime\electron" | Out-Null
  Copy-Item -Recurse -Force "$ElectronDist\*" "$StageDir\runtime\electron\"

  $Compiler = Get-Command iscc -ErrorAction SilentlyContinue
  if (-not $Compiler) { throw "Inno Setup compiler (iscc) is required. Install it with: choco install innosetup" }
  & $Compiler.Source "/DSourceRoot=$StageDir" "/DMyAppVersion=$Version" "/O$OutputPath" "$RepoRoot\packaging\windows\LuckyAgent.iss"
  if ($LASTEXITCODE -ne 0) {
    throw "Inno Setup compilation failed with exit code $LASTEXITCODE"
  }
} finally {
  Pop-Location
}
