# Arpegio — Solicitud del frontend (Flutter / Dart)

Oct 28, 2026 · @Cesar

> Este documento pide construir la interfaz táctil de Arpegio. Acompaña a **"Arpegio — Especificación del backend"** (la API, los eventos y el modelo de datos están definidos ahí) y al **sistema de diseño Arpegio** (tokens, componentes e iconos). Como referencia visual, abre `arpegio-prototipo.html` en un navegador: es el prototipo HTML a 800×480 que esta app debe reproducir en Flutter.

---

## 1. Qué se pide

Una app **Flutter** llamada `arpegio_ui` que funcione como el frontend definitivo de Arpegio en el cyberdeck. Es un **cliente delgado**: muestra el estado de `arpegiod` y le envía comandos. **No reproduce audio**: toda la reproducción la hace el daemon con mpv.

- Pantalla táctil de **5" a 800×480, horizontal**, en una **Raspberry Pi 4** (4 GB o más).
- Corre sin escritorio sobre **Raspberry Pi OS Lite de 64 bits**, con [flutter-pi](https://github.com/ardera/flutter-pi) (DRM/KMS, sin X11 ni Wayland).
- Se habla con `arpegiod` en `http://127.0.0.1:7600/api/v1` (REST) y `ws://127.0.0.1:7600/api/v1/events` (WebSocket).
- Estética **futurista neón** según el sistema de diseño Arpegio (sección 5).
- La música sigue sonando aunque la app se cierre o se reinicie. Al abrir, la app se sincroniza con el `snapshot` del WebSocket.

### Fuera de alcance (por ahora)

- Reproducir audio en el cliente (`/tracks/{id}/stream` no se usa).
- Edición de metadatos (`PATCH /tracks/{id}`).
- Importar archivos desde la app (`POST /library/import`).
- Varias pantallas o resoluciones. Solo 800×480, aunque los tamaños vivan en tokens para cambiarlos después.

---

## 2. Restricciones y principios

Heredan las del backend: un usuario, sin red, hardware modesto y batería.

- **El backend manda.** La app no guarda estado de reproducción propio. La verdad es `GET /player`, `GET /queue` y los eventos del WebSocket. Las acciones pueden ser optimistas (el corazón de Me gusta cambia al instante), pero se corrigen con el evento que llegue.
- **Eventos, no sondeos.** Nada de `Timer.periodic` para consultar la API. La posición llega por `player.position` cada segundo y los cambios por sus eventos.
- **Offline total.** Fuentes e iconos van empaquetados en los assets. Nada se descarga de internet.
- **Ligero para la Pi.**
  - Objetivo: **60 fps** en navegación y scroll, y menos de **120 MB de RAM** en reposo.
  - **Prohibido:** `BackdropFilter`, `ImageFilter.blur`, `ShaderMask` y `Opacity` animada sobre árboles grandes. Tampoco se animan `BoxShadow` (los brillos son estáticos).
  - Usar `RepaintBoundary` en la barra de progreso y el ecualizador, y widgets `const` siempre que se pueda.
  - Las imágenes se decodifican al tamaño en que se muestran (`cacheWidth`/`cacheHeight`) y se piden al tamaño correcto (`/artwork/{id}?size=96|300`).
  - Las listas largas usan `ListView.builder` con paginación por cursor.
- **Sin magia.** Va en línea con el backend:
  - Modelos escritos a mano (`fromJson`/`toJson`), sin `build_runner`, `freezed` ni `json_serializable`.
  - Dependencias mínimas (sección 4).

---

## 3. Plataforma y despliegue

| Tema | Decisión |
| --- | --- |
| Flutter | Stable más reciente compatible con la versión de `flutterpi_tool` que se use (verificar la tabla de compatibilidad en su README) |
| Embedder | [flutter-pi](https://github.com/ardera/flutter-pi), sin escritorio; la Pi arranca en consola |
| Build y deploy | [flutterpi_tool](https://github.com/ardera/flutterpi_tool): `flutter pub global activate flutterpi_tool` |
| Target | `linux/arm64`, CPU `pi4`, modo `--release` |
| Desarrollo diario | Flutter **macOS desktop** con la ventana fija a 800×480 y backend real o simulado (sección 9) |
| Arranque | Servicio systemd de usuario `arpegio-ui.service`, después de `arpegiod.service` |

### Notas para la Pi

- Configurar el arranque a consola, la memoria de GPU a 64 MB y el usuario en el grupo `render`, según el README de flutter-pi.
- **Escala 1:1.** El diseño está en píxeles lógicos a 800×480 y **`devicePixelRatio` debe ser 1**. Si flutter-pi calcula otro valor a partir de `--dimensions`, se ajusta para que `MediaQuery.sizeOf(context)` dé exactamente `Size(800, 480)`. En modo debug se agrega una aserción que lo verifique.
- Rotación con `-r`/`--orientation` si el panel viene en vertical de fábrica.
- Para la latencia táctil, evaluar el driver de usuario `raspberrypi-fast-ts` que recomienda el README de flutter-pi.
- Ejemplo de flujo (validar los flags con `flutterpi_tool --help`, porque cambian entre versiones):

```bash
flutterpi_tool devices add pi@arpegio.local --id=cyberdeck
flutterpi_tool run -d cyberdeck --release
# o build + copia manual
flutterpi_tool build --arch=arm64 --cpu=pi4 --release
rsync -a build/flutter_assets/ pi@arpegio.local:~/.local/share/arpegio-ui/
```

```ini
# ~/.config/systemd/user/arpegio-ui.service
[Unit]
Description=Arpegio UI (flutter-pi)
After=arpegiod.service
Wants=arpegiod.service

[Service]
ExecStart=/usr/local/bin/flutter-pi --release %h/.local/share/arpegio-ui
Restart=on-failure

[Install]
WantedBy=default.target
```

---

## 4. Stack y dependencias

| Necesidad | Paquete | Notas |
| --- | --- | --- |
| HTTP | `http` | Cliente único con base URL y timeout de 5 s |
| WebSocket | `web_socket_channel` | Reconexión propia con backoff |
| Estado | `flutter_riverpod` | Sin generación de código (`riverpod_generator` **no**) |
| Navegación | `Navigator` + `IndexedStack` propios | Cinco destinos fijos más detalle; `go_router` no hace falta |
| Iconos | `flutter_svg` | Los 22 SVG del sistema, coloreados con `ColorFilter` |
| Preferencias locales | `shared_preferences` | Solo ajustes de la UI (visualizador, reducir brillos) |
| Pruebas | `flutter_test`, `mocktail` | Goldens a 800×480 |

Cualquier dependencia extra se justifica en el PR. Quedan descartados `dio`, `get`, `provider` + `bloc` juntos, `freezed` y `json_serializable`.

### Estructura del proyecto

```text
arpegio_ui/
├── lib/
│   ├── main.dart                 # arranque, ProviderScope, aserción 800×480
│   ├── app.dart                  # ArpegioApp: tema, shell, atajos de teclado
│   ├── design/                   # sistema de diseño en Dart (sección 5)
│   │   ├── tokens.dart           # colores, espaciado, radios, tamaños, sombras
│   │   ├── typography.dart       # TextStyles
│   │   ├── theme.dart            # ThemeData
│   │   ├── chamfer.dart          # bordes de esquina cortada
│   │   └── icons.dart            # ArpegioIcon(name)
│   ├── data/
│   │   ├── api_client.dart       # REST /api/v1, errores tipados
│   │   ├── events_client.dart    # WebSocket /events, reconexión, snapshot
│   │   ├── models/               # Track, Album, Artist, Playlist, PlayerState, QueueItem… (a mano)
│   │   └── repositories/         # catalog, player, queue, playlists, library, home
│   ├── state/                    # providers de Riverpod
│   ├── widgets/                  # componentes del sistema (sección 6)
│   └── screens/                  # pantallas (sección 7)
├── assets/
│   ├── fonts/                    # ChakraPetch-400/500/600/700.woff2 → .ttf, ShareTechMono-400.ttf
│   └── icons/                    # 22 SVG del sistema
├── test/                         # unitarias, widget y golden
├── tool/fake_arpegiod.dart       # backend simulado para desarrollo (sección 9)
└── pubspec.yaml
```

---

## 5. Sistema de diseño en Flutter

La fuente de verdad es el sistema de diseño **Arpegio**. Aquí van los valores para que la implementación no dependa de abrirlo. Todo valor visual de la app sale de `lib/design/`. Están prohibidos los colores o tamaños sueltos en las pantallas.

### 5.1 Colores (un solo tema oscuro, "Neón")

| Token | Valor | Uso |
| --- | --- | --- |
| `void000` | `#07060D` | Fondo base |
| `void100` | `#0E0C1C` | Superficie: listas, riel, mini‑reproductor, barra de estado |
| `void200` | `#18153A` | Elevada: tarjetas, hojas, menús, fila presionada |
| `void300` | `#2A2560` | Líneas de 1px, pista vacía del progreso. Nunca texto |
| `ink` | `#EAF6FF` | Texto principal |
| `inkMuted` | `#9A9CC9` | Texto secundario |
| `inkDim` | `#5D5F8F` | Solo deshabilitado (no es texto legible) |
| `neonCyan` | `#00F0FF` | Acción: Play, progreso, activo, foco |
| `neonCyanWash` | `rgba(0,240,255,0.14)` | Fondo de la fila que suena y del destino activo |
| `neonMagenta` | `#FF3CDC` | Estado personal: Me gusta, aleatorio/repetir activos |
| `neonAmber` | `#FFB627` | Avisos: batería baja, indexando |
| `signalOk` | `#A6FF3D` | arpegiod conectado |
| `signalError` | `#FF5470` | Errores (siempre con icono y texto) |
| `onNeon` | `= void000` | Texto e iconos sobre relleno neón |

**Reglas:**
- El cian es para acciones y el magenta para lo que marcó el usuario.
- No se usan degradados: el efecto neón sale de los brillos sobre fondo oscuro.
- Las portadas son el único color libre de la pantalla, sin filtros ni tintes.

### 5.2 Tipografía

Las fuentes se empaquetan como assets. Las dos tienen licencia OFL.
- **Chakra Petch** (400, 500, 600 y 700) es la familia de toda la interfaz.
- **Share Tech Mono** (400) se usa solo para números que cambian.

Los woff2 del sistema de diseño se convierten a TTF, o se toman de Google Fonts y se guardan en el repo.

| Estilo | Familia | Tamaño / alto de línea | Peso | Extra | Uso |
| --- | --- | --- | --- | --- | --- |
| `display` | Chakra Petch | 40 / 44 | 700 | +0.01em | Arranque, vacíos grandes |
| `titleLg` | Chakra Petch | 28 / 32 | 600 | | Título de pantalla |
| `title` | Chakra Petch | 22 / 28 | 600 | | Canción en Reproduciendo (máx. 2 líneas) |
| `bodyLg` | Chakra Petch | 18 / 24 | 500 | | Artista en Reproduciendo |
| `body` | Chakra Petch | 16 / 22 | 500 | | Canción en listas (base) |
| `bodySm` | Chakra Petch | 14 / 20 | 400 | | Artista · álbum |
| `label` | Chakra Petch | 12 / 16 | 600 | +0.12em, MAYÚSCULAS | Secciones, chips |
| `caption` | Chakra Petch | 12 / 16 | 600 | +0.02em | Etiquetas del riel |
| `monoTime` | Share Tech Mono | 14 / 18 | 400 | +0.04em | Tiempos |
| `monoStatus` | Share Tech Mono | 12 / 16 | 400 | +0.06em | Barra de estado, formato y bitrate |

El alto de línea en Flutter se expresa como `height = lineHeight / fontSize`. `letterSpacing` va en px, así que +0.12em a 12px son 1.44. Ningún texto baja de 12px. Nada de marquesinas: los textos largos se cortan con elipsis.

### 5.3 Espaciado, radios y tamaños

| Token | Valor | | Token | Valor |
| --- | --- | --- | --- | --- |
| `space1` | 4 | | `touchMin` | 44 (área táctil mínima) |
| `space2` | 8 | | `touchPlay` | 64 (botón Play) |
| `space3` | 12 | | `iconMd` / `iconSm` | 24 / 18 |
| `space4` | 16 (margen de pantalla) | | `statusbarH` | 28 |
| `space6` | 24 | | `railW` | 72 |
| `space8` | 32 | | `miniplayerH` | 64 |
| `radiusSm` | 4 | | `rowH` | 56 |
| `radiusMd` | 8 | | `coverNow` | 296 |
| `radiusPill` | 999 | | `trackH` | 4 (cabezal 14) |
| `chamferSm` | 6 | | `chamferMd` | 12 |

### 5.4 Esquina cortada (la firma visual)

Las tarjetas, la portada grande, los paneles, los chips y los botones secundarios cortan en diagonal la **esquina superior izquierda y la inferior derecha**. Lo redondo queda solo para lo que gira o se desliza: el botón Play, las pistas de progreso y volumen y los interruptores.

```dart
// lib/design/chamfer.dart
const chamferMd = BeveledRectangleBorder(
  borderRadius: BorderRadius.only(
    topLeft: Radius.circular(12),
    bottomRight: Radius.circular(12),
  ),
);
// Para recortar imágenes: ClipPath con ShapeBorderClipper(shape: chamferMd)
```

### 5.5 Brillos (estáticos)

```dart
const glowCyan = [
  BoxShadow(color: Color(0x8C00F0FF), blurRadius: 6),
  BoxShadow(color: Color(0x4000F0FF), blurRadius: 18),
];
const glowMagenta = [
  BoxShadow(color: Color(0x8CFF3CDC), blurRadius: 6),
  BoxShadow(color: Color(0x40FF3CDC), blurRadius: 18),
];
const glowFocus = [
  BoxShadow(color: Color(0xFF00F0FF), spreadRadius: 2),
  BoxShadow(color: Color(0x7300F0FF), blurRadius: 12),
];
const lift = [BoxShadow(color: Color(0x99000000), offset: Offset(0, 8), blurRadius: 24)];
```

- Puede haber como máximo 1 o 2 elementos con brillo por pantalla: el botón Play y el cabezal del progreso.
- Con **"Reducir brillos"** activado, todos los brillos pasan a `[]`. Se resuelve en el tema (un `ThemeExtension`) para no regar condiciones por la app.

### 5.6 Iconos

Son 22 SVG propios del sistema, en rejilla de 24px, con trazo de 2px y terminaciones rectas:

`play`, `pause`, `prev`, `next`, `shuffle`, `repeat`, `heart`, `heart-fill`, `queue`, `volume`, `home`, `search`, `library`, `settings`, `back`, `more`, `plus`, `close`, `note`, `server`, `wifi`, `battery`

Los archivos vienen en tinta `#EAF6FF`. En la app se recolorean con `ColorFilter.mode(color, BlendMode.srcIn)` a través de un widget `ArpegioIcon(name, size, color)`. Por estado:

| Estado | Color |
| --- | --- |
| Reposo | `ink` |
| Activo | `neonCyan` |
| Modo encendido | `neonMagenta` |
| Deshabilitado | `inkDim` |

Para precargarlos se usa `SvgAssetLoader` o se convierten a `CustomPainter` si el perfilado lo pide.

### 5.7 Movimiento

- Las transiciones duran de **120 a 180 ms** y usan solo transformaciones u opacidad de widgets pequeños.
- Reproduciendo se abre deslizando desde abajo.
- Al presionar, un control escala a 0.96 (0.92 en los botones de icono).
- El ecualizador de la canción que suena son 4 barras con `ScaleTransition` en Y. Solo se anima mientras está reproduciendo y si el visualizador está activado.

---

## 6. Componentes (widgets)

Cada uno replica su componente del sistema de diseño. Todos llevan prueba golden.

| Widget | Descripción | Claves |
| --- | --- | --- |
| `StatusBar` | Barra de 28px: hora, estado de arpegiod, red, batería | Punto de 8px `signalOk`/`neonAmber`/`signalError` + palabra. Batería desde `/sys/class/power_supply/*/capacity` si existe; si no, se oculta. Sin botones |
| `NavRail` | Riel izquierdo de 72px con 5 destinos | Icono 24 + `caption`, área 64×60. Activo: `neonCyan` sobre `neonCyanWash` |
| `MiniPlayer` | Barra inferior de 64px (oculta en Reproduciendo) | Línea de progreso de 2px arriba, portada de 48, título y artista, Play de 44 sin brillo y Siguiente. Al tocarla abre Reproduciendo |
| `PlayButton` | Círculo de 64 `neonCyan` con icono `onNeon` de 28 y `glowCyan` | Variante pequeña de 44 sin brillo |
| `TransportControls` | aleatorio · (anterior · play · siguiente) · repetir | Modos activos en `neonMagenta` con punto de 4px debajo. Repetir‑una muestra un "1" |
| `ProgressBar` | Pista de 4px con área táctil de 44 de alto | Vacía `void300`, llena `neonCyan`, cabezal de 14 con brillo. Se puede tocar y arrastrar. Tiempos en `monoTime`. Envía `PUT /player/position` **al soltar**, no en cada movimiento |
| `TrackRow` | Fila de 56: portada de 40, título, "Artista · Álbum", duración, menú | Sonando: fondo `neonCyanWash`, título `neonCyan` y ecualizador. Presionada: `void200`. Divisor `void300` |
| `AlbumCard` | Tarjeta de 152 con portada de 136, ambas con chaflán | Para estantes horizontales (`Shelf`) |
| `Chip` | Filtro de selección única, 36 de alto, `chamferSm`, texto `label` | Elegido: relleno `neonCyan` con texto `onNeon` |
| `SearchField` | 44 de alto, `void100`, borde `void300` | Con foco el borde pasa a `neonCyan` |
| `SettingToggle` | Fila de 56 toda tocable, con interruptor de 44×24 | Encendido `neonCyan` con perilla `onNeon` |
| `Artwork` | Portada desde `/artwork/{id}?size=` | Sin carátula: portada generada por semilla (título del álbum), igual que `Arpegio.art()` del sistema, pintada con `CustomPainter` |
| `TrackMenu` | Hoja inferior con `lift` | Reproducir a continuación, Agregar a la cola, Agregar a playlist, Ir al álbum, Ir al artista, Iniciar radio, Me gusta |
| `ConnectionBanner` | Aviso cuando no hay conexión con arpegiod | Icono `server` + "Sin conexión con arpegiod. Reintentando…" en `signalError` |

---

## 7. Pantallas y su mapeo a la API

Marco fijo: `StatusBar` (28) arriba, `NavRail` (72) a la izquierda, contenido de 728×388 y `MiniPlayer` (64) abajo. En Reproduciendo el mini‑reproductor se oculta y el contenido mide 728×452.

### 7.1 Reproduciendo

- **Disposición:**
  - Izquierda: portada `coverNow` (296) con chaflán.
  - Derecha, arriba: "SONANDO AHORA" (o "EN PAUSA") con el ecualizador, el título (`title`, 2 líneas) con el corazón, el artista (`bodyLg`) y la línea técnica en `monoStatus` ("ÁLBUM · FLAC · 44.1 kHz · 16 bit").
  - Derecha, abajo: progreso y controles.
- **Datos:** `GET /player`, `GET /tracks/{id}` y `/artwork/{id}?size=300`.
- **Acciones:**

| Acción | Endpoint |
| --- | --- |
| Play/pausa | `POST /player/play` o `POST /player/pause` |
| Siguiente / anterior | `POST /player/next` · `POST /player/previous` |
| Adelantar | `PUT /player/position` |
| Aleatorio | `PUT /player/shuffle` |
| Repetir (off → all → one) | `PUT /player/repeat` |
| Me gusta | `PUT/DELETE /likes/track/{id}` |
| Volumen | `volume` abre una hoja con un slider que envía `PUT /player/volume` con un debounce de 150 ms |

- **Letras (fase 4):** un botón alterna la portada con la letra de `GET /tracks/{id}/lyrics`. Si viene sincronizada, la línea actual va en `neonCyan` y las demás en `inkMuted`.

### 7.2 Inicio

- `GET /home`: estantes de "Escuchado recientemente" y "Más escuchado", la lista "Agregadas recientemente" y "Tus playlists" (`GET /playlists` + Favoritas desde `GET /likes?kind=track`).
- El título de la pantalla cambia según la hora: "Buenos días", "Buenas tardes" o "Buenas noches".

### 7.3 Buscar

- `GET /search?q=&types=track,album,artist` con un debounce de 250 ms y búsqueda desde 2 caracteres.
- Chips para Canciones, Álbumes y Artistas.
- El estado vacío dice qué no encontró y sugiere otra búsqueda.
- El teclado puede ser físico (cyberdeck). Si no hay, se evalúa un teclado en pantalla simple en una fase posterior.

### 7.4 Biblioteca

- Chips: Canciones (`GET /tracks`), Álbumes (`GET /albums`), Artistas (`GET /artists`) y Playlists (`GET /playlists`).
- Paginación por cursor con `limit=50`; la siguiente página se carga al llegar al 80% del scroll.
- El encabezado muestra el total (`monoStatus`, p. ej. "1 408 CANCIONES").

### 7.5 Detalles

| Detalle | Endpoint | Contenido |
| --- | --- | --- |
| Álbum | `GET /albums/{id}` | Portada de 120, tipo, título, artista, número de canciones y duración, botón Reproducir, lista de pistas |
| Artista | `GET /artists/{id}` | Álbumes en estante y pistas populares |
| Playlist | `GET /playlists/{id}` | Igual que álbum; reordenar con pulsación larga y arrastre (`PATCH /playlists/{id}/items`) |

- Tocar una pista dentro de un contexto envía `POST /player/play` con `{"context":"album:ID","start":"track:ID"}` (o `playlist:`/`artist:`).
- El botón Volver regresa al destino de origen. En el riel se mantiene activo ese destino.

### 7.6 Cola

- `GET /queue`, en dos secciones: "Sonando ahora" y "A continuación · N".
- Reordenar con pulsación larga y arrastre (`PATCH /queue/{position}`).
- Quitar deslizando a la izquierda (`DELETE /queue/{position}`) y "Limpiar cola" (`DELETE /queue`) con confirmación.
- Los elementos agregados con "a continuación" llevan un chip `label` "Siguiente".

### 7.7 Ajustes

- **Pantalla** (preferencias locales):
  - Visualizador (on/off).
  - Reducir brillos (on/off).
- **Biblioteca:**
  - Carpetas vigiladas: `GET /library/roots`, agregar con `POST` y quitar con `DELETE`.
  - Estado: `GET /library/status` más el evento `library.scan_progress`.
  - "Reescanear" envía `POST /library/scan`.
- **Servidor:** dirección, estado de conexión y versión.
- **Estadísticas (fase 4):** `GET /stats?range=30d` con las pistas, artistas y horas más escuchados.

---

## 8. Estado, eventos y errores

### Flujo de datos

```
WebSocket /events ──► EventsClient ──► providers (player, queue, library…) ──► widgets
                                             ▲
REST (acciones) ── repositories ─────────────┘   (actualización optimista + corrección por evento)
```

### Eventos

- **Al conectar:** se espera el `snapshot` y se hidratan todos los providers con él. Hasta que llega, se muestra un esqueleto de la pantalla, sin spinner a pantalla completa.
- **Eventos y su efecto:**

| Evento | Qué hace la app |
| --- | --- |
| `player.state_changed` | Actualiza play/pausa, volumen y modos |
| `player.position` | Mueve la barra de progreso |
| `player.track_started` | Recarga la pista actual y su portada |
| `queue.changed` | Vuelve a pedir `GET /queue` si la pantalla Cola está visible; si no, la marca como desactualizada |
| `catalog.updated` | Invalida las listas de biblioteca y la búsqueda |
| `playlist.changed` / `like.changed` | Actualiza colecciones y corazones |
| `library.scan_progress` | Actualiza la barra de estado ("INDEXANDO 312/1 408") y Ajustes |

- **Progreso fluido (opcional):** entre dos `player.position` se puede interpolar localmente con un `Ticker` **solo en Reproduciendo y solo mientras suena**. Si mide caro en la Pi, se queda en saltos de 1 s.

### Reconexión y errores

- Si el WebSocket se cae, se reintenta con backoff de 1, 2, 5 y 10 s (tope 10 s).
- Mientras no hay conexión, se muestran `ConnectionBanner` y el punto `signalError`. Los controles se deshabilitan (`inkDim`), pero se sigue mostrando el último estado conocido.
- Los errores REST llegan como `{"error":{"code","message"}}` y se convierten en un `ApiException(code, message)`:

| Error | Qué se muestra |
| --- | --- |
| `not_found` | "Esta canción ya no está en la biblioteca" |
| Archivo `missing` | Fila en `inkDim` con aviso "Archivo no encontrado" |
| Otros | Aviso breve (hoja inferior de 3 s) con texto claro, nunca el código crudo |

- **Optimismo:** en Me gusta, aleatorio, repetir y play/pausa la UI cambia al instante. Si la petición falla, se revierte y se avisa.

---

## 9. Desarrollo sin la Pi: backend simulado

Para avanzar sin `arpegiod` corriendo:

- `tool/fake_arpegiod.dart` es un servidor `dart:io` que implementa los endpoints de este documento y el WebSocket con datos de prueba en memoria.
  - Usa los mismos álbumes inventados del prototipo HTML: Horizonte, Ruido Blanco, Señal Perdida, Ciberjardín, Kilohertz y Marea Alta.
  - Avanza la posición con un timer, solo dentro del simulador.
- La app recibe la URL por `--dart-define=ARPEGIO_API=http://127.0.0.1:7600`.
- En macOS la ventana se fija a 800×480 sin poder cambiar de tamaño (`window_manager` solo en dev, o configuración nativa del runner).

---

## 10. Entrada y accesibilidad

- **Táctil primero:** todo control mide al menos 44×44 de área.
- **Teclado** (el cyberdeck puede tenerlo):

| Tecla | Acción |
| --- | --- |
| Espacio | Play/pausa |
| ← / → | Anterior / siguiente |
| 1–6 | Reproduciendo, Inicio, Buscar, Biblioteca, Cola, Ajustes |
| Esc | Volver o cerrar la hoja |

  Los atajos se ignoran mientras un campo de texto tiene el foco. Se implementan con `Shortcuts`/`Actions`.
- **Foco visible** con `glowFocus` en todo control enfocable, para navegación con teclado o encoder.
- **Botones físicos, encoder y MPRIS:** los maneja el daemon (`hw`, `mpris`). La app solo refleja los eventos y no lee GPIO.
- **Nunca solo color:** Me gusta cambia de contorno a relleno, los modos llevan un punto, repetir‑una muestra un "1" y los errores llevan icono y texto.
- Toda la UI está en español. Los textos van centralizados en `lib/l10n/strings.dart`, sin paquete de i18n por ahora.

---

## 11. Calidad y pruebas

- `flutter analyze` sin advertencias, con `very_good_analysis` o `flutter_lints` en modo estricto.
- **Unitarias:** parseo de cada modelo con JSON de ejemplo y la reconexión de `EventsClient`.
- **Widget:** cada pantalla con el repositorio simulado.
- **Golden:** cada componente y pantalla a **800×480** con las fuentes reales cargadas, en sus estados (reposo, activo, deshabilitado y sin conexión).
- **Perfilado en la Pi** (modo `--profile`) en estas pantallas: scroll de 1 000 pistas en Biblioteca, apertura de Reproduciendo y búsqueda escribiendo. Se reportan fps, RAM y CPU en reposo con la pantalla quieta, que debe estar cerca de 0%.

---

## 12. Convenciones

Son las mismas del backend:

- Código, nombres y comentarios en **inglés**; documentación y mensajes de commit en **español**.
- Formato con `dart format`. Commits con **Conventional Commits** (`feat(now-playing): …`, `fix(queue): …`).
- **Capas:** los widgets no llaman `http` directamente (`screens → state → repositories → api_client`), y los modelos de `data/` no importan Flutter.
- Si un endpoint no existe o no basta, **no se inventa**: se documenta el hueco y se propone el cambio a `api/openapi.yaml` del backend.

---

## 13. Fases

Cada fase termina con algo usable en el cyberdeck.

### Fase A — Base y Reproduciendo
- [ ] Proyecto, `pubspec`, lints, fuentes e iconos en assets
- [ ] `lib/design/` completo (tokens, tipografía, tema, chaflán, iconos) con goldens
- [ ] `ApiClient`, `EventsClient` con snapshot y reconexión, `fake_arpegiod`
- [ ] Marco de la app (StatusBar, NavRail, MiniPlayer) y pantalla **Reproduciendo** funcional contra el simulador

### Fase B — Navegar la música
- [ ] Biblioteca con paginación, detalles de álbum, artista y playlist
- [ ] Buscar con debounce y chips
- [ ] Reproducir contextos y `TrackMenu`

### Fase C — Cola, colecciones y ajustes
- [ ] Cola con reordenar, quitar y "a continuación"
- [ ] Me gusta y playlists (crear, renombrar, agregar, reordenar)
- [ ] Ajustes: carpetas, reescaneo, visualizador y reducir brillos

### Fase D — En la Pi
- [ ] Build con `flutterpi_tool`, verificación de escala 1:1 a 800×480
- [ ] Servicio systemd `arpegio-ui`
- [ ] Perfilado y ajustes de rendimiento; reporte de fps, RAM y CPU

### Fase E — Experiencia completa (cuando el backend tenga su fase 4)
- [ ] Letras sincronizadas, radio, `/stats` e Inicio con `/home`

---

## 14. Criterios de aceptación

- [ ] En la Pi 4 con flutter-pi, la app llena exactamente 800×480 y coincide visualmente con `arpegio-prototipo.html`.
- [ ] Al cerrar y abrir la app, la música no se interrumpe y la UI muestra el estado correcto en menos de 1 s.
- [ ] Todas las acciones de reproducción, cola, likes y playlists funcionan contra `arpegiod` real.
- [ ] Sin `arpegiod`, la app muestra el aviso de conexión y se recupera sola cuando el daemon vuelve.
- [ ] El scroll de Biblioteca va a 60 fps con 1 000+ pistas y la RAM en reposo queda por debajo de 120 MB.
- [ ] Ningún color, tamaño o tipografía fuera de `lib/design/`.
- [ ] Goldens y pruebas en verde.
