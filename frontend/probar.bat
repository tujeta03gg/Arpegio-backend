@echo off
rem Compila la app web y la sirve junto con el backend simulado en http://127.0.0.1:7600
rem Requiere Flutter: si no esta en el PATH se usa C:\src\flutter
set "PATH=C:\src\flutter\bin;%PATH%"
cd /d "%~dp0"
call flutter build web --release || exit /b 1
start "" http://127.0.0.1:7600
dart run tool/fake_arpegiod.dart
