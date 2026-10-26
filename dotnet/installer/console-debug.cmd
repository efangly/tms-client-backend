@echo off
rem Runs TMS Backend in a console window for troubleshooting (live log, Ctrl+C to stop).
rem The Windows service holds the HTTP port and every COM port, so it must be stopped first.
cd /d "%~dp0"
sc query TMSBackend | find "RUNNING" >nul
if not errorlevel 1 (
  echo The TMSBackend service is running. Stop it first ^(as administrator^):
  echo     net stop TMSBackend
  echo and start it again afterwards with: net start TMSBackend
  pause
  exit /b 1
)
tms-backend.exe
pause
