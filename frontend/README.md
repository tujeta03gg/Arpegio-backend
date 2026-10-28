# arpegio_ui

Interfaz táctil (Flutter) de Arpegio, 800×480. Cliente delgado de `arpegiod`: muestra su estado y le envía comandos. Especificación en `Arpegio-Frontend-Flutter.md`; referencia visual en `arpegio-prototipo.html`.

## Desarrollo

```sh
dart run tool/fake_arpegiod.dart            # backend simulado en :7600
flutter run -d chrome                       # o -d windows / macos / linux
# contra otro daemon:
flutter run --dart-define=ARPEGIO_API=http://127.0.0.1:7600
flutter analyze && flutter test             # goldens en test/goldens (regenerar: --update-goldens)
```

Atajos: Espacio play/pausa, ←/→ anterior/siguiente, 1–6 destinos, Esc volver.

## Estructura

`lib/design` tokens, tipografía, tema y chaflán · `lib/data` modelos y clientes REST/WebSocket · `lib/state` providers de Riverpod · `lib/widgets` componentes · `lib/screens` pantallas. Los huecos del API están en `docs/huecos-api.md`.

## Estado por fase

A, B y C implementadas contra el simulador. Pendientes: D (flutter-pi en la Pi: build, systemd, perfilado) y E (letras, radio desde Inicio, `/stats`).
