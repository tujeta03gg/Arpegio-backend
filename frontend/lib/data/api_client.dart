import 'dart:convert';

import 'package:http/http.dart' as http;

import 'models.dart';

/// REST error `{"error":{"code","message"}}`, or a transport failure (code `unavailable`).
class ApiException implements Exception {
  const ApiException(this.code, this.message, [this.status = 0]);
  final String code;
  final String message;
  final int status;
  bool get isNotFound => code == 'not_found';
  @override
  String toString() => 'ApiException($code, $message)';
}

/// Single HTTP client against `/api/v1`, 5 s timeout.
class ApiClient {
  ApiClient(this.baseUrl, {http.Client? client})
    : _http = client ?? http.Client();
  final String baseUrl; // e.g. http://127.0.0.1:7600
  final http.Client _http;

  String get apiBase => '$baseUrl/api/v1';
  String get eventsUrl => '${baseUrl.replaceFirst('http', 'ws')}/api/v1/events';
  String artworkUrl(String id, int size) => '$apiBase/artwork/$id?size=$size';

  Future<Object?> _send(
    String method,
    String path, {
    Json? query,
    Object? body,
  }) async {
    var uri = Uri.parse('$apiBase$path');
    if (query != null) {
      uri = uri.replace(
        queryParameters: {for (final e in query.entries) e.key: '${e.value}'},
      );
    }
    final req = http.Request(method, uri);
    if (body != null) {
      req.headers['content-type'] = 'application/json';
      req.body = jsonEncode(body);
    }
    http.Response res;
    try {
      res = await http.Response.fromStream(
        await _http.send(req).timeout(const Duration(seconds: 5)),
      );
    } on Exception catch (e) {
      throw ApiException('unavailable', '$e');
    }
    if (res.statusCode >= 400) {
      try {
        final e =
            (jsonDecode(utf8.decode(res.bodyBytes)) as Json)['error'] as Json;
        throw ApiException(
          e['code'] as String? ?? 'internal',
          e['message'] as String? ?? '',
          res.statusCode,
        );
      } on FormatException {
        throw ApiException('internal', res.reasonPhrase ?? '', res.statusCode);
      }
    }
    if (res.bodyBytes.isEmpty) return null;
    return jsonDecode(utf8.decode(res.bodyBytes));
  }

  Future<Json> _obj(String m, String p, {Json? query, Object? body}) async =>
      (await _send(m, p, query: query, body: body) ?? <String, dynamic>{})
          as Json;

  // Catalog
  Future<Page<Track>> tracks({String cursor = '', int limit = 50}) async =>
      _page(
        await _obj('GET', '/tracks', query: _q(cursor, limit)),
        Track.fromJson,
      );
  Future<Page<Album>> albums({String cursor = '', int limit = 50}) async =>
      _page(
        await _obj('GET', '/albums', query: _q(cursor, limit)),
        Album.fromJson,
      );
  Future<Page<Artist>> artists({String cursor = '', int limit = 50}) async =>
      _page(
        await _obj('GET', '/artists', query: _q(cursor, limit)),
        Artist.fromJson,
      );
  Future<Track> track(String id) async =>
      Track.fromJson(await _obj('GET', '/tracks/$id'));
  Future<Album> album(String id) async =>
      Album.fromJson(await _obj('GET', '/albums/$id'));
  Future<Artist> artist(String id) async =>
      Artist.fromJson(await _obj('GET', '/artists/$id'));
  Future<SearchResult> search(
    String q, {
    String types = 'track,album,artist',
  }) async => SearchResult.fromJson(
    await _obj('GET', '/search', query: {'q': q, 'types': types, 'limit': 30}),
  );
  Future<Lyrics> lyrics(String trackId) async =>
      Lyrics.fromJson(await _obj('GET', '/tracks/$trackId/lyrics'));
  Future<Home> home() async => Home.fromJson(await _obj('GET', '/home'));

  // Player
  Future<PlayerState> player() async =>
      PlayerState.fromJson(await _obj('GET', '/player'));
  Future<void> play({String? context, String? start}) => _send(
    'POST',
    '/player/play',
    body: {'context': ?context, 'start': ?start},
  );
  Future<void> pause() => _send('POST', '/player/pause');
  Future<void> next() => _send('POST', '/player/next');
  Future<void> previous() => _send('POST', '/player/previous');
  Future<void> seek(int ms) =>
      _send('PUT', '/player/position', body: {'ms': ms});
  Future<void> setVolume(int level) =>
      _send('PUT', '/player/volume', body: {'level': level});
  Future<void> setShuffle(bool on) =>
      _send('PUT', '/player/shuffle', body: {'enabled': on});
  Future<void> setRepeat(String mode) =>
      _send('PUT', '/player/repeat', body: {'mode': mode});
  Future<void> radio(String seed) =>
      _send('POST', '/radio', body: {'seed': seed});

  // Queue
  Future<QueueState> queue() async =>
      QueueState.fromJson(await _obj('GET', '/queue'));
  Future<void> queueAdd(List<String> trackIds, {bool next = false}) =>
      _send('POST', '/queue', body: {'track_ids': trackIds, 'next': next});
  Future<void> queueMove(double position, int index) =>
      _send('PATCH', '/queue/${_pos(position)}', body: {'index': index});
  Future<void> queueRemove(double position) =>
      _send('DELETE', '/queue/${_pos(position)}');
  Future<void> queueClear() => _send('DELETE', '/queue');

  // Playlists and likes
  Future<List<Playlist>> playlists() async =>
      _items(await _obj('GET', '/playlists')).map(Playlist.fromJson).toList();
  Future<Playlist> playlist(String id) async =>
      Playlist.fromJson(await _obj('GET', '/playlists/$id'));
  Future<Playlist> createPlaylist(String name) async =>
      Playlist.fromJson(await _obj('POST', '/playlists', body: {'name': name}));
  Future<void> renamePlaylist(String id, String name) =>
      _send('PATCH', '/playlists/$id', body: {'name': name});
  Future<void> deletePlaylist(String id) => _send('DELETE', '/playlists/$id');
  Future<void> playlistAdd(String id, List<String> trackIds) =>
      _send('POST', '/playlists/$id/items', body: {'track_ids': trackIds});
  Future<void> playlistMove(String id, double position, int index) => _send(
    'PATCH',
    '/playlists/$id/items',
    body: {'position': position, 'index': index},
  );
  Future<void> playlistRemove(String id, double position) =>
      _send('DELETE', '/playlists/$id/items', body: {'position': position});
  Future<List<Track>> likedTracks() async =>
      _items(await _obj('GET', '/likes', query: {'kind': 'track'}))
          .map(Track.fromJson)
          .toList();
  Future<void> like(String trackId) => _send('PUT', '/likes/track/$trackId');
  Future<void> unlike(String trackId) =>
      _send('DELETE', '/likes/track/$trackId');

  // Library
  Future<List<LibraryRoot>> roots() async =>
      _items(await _obj('GET', '/library/roots'))
          .map(LibraryRoot.fromJson)
          .toList();
  Future<void> addRoot(String path) =>
      _send('POST', '/library/roots', body: {'path': path});
  Future<void> removeRoot(String id) => _send('DELETE', '/library/roots/$id');
  Future<void> scan() =>
      _send('POST', '/library/scan', body: <String, dynamic>{});
  Future<LibraryStatus> libraryStatus() async =>
      LibraryStatus.fromJson(await _obj('GET', '/library/status'));

  // Helpers
  static Json _q(String cursor, int limit) => {
    'limit': limit,
    if (cursor.isNotEmpty) 'cursor': cursor,
  };
  // Queue/playlist positions are float64; whole numbers go without ".0".
  static String _pos(double p) =>
      p == p.roundToDouble() ? p.toInt().toString() : p.toString();
  static List<Json> _items(Json j) => [
    for (final e in (j['items'] as List? ?? const [])) e as Json,
  ];
  static Page<T> _page<T>(Json j, T Function(Json) f) => Page([
    for (final e in _items(j)) f(e),
  ], j['next_cursor'] as String? ?? '');

  void close() => _http.close();
}
