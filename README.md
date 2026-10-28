# Arpegio

Daemon de música local para el cyberdeck (Raspberry Pi). Especificación completa en
`Arpegio — Especificación del backend.md`; contrato de la API en `api/openapi.yaml`.

## Uso rápido

```sh
make build            # bin/arpegiod y bin/arpegio
arpegiod              # usa ~/.config/arpegio/config.toml (ver deploy/config.example.toml)
arpegio scan --wait   # indexa las carpetas de [library].roots
arpegio search cafe
arpegio play album:<id>
arpegio tui           # interfaz de terminal
```

Dependencias del sistema en la Pi: `mpv` y `ffmpeg` (`ffprobe`). Sin `ffprobe` no se pueden
ingerir archivos nuevos; sin `mpv` la API de catálogo funciona y los comandos de reproducción
responden 503.

## Desarrollo

```sh
make check            # sqlc + vet + tests + gofmt
make build-arm64      # binarios linux/arm64 sin CGO
```

SQL solo en `internal/store/queries/*.sql` (se genera con `sqlc`). La única excepción es la
consulta FTS5 de `catalog`, porque `sqlc` no entiende `MATCH` sobre tablas virtuales.

## Estado por fase

| Fase | Estado |
| --- | --- |
| 1 Esqueleto y biblioteca | Hecha: config, store+migraciones, metadata, library, catalog+FTS5, API, CLI `scan`/`search` |
| 2 Reproducción | Hecha: cola (shuffle con semilla, repeat, "a continuación"), `playback` con mpv por IPC y supervisor, estado persistido, WebSocket, CLI |
| 3 TUI y colecciones | Hecha, salvo portadas en la TUI: Bubble Tea, playlists, likes, historial con regla de reproducción contada |
| 4 Experiencia tipo Spotify | Hecha: fsnotify + importación por API + jobs, radio por reglas, `/stats`, `/home`, letras `.lrc` |
| 5 Integración con el cyberdeck | Pendiente (necesita hardware): MPRIS por D-Bus, GPIO, instalación en la Pi y medición de RAM/CPU. Ya están `deploy/arpegiod.service` y el build arm64 |

## Límites conocidos

- `crossfade_seconds` se ignora: mpv no tiene crossfade (se avisa en el log).
- Al desactivar shuffle se conserva el orden barajado (no se restaura el original).
- Una carpeta renombrada o movida fuera de las vigiladas deja sus archivos como presentes
  hasta el siguiente `arpegio scan`.
- La ingesta, el parseo de `ffprobe` y el IPC de mpv están probados con falsos; falta
  validarlos con `ffprobe` y `mpv` reales.
