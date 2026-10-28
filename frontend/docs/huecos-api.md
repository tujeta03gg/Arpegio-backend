# Huecos entre el documento del frontend y la API real

Detectados al leer `api/openapi.yaml` y `internal/api/*.go` del backend. Según el documento (sección 12), no se inventaron endpoints: aquí quedan los cambios propuestos.

| # | Hueco | Cómo se resolvió en la app | Cambio propuesto al backend |
| --- | --- | --- | --- |
| 1 | `TrackDTO` no trae `artwork_id` (solo `album_id`), y `/artwork/{id}` espera el id de la carátula. | `artworkIdProvider(albumId)` pide `GET /albums/{id}` una vez por álbum y lo cachea. | Agregar `artwork_id` a `TrackDTO`. |
| 2 | Las listas paginadas no devuelven el total. | La cabecera de Biblioteca muestra lo cargado y un `+` si hay más páginas. | Agregar `total` a `Paged[T]`. |
| 3 | No hay códec, bitrate, frecuencia ni profundidad de bits en la pista. | La línea técnica de Reproduciendo muestra solo el álbum. | Agregar `format`, `bitrate`, `sample_rate`, `bit_depth` a `TrackDTO`. |
| 4 | No hay indicador de archivo faltante. | No se pinta "Archivo no encontrado". | Agregar `missing: bool` a `TrackDTO`. |
| 5 | Los resultados de `/search` no traen `album_id` ni duración. | Las filas de canciones en Buscar no muestran duración ni portada real (usan la generada). | Agregar `album_id` y `duration_ms` a `HitDTO`. |
| 6 | `/home` devuelve pistas, no álbumes. | Los estantes de Inicio son tarjetas de pista con la portada de su álbum. | — (decisión de diseño). |
| 7 | No hay endpoint para saltar a un elemento de la cola. | Tocar una fila de "A continuación" no hace nada. | `POST /queue/{position}/play`. |
| 8 | No hay versión del servidor. | Ajustes → Servidor muestra dirección y estado, sin versión. | `GET /version` o un campo en `/library/status`. |

Además, `queue.changed` se atiende siempre con `GET /queue` (el documento pide marcarla como desactualizada si la pantalla no está visible); es una petición local barata y evita estados viejos.
