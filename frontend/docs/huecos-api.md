# Huecos entre el documento del frontend y la API real

Detectados al leer `api/openapi.yaml` y `internal/api/*.go` del backend. Según el documento (sección 12), no se inventan endpoints: lo que falta se propone al backend.

Estado tras integrar el backend nuevo (campos y endpoints agregados, todo retrocompatible):

| # | Hueco | Estado | Cómo lo usa la app |
| --- | --- | --- | --- |
| 1 | `TrackDTO` sin `artwork_id` | Resuelto | `Artwork(artworkId: track.artworkId)`; ya no se pide `GET /albums/{id}` por fila. |
| 2 | Listas paginadas sin total | Resuelto | `total` en `/tracks`, `/albums`, `/artists`; la cabecera de Biblioteca ya no lleva `+`. |
| 3 | Sin códec, bitrate, frecuencia ni profundidad | Resuelto | `format`, `bitrate_kbps`, `sample_rate`, `bit_depth`; línea "ÁLBUM · FLAC · 44.1 kHz · 16 bit" en Reproduciendo (`Track.techLine`). |
| 4 | Sin indicador de archivo faltante | Resuelto | `missing`: fila en `inkDim`, "Archivo no encontrado", sin reproducción al tocar. |
| 5 | `/search` sin `album_id` ni duración | Resuelto | Las filas de Buscar son `TrackRow` completas (portada y duración). |
| 6 | `/home` solo devolvía pistas | Resuelto | `recent_albums` y `playlists`: estante "Álbumes recientes" y "Tus playlists" salen de `/home`. |
| 7 | No se podía saltar a un elemento de la cola | Resuelto | `POST /queue/{position}/play`; tocar una fila de "A continuación" la reproduce. |
| 8 | Sin versión del servidor | Resuelto | `GET /version`; se muestra en Ajustes → Servidor. |

Además:

- `allow_origin` y `web_dir` en `[api]` del daemon permiten servir la app web desde el propio daemon (`http://127.0.0.1:7600/`), así que el proxy de desarrollo se eliminó.
- `queue.changed` se atiende siempre con `GET /queue` (el documento pide marcarla como desactualizada si la pantalla no está visible); es una petición local barata y evita estados viejos.
- Reproducción en Windows sin WSL (named pipe a `mpv`): pendiente de probar con `mpv.exe` real.
