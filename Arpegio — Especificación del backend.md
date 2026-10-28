# Arpegio — Especificación del backend

Oct 27, 2026 · @Cesar

## Visión general

Arpegio es un daemon de música local, de un solo usuario, que replica en backend lo que hacen Spotify o Apple Music: biblioteca, búsqueda, cola, playlists, historial y recomendaciones, pero con archivos propios y sin internet.

Corre en un cyberdeck basado en Raspberry Pi 4 (4 GB o más). El backend es el producto; el frontend será un prototipo intercambiable (primero una TUI) hasta definir su forma final.

- **`arpegiod`**: el daemon. Administra la biblioteca, la base de datos, la cola y controla la reproducción.
- **`arpegio`**: el cliente de línea de comandos y la TUI. Habla con el daemon por la API local.
- La música sigue sonando aunque se cierre cualquier interfaz.

## Restricciones y principios

Todo el diseño parte de cuatro restricciones: un usuario, sin red, hardware limitado y batería.

- **Un solo usuario.** No hay cuentas, roles ni autenticación. La API escucha solo en `127.0.0.1` o en un socket Unix.
- **100% local y offline.** Ninguna función depende de internet. El enriquecimiento externo (MusicBrainz, AcoustID) es opcional y desactivable.
- **Hardware modesto.** Objetivo: menos de 150 MB de RAM en reposo para el daemon y CPU casi en cero cuando no hay ingesta.
- **Batería.** Nada de sondeos constantes: se usan eventos (inotify, WebSocket, D-Bus) en lugar de polling.
- **Backend primero.** Toda la lógica vive en el daemon; los clientes solo muestran estado y envían comandos.
- **Sin magia.** Sin ORM ni framework web; SQL escrito a mano y código explícito.
- **Archivos originales intactos.** Arpegio nunca modifica los archivos de audio del usuario; los metadatos editados viven en la base de datos.

## Stack tecnológico

Go con la librería estándar como base, SQLite como única base de datos y mpv como motor de audio; sin framework web ni ORM.

| Capa | Elección | Notas |
| --- | --- | --- |
| Lenguaje | Go 1.23+ | Un solo binario `linux/arm64`, sin CGO |
| HTTP y rutas | `net/http` estándar | `ServeMux` con patrones `GET /tracks/{id}`; `chi` solo si hace falta agrupar middleware |
| WebSocket | `github.com/coder/websocket` | Eventos de estado en tiempo real |
| Base de datos | SQLite vía `modernc.org/sqlite` | Modo WAL, sin CGO |
| Consultas | `sqlc` | Genera Go tipado desde SQL |
| Migraciones | `goose` | Migraciones SQL embebidas con `embed` |
| Búsqueda | SQLite FTS5 | Tokenizador `unicode61 remove_diacritics 2` |
| Reproducción | mpv por IPC JSON (socket Unix) | Gapless, ReplayGain, crossfade; salida a PipeWire |
| Metadatos | `github.com/dhowden/tag` + `ffprobe` | Tags, duración, bitrate, portadas |
| Vigilancia de carpetas | `github.com/fsnotify/fsnotify` | Ingesta automática al copiar archivos |
| Configuración | `github.com/BurntSushi/toml` | `~/.config/arpegio/config.toml` |
| Logs | `log/slog` estándar | JSON a archivo, texto a consola |
| MPRIS | `github.com/godbus/dbus/v5` | Teclas multimedia y controles Bluetooth |
| GPIO (opcional) | `periph.io` | Botones y encoder rotatorio |
| CLI | `github.com/spf13/cobra` | Subcomandos de `arpegio` |
| TUI | Bubble Tea, Bubbles, Lip Gloss | Frontend prototipo |
| Contrato de API | OpenAPI 3.1 en `api/openapi.yaml` | Fuente de verdad para futuros frontends |

Descartados a propósito: PostgreSQL, Redis, Docker, ORMs (GORM) y frameworks web (Gin, Echo, Fiber).

## Arquitectura

Arpegio es un daemon en capas: adaptadores de entrada arriba, dominios en medio e infraestructura abajo; ninguna interfaz toca la base de datos ni el audio directamente.

&#91;embedded content: arquitectura de arpegiod · 4 capas\]

Los tres adaptadores (`api`, `mpris`, `hw`) traducen comandos externos a llamadas de los servicios de dominio. Los dominios se coordinan por el bus de `events` y persisten todo en SQLite a través de `store`.

El audio lo produce un proceso de mpv que el daemon lanza y supervisa. Así Arpegio obtiene gapless, ReplayGain, crossfade y soporte de formatos sin implementarlos, y la música sigue sonando aunque se cierre la TUI.

## Estructura del repositorio

El código se organiza por dominio dentro de `internal/`; cada paquete es dueño de sus tablas y expone un servicio con interfaz pequeña.

```text
arpegio/
├── cmd/
│   ├── arpegiod/            # main del daemon: carga config, arma servicios, arranca API
│   └── arpegio/             # CLI (cobra) + TUI (Bubble Tea)
├── internal/
│   ├── app/                 # wiring: construye y conecta los servicios
│   ├── config/              # lectura y validación de config.toml
│   ├── events/              # bus de eventos interno (pub/sub en memoria)
│   ├── library/             # escaneo, fsnotify, ingesta, hash, portadas
│   ├── metadata/            # lectura de tags, ffprobe, normalización de nombres
│   ├── catalog/             # artistas, álbumes, pistas, géneros, búsqueda FTS5
│   ├── playback/            # cliente IPC de mpv, estado del reproductor
│   ├── queue/               # cola, shuffle, repeat, "reproducir a continuación"
│   ├── playlists/           # playlists manuales, "me gusta"
│   ├── history/             # registro de reproducciones y estadísticas
│   ├── radio/               # recomendaciones a partir de una pista o artista
│   ├── lyrics/              # letras .lrc locales sincronizadas
│   ├── jobs/                # worker de tareas en segundo plano sobre SQLite
│   ├── api/                 # handlers REST, WebSocket, middleware, DTOs
│   ├── mpris/               # servidor D-Bus MPRIS2
│   ├── hw/                  # GPIO opcional (botones, encoder)
│   └── store/               # conexión SQLite, migraciones, código sqlc
│       ├── migrations/      # *.sql de goose
│       ├── queries/         # *.sql de sqlc, uno por dominio
│       └── db/              # código generado (no editar)
├── api/openapi.yaml         # contrato de la API
├── deploy/
│   ├── arpegiod.service     # unidad systemd de usuario
│   └── config.example.toml
├── scripts/                 # build cruzado, seed de pruebas
├── testdata/                # audios cortos de prueba con tags
├── sqlc.yaml
├── Makefile
└── go.mod
```

Regla de dependencias: `api`, `mpris` y `hw` dependen de los servicios de dominio; los dominios nunca importan `api`. Los dominios se comunican por el bus de `events` o por interfaces, nunca leyendo tablas ajenas.

## Módulos de dominio

Cada módulo tiene una responsabilidad única; esta tabla es la referencia para decidir dónde vive cada función nueva.

| Módulo | Responsabilidad | Tablas propias | Eventos que emite |
| --- | --- | --- | --- |
| `library` | Escanear carpetas, detectar archivos nuevos o borrados, calcular hash, extraer portadas, encolar ingesta | `library_roots`, `files`, `artwork` | `library.file_added`, `library.file_removed`, `library.scan_finished` |
| `metadata` | Leer tags y propiedades técnicas, normalizar nombres de artistas y álbumes | (ninguna; es servicio puro) | — |
| `catalog` | Modelo musical: artistas, álbumes, pistas, géneros; búsqueda; ediciones manuales de metadatos | `artists`, `albums`, `tracks`, `track_artists`, `genres`, `track_genres`, `search_index` | `catalog.updated` |
| `playback` | Controlar mpv: play, pausa, seek, volumen, salida; publicar posición y estado | `player_state` | `player.state_changed`, `player.track_started`, `player.track_ended` |
| `queue` | Cola actual, contexto de origen (álbum, playlist, radio), shuffle, repeat, "a continuación" | `queue_items`, `queue_state` | `queue.changed` |
| `playlists` | Playlists manuales, orden, "me gusta" | `playlists`, `playlist_items`, `likes` | `playlist.changed`, `like.changed` |
| `history` | Registrar cada reproducción, saltos y estadísticas (top artistas, horas escuchadas) | `plays` | — |
| `radio` | Generar una cola infinita a partir de una semilla (pista, artista, género) | (lee `plays`, `catalog`) | — |
| `lyrics` | Encontrar `.lrc` junto al audio y sincronizar con la posición | `lyrics` | — |
| `jobs` | Ejecutar tareas en segundo plano con reintentos | `jobs` | `job.finished` |
| `api` | Exponer REST y WebSocket; traducir DTOs | — | Reenvía todos los eventos a WebSocket |
| `mpris` | Publicar el reproductor en D-Bus y aceptar sus comandos | — | — |
| `hw` | Leer GPIO y traducir a comandos de `playback` y `queue` | — | — |

**Regla de reproducción contada:** una reproducción se registra en `history` cuando se escucha al menos 30 segundos o el 50% de la pista, lo que ocurra primero. Antes de eso cuenta como salto.

## Modelo de datos

Un solo archivo SQLite (`~/.local/share/arpegio/arpegio.db`) en modo WAL; IDs como texto ULID para que sean ordenables y estables entre reescaneos.

Separación clave: `files` es el archivo físico y `tracks` es la canción. Si un archivo se mueve, su hash no cambia y la pista conserva historial, likes y playlists.

```sql
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;

-- Biblioteca física
CREATE TABLE library_roots (
  id          TEXT PRIMARY KEY,
  path        TEXT NOT NULL UNIQUE,
  enabled     INTEGER NOT NULL DEFAULT 1,
  last_scan   TEXT
);

CREATE TABLE files (
  id          TEXT PRIMARY KEY,
  root_id     TEXT NOT NULL REFERENCES library_roots(id),
  rel_path    TEXT NOT NULL,
  content_hash TEXT NOT NULL,          -- xxhash64 del audio
  size_bytes  INTEGER NOT NULL,
  mtime       TEXT NOT NULL,
  format      TEXT NOT NULL,           -- flac, mp3, opus, m4a
  bitrate_kbps INTEGER,
  sample_rate INTEGER,
  bit_depth   INTEGER,
  channels    INTEGER,
  duration_ms INTEGER NOT NULL,
  missing     INTEGER NOT NULL DEFAULT 0,
  UNIQUE (root_id, rel_path)
);
CREATE INDEX idx_files_hash ON files(content_hash);

CREATE TABLE artwork (
  id          TEXT PRIMARY KEY,
  hash        TEXT NOT NULL UNIQUE,
  cache_path  TEXT NOT NULL,           -- miniaturas en ~/.cache/arpegio/art
  width       INTEGER,
  height      INTEGER
);

-- Catálogo musical
CREATE TABLE artists (
  id          TEXT PRIMARY KEY,
  name        TEXT NOT NULL,
  sort_name   TEXT NOT NULL,
  mbid        TEXT,
  UNIQUE (sort_name)
);

CREATE TABLE albums (
  id          TEXT PRIMARY KEY,
  title       TEXT NOT NULL,
  album_artist_id TEXT REFERENCES artists(id),
  year        INTEGER,
  album_type  TEXT,                    -- album, ep, single, compilation
  artwork_id  TEXT REFERENCES artwork(id),
  mbid        TEXT
);

CREATE TABLE tracks (
  id          TEXT PRIMARY KEY,
  file_id     TEXT NOT NULL UNIQUE REFERENCES files(id),
  album_id    TEXT REFERENCES albums(id),
  title       TEXT NOT NULL,
  disc_no     INTEGER,
  track_no    INTEGER,
  duration_ms INTEGER NOT NULL,
  replaygain_track_db REAL,
  replaygain_album_db REAL,
  bpm         INTEGER,
  added_at    TEXT NOT NULL,
  overrides   TEXT                     -- JSON con metadatos editados a mano
);

CREATE TABLE track_artists (
  track_id    TEXT NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
  artist_id   TEXT NOT NULL REFERENCES artists(id),
  role        TEXT NOT NULL DEFAULT 'main', -- main, featured, composer
  position    INTEGER NOT NULL,
  PRIMARY KEY (track_id, artist_id, role)
);

CREATE TABLE genres (
  id          TEXT PRIMARY KEY,
  name        TEXT NOT NULL UNIQUE
);

CREATE TABLE track_genres (
  track_id    TEXT NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
  genre_id    TEXT NOT NULL REFERENCES genres(id),
  PRIMARY KEY (track_id, genre_id)
);

CREATE VIRTUAL TABLE search_index USING fts5(
  kind, ref_id UNINDEXED, title, artist, album,
  tokenize = 'unicode61 remove_diacritics 2'
);

-- Colecciones del usuario
CREATE TABLE playlists (
  id          TEXT PRIMARY KEY,
  name        TEXT NOT NULL,
  description TEXT,
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL
);

CREATE TABLE playlist_items (
  playlist_id TEXT NOT NULL REFERENCES playlists(id) ON DELETE CASCADE,
  position    REAL NOT NULL,           -- REAL permite reordenar sin renumerar
  track_id    TEXT NOT NULL REFERENCES tracks(id),
  added_at    TEXT NOT NULL,
  PRIMARY KEY (playlist_id, position)
);

CREATE TABLE likes (
  target_kind TEXT NOT NULL,           -- track, album, artist
  target_id   TEXT NOT NULL,
  liked_at    TEXT NOT NULL,
  PRIMARY KEY (target_kind, target_id)
);

-- Reproducción
CREATE TABLE queue_items (
  position    REAL PRIMARY KEY,
  track_id    TEXT NOT NULL REFERENCES tracks(id),
  source      TEXT NOT NULL,           -- user, album, playlist, radio
  source_id   TEXT,
  priority    INTEGER NOT NULL DEFAULT 0 -- 1 = "a continuación"
);

CREATE TABLE queue_state (
  id          INTEGER PRIMARY KEY CHECK (id = 1),
  current_pos REAL,
  shuffle     INTEGER NOT NULL DEFAULT 0,
  repeat_mode TEXT NOT NULL DEFAULT 'off', -- off, all, one
  shuffle_seed INTEGER
);

CREATE TABLE player_state (
  id          INTEGER PRIMARY KEY CHECK (id = 1),
  track_id    TEXT,
  position_ms INTEGER NOT NULL DEFAULT 0,
  volume      INTEGER NOT NULL DEFAULT 70,
  output      TEXT,
  updated_at  TEXT NOT NULL
);

CREATE TABLE plays (
  id          TEXT PRIMARY KEY,
  track_id    TEXT NOT NULL REFERENCES tracks(id),
  started_at  TEXT NOT NULL,
  listened_ms INTEGER NOT NULL,
  completed   INTEGER NOT NULL,        -- 1 = cumplió la regla de reproducción contada
  skipped     INTEGER NOT NULL,
  source      TEXT NOT NULL
);
CREATE INDEX idx_plays_track ON plays(track_id, started_at);

CREATE TABLE lyrics (
  track_id    TEXT PRIMARY KEY REFERENCES tracks(id) ON DELETE CASCADE,
  synced      INTEGER NOT NULL,
  body        TEXT NOT NULL
);

-- Infraestructura
CREATE TABLE jobs (
  id          TEXT PRIMARY KEY,
  kind        TEXT NOT NULL,           -- ingest_file, extract_art, rebuild_fts
  payload     TEXT NOT NULL,           -- JSON
  status      TEXT NOT NULL,           -- pending, running, done, failed
  attempts    INTEGER NOT NULL DEFAULT 0,
  run_after   TEXT NOT NULL,
  last_error  TEXT
);
CREATE INDEX idx_jobs_ready ON jobs(status, run_after);
```

Fechas en ISO 8601 UTC como `TEXT`. La cola y el estado del reproductor se persisten para retomar exactamente donde se quedó tras reiniciar el cyberdeck.

## API

REST con JSON bajo `/api/v1`, más un WebSocket de eventos; escucha en `127.0.0.1:7600` y en `$XDG_RUNTIME_DIR/arpegio.sock`.

Convenciones: paginación por cursor (`?limit=50&cursor=…`), errores como `{"error":{"code":"not_found","message":"…"}}`, IDs ULID, duraciones en milisegundos.

### Catálogo y búsqueda

| Método | Ruta | Descripción |
| --- | --- | --- |
| GET | `/search?q=&types=track,album,artist` | Búsqueda FTS5 agrupada por tipo |
| GET | `/artists` · `/artists/{id}` | Lista y detalle (álbumes, pistas populares) |
| GET | `/albums` · `/albums/{id}` | Lista y detalle con pistas |
| GET | `/tracks` · `/tracks/{id}` | Lista con filtros y detalle |
| PATCH | `/tracks/{id}` | Editar metadatos (se guardan en `overrides`) |
| GET | `/genres` · `/genres/{id}/tracks` | Géneros y sus pistas |
| GET | `/artwork/{id}?size=300` | Portada redimensionada |
| GET | `/tracks/{id}/stream` | Archivo original con soporte de Range (para clientes que reproducen por su cuenta) |
| GET | `/tracks/{id}/lyrics` | Letra, sincronizada si existe |

### Biblioteca

| Método | Ruta | Descripción |
| --- | --- | --- |
| GET | `/library/roots` | Carpetas vigiladas |
| POST | `/library/roots` | Agregar carpeta |
| DELETE | `/library/roots/{id}` | Dejar de vigilar una carpeta |
| POST | `/library/scan` | Reescaneo completo o incremental |
| POST | `/library/import` | Importar archivos subidos (multipart) a la carpeta principal |
| GET | `/library/status` | Progreso de escaneo y jobs pendientes |

### Reproducción y cola

| Método | Ruta | Descripción |
| --- | --- | --- |
| GET | `/player` | Estado completo: pista, posición, volumen, shuffle, repeat |
| POST | `/player/play` | Reanudar, o reproducir un contexto `{"context":"album:ID","start":"track:ID"}` |
| POST | `/player/pause` · `/player/next` · `/player/previous` | Controles básicos |
| PUT | `/player/position` | Seek `{"ms":…}` |
| PUT | `/player/volume` | `{"level":0-100}` |
| PUT | `/player/shuffle` · `/player/repeat` | Modos |
| GET | `/queue` | Cola actual y lo que sigue |
| POST | `/queue` | Agregar al final o "a continuación" (`{"next":true}`) |
| PATCH | `/queue/{position}` | Mover un elemento |
| DELETE | `/queue/{position}` · `/queue` | Quitar uno o limpiar |
| POST | `/radio` | Iniciar radio desde `{"seed":"track:ID"}` |

### Colecciones y estadísticas

| Método | Ruta | Descripción |
| --- | --- | --- |
| GET/POST | `/playlists` | Listar y crear |
| GET/PATCH/DELETE | `/playlists/{id}` | Detalle, renombrar, borrar |
| POST/PATCH/DELETE | `/playlists/{id}/items` | Agregar, reordenar, quitar pistas |
| PUT/DELETE | `/likes/{kind}/{id}` | Dar o quitar "me gusta" |
| GET | `/likes?kind=track` | Biblioteca de favoritos |
| GET | `/history?limit=` | Reproducciones recientes |
| GET | `/stats?range=30d` | Top pistas, artistas, géneros, tiempo escuchado |
| GET | `/home` | Vista tipo inicio: recientes, más escuchados, agregados recientemente |

### WebSocket `/api/v1/events`

Cada mensaje es `{"type":"…","data":{…},"at":"…"}`. Al conectar, el servidor manda un `snapshot` con el estado completo.

| Evento | Cuándo |
| --- | --- |
| `snapshot` | Al conectar |
| `player.state_changed` | Play, pausa, volumen, modos |
| `player.position` | Cada 1 s durante la reproducción |
| `player.track_started` | Inicio de cada pista |
| `queue.changed` | Cualquier cambio de cola |
| `library.scan_progress` | Durante escaneos |
| `catalog.updated` | Pistas nuevas o editadas |
| `playlist.changed` · `like.changed` | Cambios en colecciones |

## Flujos clave

Tres flujos concentran casi toda la lógica: ingesta, reproducción de un contexto y fin de pista.

### Ingesta de un archivo

1. `fsnotify` detecta un archivo nuevo o modificado en una carpeta vigilada (o llega por `POST /library/import`, o por un escaneo).
2. `library` espera 2 s sin cambios de tamaño (el archivo terminó de copiarse) y crea un job `ingest_file`.
3. El worker calcula el hash del contenido.
   - Si el hash ya existe en `files` con otra ruta: es un archivo movido; se actualiza la ruta y termina.
   - Si el hash ya existe en la misma ruta: no hay cambios; termina.
4. `metadata` lee tags y propiedades técnicas; `ffprobe` solo si `tag` no da duración o bitrate.
5. `catalog` resuelve o crea artista, álbum y géneros por `sort_name` normalizado, y crea la pista.
6. Se extrae la portada (embebida, o `cover.jpg`/`folder.jpg` en la carpeta), se guarda por hash y se generan miniaturas de 96, 300 y 600 px.
7. Se busca un `.lrc` con el mismo nombre del archivo.
8. Se actualiza `search_index` y se emite `catalog.updated`.

Un archivo borrado se marca `missing = 1` en lugar de eliminarse, para no perder historial si vuelve.

### Reproducir un contexto

1. El cliente envía `POST /player/play` con un contexto (álbum, playlist, artista, radio) y una pista inicial.
2. `queue` reemplaza la cola de contexto conservando los elementos "a continuación" agregados por el usuario.
3. Si shuffle está activo, se baraja con una semilla guardada para que el orden sea reproducible tras reiniciar.
4. `playback` manda a mpv `loadfile` de la pista actual y precarga la siguiente para gapless.
5. Se emiten `queue.changed` y `player.track_started`; MPRIS actualiza sus metadatos.

### Fin de pista

1. mpv avisa por IPC el fin del archivo.
2. `history` registra la reproducción con `listened_ms` y aplica la regla de reproducción contada.
3. `queue` avanza según `repeat_mode`; si la cola viene de radio y quedan menos de 5 pistas, `radio` agrega 10 más.
4. Si no hay siguiente pista, el reproductor se detiene y se guarda el estado.

### Radio (versión 1, sin aprendizaje automático)

La radio puntúa candidatos con reglas simples y explicables: mismo artista (bajo peso, para no repetir), artistas que suelen escucharse en la misma sesión según `plays`, mismo género, década cercana y BPM similar. Excluye pistas escuchadas en las últimas 2 horas y pistas saltadas con frecuencia.

## Configuración y despliegue

Arpegio se instala como un binario y un servicio de usuario de systemd; sigue las rutas XDG y no necesita root.

| Ruta | Contenido |
| --- | --- |
| `~/.config/arpegio/config.toml` | Configuración |
| `~/.local/share/arpegio/arpegio.db` | Base de datos SQLite |
| `~/.cache/arpegio/art/` | Miniaturas de portadas |
| `~/.local/state/arpegio/arpegiod.log` | Logs |
| `$XDG_RUNTIME_DIR/arpegio.sock` | Socket de la API |
| `$XDG_RUNTIME_DIR/arpegio-mpv.sock` | Socket IPC de mpv |

```toml
# config.toml
[library]
roots = ["/media/music"]
import_dir = "/media/music/_importados"
watch = true

[api]
listen = "127.0.0.1:7600"
socket = true

[playback]
audio_output = "pipewire"
gapless = true
replaygain = "album"      # off, track, album
crossfade_seconds = 0

[history]
min_listen_seconds = 30
min_listen_percent = 50

[enrichment]
musicbrainz = false

[hardware]
gpio = false
```

### Raspberry Pi

- Sistema: Raspberry Pi OS Lite de 64 bits (Bookworm o posterior) con PipeWire.
- Dependencias del sistema: `mpv`, `ffmpeg` (para `ffprobe`).
- Almacenamiento de la música: SSD por USB 3 o microSD de alta resistencia (high endurance).
- Build cruzado desde macOS: `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o arpegiod ./cmd/arpegiod`.
- El daemon lanza y supervisa su propio proceso de mpv (`--idle --no-video --input-ipc-server=…`) y lo reinicia si muere.

```ini
# ~/.config/systemd/user/arpegiod.service
[Unit]
Description=Arpegio music daemon
After=pipewire.service

[Service]
ExecStart=%h/.local/bin/arpegiod
Restart=on-failure
Nice=5

[Install]
WantedBy=default.target
```

Activar con `systemctl --user enable --now arpegiod` y `loginctl enable-linger` para que arranque sin sesión gráfica.

## Convenciones de código

Reglas para que cualquier sesión de Claude escriba código coherente con el resto del proyecto.

- **Idioma:** código, nombres y comentarios en inglés; documentación y mensajes de commit en español.
- **Formato:** `gofmt` y `goimports`; `golangci-lint` con la configuración del repo antes de cada commit.
- **Paquetes:** cada dominio expone un `Service` con constructor `New(deps)`; las dependencias se pasan como interfaces pequeñas definidas por quien las usa.
- **Contexto:** toda función que toque I/O recibe `ctx context.Context` como primer parámetro.
- **Errores:** se envuelven con `fmt.Errorf("catalog: get track %s: %w", id, err)`; errores de dominio como variables `ErrNotFound`, `ErrInvalid`, mapeados a HTTP solo en `api`.
- **SQL:** solo en `internal/store/queries/*.sql`; nada de SQL armado con strings en Go. Escrituras de varias tablas dentro de una transacción.
- **SQLite:** una conexión de escritura y un pool de lectura; `busy_timeout = 5000`.
- **Concurrencia:** goroutines siempre con dueño y cancelación por contexto; nada de goroutines sueltas.
- **Logs:** `slog` con atributos (`slog.String("track_id", id)`), sin `fmt.Println`.
- **DTOs:** los handlers no devuelven structs de `sqlc` directamente; se mapean a tipos de `api`.
- **Pruebas:** tablas de casos con `testing` estándar; SQLite en memoria para pruebas de dominio; audios de prueba en `testdata/`. Un fake de mpv para probar `playback` sin audio.
- **Commits:** Conventional Commits (`feat(queue): …`, `fix(library): …`).
- **API:** cualquier cambio de endpoint actualiza primero `api/openapi.yaml`.

## Roadmap por fases

Cada fase termina con algo usable en el cyberdeck; no se empieza una fase sin cerrar la anterior.

### Fase 1 — Esqueleto y biblioteca

- [ ] Repo, `go.mod`, Makefile, `sqlc.yaml`, configuración de lint
- [ ] `config` y `store` con migraciones iniciales
- [ ] `library` + `metadata`: escaneo completo de una carpeta
- [ ] `catalog`: artistas, álbumes, pistas y búsqueda FTS5
- [ ] Endpoints de catálogo y búsqueda
- [ ] CLI: `arpegio scan`, `arpegio search`

### Fase 2 — Reproducción

- [ ] `playback` con mpv por IPC y supervisión del proceso
- [ ] `queue` con shuffle, repeat y "a continuación"
- [ ] Persistencia del estado al reiniciar
- [ ] WebSocket de eventos
- [ ] CLI: `arpegio play`, `pause`, `next`, `queue`

### Fase 3 — TUI y colecciones

- [ ] TUI con Bubble Tea: biblioteca, búsqueda, cola, "reproduciendo ahora"
- [ ] `playlists` y `likes`
- [ ] `history` con la regla de reproducción contada
- [ ] Portadas en la TUI (protocolo de imágenes de la terminal, si lo soporta)

### Fase 4 — Experiencia tipo Spotify

- [ ] Vigilancia de carpetas con `fsnotify` e importación por API
- [ ] `radio` basada en reglas
- [ ] `/stats` y `/home`
- [ ] Letras `.lrc` sincronizadas

### Fase 5 — Integración con el cyberdeck

- [ ] MPRIS por D-Bus
- [ ] GPIO: botones y encoder rotatorio
- [ ] Servicio systemd e instalación en la Pi
- [ ] Medición de RAM, CPU y consumo en reposo

### Después

Frontend definitivo (web local o táctil), enriquecimiento opcional con MusicBrainz y edición de metadatos desde la TUI.
