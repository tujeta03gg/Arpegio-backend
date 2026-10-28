// Hand-written models for the arpegiod API. No Flutter imports.

typedef Json = Map<String, dynamic>;

List<T> _list<T>(Object? v, T Function(Json) f) =>
    v is List ? [for (final e in v) f(e as Json)] : <T>[];

class ArtistRef {
  const ArtistRef({required this.id, required this.name});
  factory ArtistRef.fromJson(Json j) =>
      ArtistRef(id: j['id'] as String? ?? '', name: j['name'] as String? ?? '');
  final String id;
  final String name;
}

class Track {
  const Track({
    required this.id,
    required this.title,
    this.artist = '',
    this.album = '',
    this.albumId = '',
    this.trackNo = 0,
    this.durationMs = 0,
    this.artists = const [],
    this.artworkId = '',
    this.format = '',
    this.bitrateKbps = 0,
    this.sampleRate = 0,
    this.bitDepth = 0,
    this.missing = false,
  });
  factory Track.fromJson(Json j) => Track(
    id: j['id'] as String,
    title: j['title'] as String? ?? '',
    artist: j['artist'] as String? ?? '',
    album: j['album'] as String? ?? '',
    albumId: j['album_id'] as String? ?? '',
    trackNo: (j['track_no'] as num?)?.toInt() ?? 0,
    durationMs: (j['duration_ms'] as num?)?.toInt() ?? 0,
    artists: _list(j['artists'], ArtistRef.fromJson),
    artworkId: j['artwork_id'] as String? ?? '',
    format: j['format'] as String? ?? '',
    bitrateKbps: (j['bitrate_kbps'] as num?)?.toInt() ?? 0,
    sampleRate: (j['sample_rate'] as num?)?.toInt() ?? 0,
    bitDepth: (j['bit_depth'] as num?)?.toInt() ?? 0,
    missing: j['missing'] as bool? ?? false,
  );
  Json toJson() => {
    'id': id,
    'title': title,
    'artist': artist,
    'album': album,
    'album_id': albumId,
    'track_no': trackNo,
    'duration_ms': durationMs,
    'artwork_id': artworkId,
    'format': format,
    'bitrate_kbps': bitrateKbps,
    'sample_rate': sampleRate,
    'bit_depth': bitDepth,
    'missing': missing,
    'artists': [
      for (final a in artists) {'id': a.id, 'name': a.name},
    ],
  };
  final String id;
  final String title;
  final String artist;
  final String album;
  final String albumId;
  final int trackNo;
  final int durationMs;
  final List<ArtistRef> artists;
  final String artworkId; // "" = no cover
  final String format; // flac, mp3, ...
  final int bitrateKbps;
  final int sampleRate; // Hz
  final int bitDepth;
  final bool missing; // file not found

  /// "FLAC · 44.1 kHz · 16 bit", skipping what the file does not report.
  String get techLine => [
    if (format.isNotEmpty) format.toUpperCase(),
    if (sampleRate > 0)
      '${(sampleRate / 1000).toStringAsFixed(sampleRate % 1000 == 0 ? 0 : 1)} kHz',
    if (bitDepth > 0) '$bitDepth bit',
    if (bitDepth == 0 && bitrateKbps > 0) '$bitrateKbps kbps',
  ].join(' · ');

  /// Main artist id, when the API sent one.
  String? get artistId => artists.isEmpty ? null : artists.first.id;
}

class Album {
  const Album({
    required this.id,
    required this.title,
    this.artistId = '',
    this.artist = '',
    this.year = 0,
    this.artworkId = '',
    this.tracks = const [],
  });
  factory Album.fromJson(Json j) => Album(
    id: j['id'] as String,
    title: j['title'] as String? ?? '',
    artistId: j['artist_id'] as String? ?? '',
    artist: j['artist'] as String? ?? '',
    year: (j['year'] as num?)?.toInt() ?? 0,
    artworkId: j['artwork_id'] as String? ?? '',
    tracks: _list(j['tracks'], Track.fromJson),
  );
  final String id;
  final String title;
  final String artistId;
  final String artist;
  final int year;
  final String artworkId;
  final List<Track> tracks;

  int get durationMs => tracks.fold(0, (s, t) => s + t.durationMs);
}

class Artist {
  const Artist({
    required this.id,
    required this.name,
    this.albums = const [],
    this.tracks = const [],
  });
  factory Artist.fromJson(Json j) => Artist(
    id: j['id'] as String,
    name: j['name'] as String? ?? '',
    albums: _list(j['albums'], Album.fromJson),
    tracks: _list(j['tracks'], Track.fromJson),
  );
  final String id;
  final String name;
  final List<Album> albums;
  final List<Track> tracks;
}

class PlaylistItem {
  const PlaylistItem({required this.position, required this.track});
  factory PlaylistItem.fromJson(Json j) => PlaylistItem(
    position: (j['position'] as num).toDouble(),
    track: Track.fromJson(j['track'] as Json),
  );
  final double position;
  final Track track;
}

class Playlist {
  const Playlist({
    required this.id,
    required this.name,
    this.description = '',
    this.itemCount = 0,
    this.items = const [],
  });
  factory Playlist.fromJson(Json j) => Playlist(
    id: j['id'] as String,
    name: j['name'] as String? ?? '',
    description: j['description'] as String? ?? '',
    itemCount: (j['item_count'] as num?)?.toInt() ?? 0,
    items: _list(j['items'], PlaylistItem.fromJson),
  );
  final String id;
  final String name;
  final String description;
  final int itemCount;
  final List<PlaylistItem> items;
}

class PlayerState {
  const PlayerState({
    this.track,
    this.positionMs = 0,
    this.durationMs = 0,
    this.playing = false,
    this.volume = 100,
    this.shuffle = false,
    this.repeat = 'off',
  });
  factory PlayerState.fromJson(Json j) => PlayerState(
    track: j['track'] == null ? null : Track.fromJson(j['track'] as Json),
    positionMs: (j['position_ms'] as num?)?.toInt() ?? 0,
    durationMs: (j['duration_ms'] as num?)?.toInt() ?? 0,
    playing: j['playing'] as bool? ?? false,
    volume: (j['volume'] as num?)?.toInt() ?? 100,
    shuffle: j['shuffle'] as bool? ?? false,
    repeat: j['repeat'] as String? ?? 'off',
  );
  final Track? track;
  final int positionMs;
  final int durationMs;
  final bool playing;
  final int volume;
  final bool shuffle;
  final String repeat; // off | all | one

  PlayerState copyWith({
    bool? playing,
    bool? shuffle,
    String? repeat,
    int? volume,
  }) => PlayerState(
    track: track,
    positionMs: positionMs,
    durationMs: durationMs,
    playing: playing ?? this.playing,
    volume: volume ?? this.volume,
    shuffle: shuffle ?? this.shuffle,
    repeat: repeat ?? this.repeat,
  );
}

class QueueItem {
  const QueueItem({
    required this.position,
    required this.priority,
    required this.current,
    required this.track,
  });
  factory QueueItem.fromJson(Json j) => QueueItem(
    position: (j['position'] as num).toDouble(),
    priority: (j['priority'] as num?)?.toInt() ?? 0,
    current: j['current'] as bool? ?? false,
    track: Track.fromJson(j['track'] as Json),
  );
  final double position;
  final int priority; // 1 = "play next"
  final bool current;
  final Track track;
}

class QueueState {
  const QueueState({this.items = const []});
  factory QueueState.fromJson(Json j) =>
      QueueState(items: _list(j['items'], QueueItem.fromJson));
  final List<QueueItem> items;

  int get currentIndex => items.indexWhere((i) => i.current);
  QueueItem? get current => currentIndex < 0 ? null : items[currentIndex];
  List<QueueItem> get upcoming =>
      currentIndex < 0 ? items : items.sublist(currentIndex + 1);
}

class LibraryStatus {
  const LibraryStatus({
    this.scanning = false,
    this.done = 0,
    this.total = 0,
    this.pendingJobs = 0,
  });
  factory LibraryStatus.fromJson(Json j) => LibraryStatus(
    scanning: j['scanning'] as bool? ?? false,
    done: (j['done'] as num?)?.toInt() ?? 0,
    total: (j['total'] as num?)?.toInt() ?? 0,
    pendingJobs: (j['pending_jobs'] as num?)?.toInt() ?? 0,
  );
  final bool scanning;
  final int done;
  final int total;
  final int pendingJobs;
}

class LibraryRoot {
  const LibraryRoot({
    required this.id,
    required this.path,
    this.enabled = true,
  });
  factory LibraryRoot.fromJson(Json j) => LibraryRoot(
    id: j['id'] as String,
    path: j['path'] as String? ?? '',
    enabled: j['enabled'] as bool? ?? true,
  );
  final String id;
  final String path;
  final bool enabled;
}

class Home {
  const Home({
    this.recentlyPlayed = const [],
    this.mostPlayed = const [],
    this.recentlyAdded = const [],
    this.recentAlbums = const [],
    this.playlists = const [],
  });
  factory Home.fromJson(Json j) => Home(
    recentlyPlayed: _list(j['recently_played'], Track.fromJson),
    mostPlayed: _list(j['most_played'], Track.fromJson),
    recentlyAdded: _list(j['recently_added'], Track.fromJson),
    recentAlbums: _list(j['recent_albums'], Album.fromJson),
    playlists: _list(j['playlists'], Playlist.fromJson),
  );
  final List<Track> recentlyPlayed;
  final List<Track> mostPlayed;
  final List<Track> recentlyAdded;
  final List<Album> recentAlbums;
  final List<Playlist> playlists;
}

class Hit {
  const Hit({
    required this.id,
    required this.title,
    this.artist = '',
    this.album = '',
    this.albumId = '',
    this.artworkId = '',
    this.durationMs = 0,
  });
  factory Hit.fromJson(Json j) => Hit(
    id: j['id'] as String,
    title: j['title'] as String? ?? '',
    artist: j['artist'] as String? ?? '',
    album: j['album'] as String? ?? '',
    albumId: j['album_id'] as String? ?? '',
    artworkId: j['artwork_id'] as String? ?? '',
    durationMs: (j['duration_ms'] as num?)?.toInt() ?? 0,
  );
  final String id;
  final String title;
  final String artist;
  final String album;
  final String albumId;
  final String artworkId;
  final int durationMs;

  Track toTrack() => Track(
    id: id,
    title: title,
    artist: artist,
    album: album,
    albumId: albumId,
    artworkId: artworkId,
    durationMs: durationMs,
  );
}

class SearchResult {
  const SearchResult({
    this.tracks = const [],
    this.albums = const [],
    this.artists = const [],
  });
  factory SearchResult.fromJson(Json j) => SearchResult(
    tracks: _list(j['tracks'], Hit.fromJson),
    albums: _list(j['albums'], Hit.fromJson),
    artists: _list(j['artists'], Hit.fromJson),
  );
  final List<Hit> tracks;
  final List<Hit> albums;
  final List<Hit> artists;
}

class LyricLine {
  const LyricLine({this.timeMs, required this.text});
  factory LyricLine.fromJson(Json j) => LyricLine(
    timeMs: (j['time_ms'] as num?)?.toInt(),
    text: j['text'] as String? ?? '',
  );
  final int? timeMs;
  final String text;
}

class Lyrics {
  const Lyrics({this.synced = false, this.lines = const []});
  factory Lyrics.fromJson(Json j) => Lyrics(
    synced: j['synced'] as bool? ?? false,
    lines: _list(j['lines'], LyricLine.fromJson),
  );
  final bool synced;
  final List<LyricLine> lines;
}

class Page<T> {
  const Page(this.items, this.nextCursor, [this.total = 0]);
  final List<T> items;
  final String nextCursor; // "" = last page
  final int total; // whole catalog, not just this page
}

class ServerVersion {
  const ServerVersion({
    this.version = '',
    this.commit = '',
    this.os = '',
    this.arch = '',
  });
  factory ServerVersion.fromJson(Json j) => ServerVersion(
    version: j['version'] as String? ?? '',
    commit: j['commit'] as String? ?? '',
    os: j['os'] as String? ?? '',
    arch: j['arch'] as String? ?? '',
  );
  final String version;
  final String commit;
  final String os;
  final String arch;
}

/// A WebSocket frame: `{type, data, at}`.
class EventFrame {
  const EventFrame(this.type, this.data);
  factory EventFrame.fromJson(Json j) =>
      EventFrame(j['type'] as String? ?? '', j['data']);
  final String type;
  final Object? data;
}
