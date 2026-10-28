@echo off
rem Prueba con tu musica real, todo en Windows (sin WSL): arpegiod.exe + mpv.exe + ffprobe.exe.
rem Sirve tambien la app en http://127.0.0.1:7600. Biblioteca y opciones: .local\config.windows.toml
set "PATH=C:\src\flutter\bin;C:\Program Files\Go\bin;C:\Program Files\MPV Player;%LOCALAPPDATA%\Microsoft\WinGet\Packages\Gyan.FFmpeg_Microsoft.Winget.Source_8wekyb3d8bbwe\ffmpeg-9.0.2-full_build\bin;%PATH%"
cd /d "%~dp0"
taskkill /f /im arpegiod.exe >nul 2>&1
taskkill /f /im mpv.exe >nul 2>&1
call flutter build web --release || exit /b 1
rem Recompila el backend desde su carpeta para recoger cambios nuevos.
pushd C:\Users\cesar\Documents\Arpegio
go build -o "%~dp0.local\arpegiod.exe" .\cmd\arpegiod || exit /b 1
popd
start "arpegiod" /min "%~dp0.local\arpegiod.exe" -config "%~dp0.local\config.windows.toml"
timeout /t 6 >nul
start "" http://127.0.0.1:7600