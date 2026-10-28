/// All UI text (Spanish). No i18n package for now.
abstract final class S {
  static const home = 'Inicio';
  static const search = 'Buscar';
  static const library = 'Biblioteca';
  static const queue = 'Cola';
  static const settings = 'Ajustes';
  static const nowPlaying = 'Sonando ahora';
  static const paused = 'En pausa';
  static const nothingPlaying = 'No hay nada sonando';
  static const nothingPlayingHint =
      'Elige una canción de tu biblioteca para empezar.';
  static const songs = 'Canciones';
  static const albums = 'Álbumes';
  static const artists = 'Artistas';
  static const playlists = 'Playlists';
  static const play = 'Reproducir';
  static const back = 'Volver';
  static const connecting = 'CONECTANDO';
  static const connected = 'ARPEGIOD';
  static const offline = 'SIN CONEXIÓN';
  static const noConnection = 'Sin conexión con arpegiod. Reintentando…';
  static const searchHint = 'Canciones, artistas, álbumes';
  static const recentlyPlayed = 'Escuchado recientemente';
  static const mostPlayed = 'Más escuchado';
  static const recentlyAdded = 'Agregadas recientemente';
  static const yourPlaylists = 'Tus playlists';
  static const favorites = 'Favoritas';
  static const nowPlayingSection = 'Sonando ahora';
  static const clearQueue = 'Limpiar cola';
  static const clearQueueAsk = '¿Quitar todo lo que sigue en la cola?';
  static const cancel = 'Cancelar';
  static const confirm = 'Quitar';
  static const nextTag = 'Siguiente';
  static const emptyQueue = 'No hay más canciones en la cola.';
  static const screenSection = 'Pantalla';
  static const visualizer = 'Visualizador';
  static const visualizerHint = 'Ecualizador animado en la canción que suena';
  static const calm = 'Reducir brillos';
  static const calmHint = 'Quita los brillos neón para ahorrar batería y GPU';
  static const librarySection = 'Biblioteca';
  static const folders = 'Carpetas vigiladas';
  static const addFolder = 'Agregar carpeta';
  static const folderPath = 'Ruta de la carpeta';
  static const rescan = 'Reescanear';
  static const serverSection = 'Servidor';
  static const missingFile = 'Archivo no encontrado';
  static const loadMore = 'Cargando…';
  static const noResultsHint = 'Prueba con el artista o el álbum.';
  static const volume = 'Volumen';
  static const newPlaylist = 'Nueva playlist';
  static const playlistName = 'Nombre de la playlist';
  static const create = 'Crear';
  static const delete = 'Eliminar';
  static const rename = 'Renombrar';
  static const popular = 'Populares';

  // Track menu
  static const playNext = 'Reproducir a continuación';
  static const addToQueue = 'Agregar a la cola';
  static const addToPlaylist = 'Agregar a playlist';
  static const goAlbum = 'Ir al álbum';
  static const goArtist = 'Ir al artista';
  static const startRadio = 'Iniciar radio';
  static const like = 'Me gusta';
  static const unlike = 'Quitar de Me gusta';

  // Feedback
  static const added = 'Agregada a la cola';
  static const addedNext = 'Sonará a continuación';
  static const addedPlaylist = 'Agregada a la playlist';
  static const failed = 'No se pudo completar la acción';
  static const notFound = 'Esta canción ya no está en la biblioteca';
  static const unavailable = 'El reproductor no está disponible';

  static String greeting(int hour) => hour < 12
      ? 'Buenos días'
      : (hour < 20 ? 'Buenas tardes' : 'Buenas noches');
  static String songCount(int n) => n == 1 ? '1 canción' : '$n canciones';
  static String upNext(int n) => 'A continuación · $n';
  static String noResults(String q) => 'Nada con “$q”.';
  static String indexing(int done, int total) => 'INDEXANDO $done/$total';
}
