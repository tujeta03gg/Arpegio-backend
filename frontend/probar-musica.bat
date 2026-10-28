@echo off
rem Prueba con tu musica real: arpegiod (en WSL, con audio) + la app web en http://127.0.0.1:7601
rem La biblioteca se configura en .local\config.toml (roots = [...]).
set "PATH=C:\src\flutter\bin;%PATH%"
cd /d "%~dp0"
wsl -d Ubuntu -u root -- pkill arpegiod
start "arpegiod (WSL)" /min wsl -d Ubuntu -u root -- bash /mnt/c/Users/cesar/Documents/arpegio-front/.local/run-arpegiod.sh
if not exist build\web\index.html call flutter build web --release || exit /b 1
timeout /t 4 >nul
start "" http://127.0.0.1:7601
dart run tool/dev_server.dart 7601 http://127.0.0.1:7600
