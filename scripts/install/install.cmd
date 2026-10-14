@echo off
rem nano-llm-proxy installer for Windows CMD. Downloads install.ps1 from the
rem latest release and runs it; arguments pass through, e.g.
rem   install.cmd -Version v0.3.0 -Port 9000 -NoOpen
rem   install.cmd -Uninstall -Purge
rem One line:
rem   curl -fsSL https://github.com/kmmuntasir/nano-llm-proxy/releases/latest/download/install.cmd -o install.cmd && install.cmd && del install.cmd
setlocal
set "NANO_PS1=%TEMP%\nano-llm-proxy-install.ps1"
set "NANO_PS1_URL=https://github.com/kmmuntasir/nano-llm-proxy/releases/latest/download/install.ps1"
if defined NANO_INSTALL_BASE_URL set "NANO_PS1_URL=%NANO_INSTALL_BASE_URL%/install.ps1"

curl.exe -fsSL "%NANO_PS1_URL%" -o "%NANO_PS1%"
if errorlevel 1 (
    echo error: could not download %NANO_PS1_URL% 1>&2
    exit /b 1
)

powershell -NoProfile -ExecutionPolicy Bypass -File "%NANO_PS1%" %*
set "NANO_RC=%ERRORLEVEL%"
del /q "%NANO_PS1%" >nul 2>&1
exit /b %NANO_RC%
