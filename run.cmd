@echo off
rem Windows launcher for eTape. Delegates to run.ps1, setting the PowerShell
rem execution policy for this one invocation so nothing needs configuring.
rem Usage mirrors run.sh: run.cmd <live|demo|dev> [options]
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0run.ps1" %*
exit /b %ERRORLEVEL%
