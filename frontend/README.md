# arpegio_ui

Interfaz táctil (Flutter) de Arpegio, 800×480. Cliente delgado de `arpegiod`: muestra su estado y le envía comandos. Especificación en `Arpegio-Frontend-Flutter.md`; referencia visual en `arpegio-prototipo.html`.

## Desarrollo

```sh
probar.bat            # demo con datos simulados: app + backend simulado en http://127.0.0.1:7600
probar-musica.bat     # tu música real, todo en Windows (arpegiod.exe + mpv + ffprobe)

# Desarrollo con recarga en caliente (simulador aparte):
dart run tool/fake_arpegiod.dart
flutter run -d chrome --dart-define=ARPEGIO_API=http://127.0.0.1:7600
flutter analyze && flutter test             # goldens en test/goldens (regenerar: --update-goldens)
```

`probar-musica.bat` lee la biblioteca de `.local\config.windows.toml` y necesita `mpv` y `ffmpeg` (`winget install shinchiro.mpv Gyan.FFmpeg`), Go y el backend en `C:\Users\cesar\Documents\Arpegio`. El simulador y el daemon usan el mismo puerto 7600: no los corras a la vez.

Atajos: Espacio play/pausa, ←/→ anterior/siguiente, 1–6 destinos, Esc volver.

## Estructura

`lib/design` tokens, tipografía, tema y chaflán · `lib/data` modelos y clientes REST/WebSocket · `lib/state` providers de Riverpod · `lib/widgets` componentes · `lib/screens` pantallas. Los huecos del API están en `docs/huecos-api.md`.

## Estado por fase

A, B y C implementadas contra el simulador. Pendientes: D (flutter-pi en la Pi: build, systemd, perfilado) y E (letras, radio desde Inicio, `/stats`).
