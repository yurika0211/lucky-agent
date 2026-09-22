@echo off
setlocal
set "APP_ROOT=%~dp0"
set "ELECTRON=%APP_ROOT%runtime\electron\electron.exe"
if not exist "%ELECTRON%" (
  echo LuckyAgent desktop runtime is not installed.
  echo The Windows package still has the browser GUI launcher: LuckyAgent-GUI.cmd
  exit /b 1
)
set "LH_ELECTRON_LOAD=%LH_ELECTRON_LOAD%"
if "%LH_ELECTRON_LOAD%"=="" set "LH_ELECTRON_LOAD=dist"
set "LH_APP_ROOT=%APP_ROOT%"
cd /d "%APP_ROOT%desktop"
"%ELECTRON%" "%APP_ROOT%desktop" %*
