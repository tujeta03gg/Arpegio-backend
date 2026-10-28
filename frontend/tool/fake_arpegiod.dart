// Simulated arpegiod for development: dart run tool/fake_arpegiod.dart [port]
// Implements the REST endpoints and /events with in-memory data (same albums as the HTML prototype).
import 'dart:async';
import 'dart:convert';
import 'dart:io';

typedef J = Map<String, dynamic>;

const _albums = <(String, String, String, List<(String, int)>)>[
  (
    'horizonte',
    'Horizonte',
    'Synthwave Collective',
    [
      ('Noches de Neón', 252),
      ('Autopista 404', 221),
      ('Horizonte', 289),
      ('Luz de sodio', 198),
      ('Último tren', 247),
    ],
  ),
  (
    'ruido',
    'Ruido Blanco',
    'Los Circuitos',
    [
      ('Ciudad de cristal', 218),
      ('Frecuencia fantasma', 204),
      ('Estática', 175),
      ('Paralelo', 233),
    ],
  ),
  (
    'senal',
    'Señal Perdida',
    'Andrea Volt',
    [
      ('Señal perdida', 301),
      ('Antena', 189),
      ('Interferencia', 214),
      ('Modo avión', 166),
    ],
  ),
  (
    'ciber',
    'Ciberjardín',
    'Moho Digital',
    [
      ('Fotosíntesis', 243),
      ('Raíces de cobre', 207),
      ('Musgo en la terminal', 262),
    ],
  ),
  (
    'kilo',
    'Kilohertz',
    'Pulso 88',
    [
      ('Kilohertz', 196),
      ('Onda cuadrada', 184),
      ('Arpegio en Do', 229),
      ('Bucle infinito', 270),
    ],
  ),
  (
    'marea',
    'Marea Alta',
    'Synthwave Collective',
    [('Marea alta', 238), ('Puerto 8080', 211), ('Faro', 254)],
  ),
];

String _artistId(String name) => 'a-${name.toLowerCase().replaceAll(' ', '-')}';

final tracks = <J>[
  for (final (aid, atitle, artist, ts) in _albums)
    for (var i = 0; i < ts.length; i++)
      {
        'id': '$aid$i',
        'title': ts[i].$1,
        'artist': artist,
        'album': atitle,
        'album_id': aid,
        'track_no': i + 1,
        'duration_ms': ts[i].$2 * 1000,
        'added_at': '2026-10-01T00:00:00Z',
        'format': aid == 'marea' ? 'mp3' : 'flac',
        'sample_rate': 44100,
        if (aid != 'marea') 'bit_depth': 16,
        'bitrate_kbps': aid == 'marea' ? 320 : 900,
        // One missing file to exercise the "Archivo no encontrado" row.
        'missing': aid == 'ruido' && i == 2,
        'artists': [
          {'id': _artistId(artist), 'name': artist, 'role': 'main'},
        ],
      },
];
J track(String id) => tracks.firstWhere((t) => t['id'] == id);
J albumJson(String id, {bool withTracks = true}) {
  final a = _albums.firstWhere((a) => a.$1 == id);
  return {
    'id': a.$1,
    'title': a.$2,
    'artist_id': _artistId(a.$3),
    'artist': a.$3,
    'year': 2024,
    if (withTracks) 'tracks': tracks.where((t) => t['album_id'] == id).toList(),
  };
}

final artists = {for (final a in _albums) _artistId(a.$3): a.$3};

// Player state
var queue = <String>[
  for (var i = 0; i < 5; i++) 'horizonte$i',
  'ruido0',
  'kilo2',
  'senal0',
];
var qi = 0;
var posMs = 167000;
var playing = true;
var volume = 80;
var shuffle = false;
var repeat = 'off';
final nextFlags = <String>{};
final likes = <String>{'horizonte0', 'kilo2', 'ruido0', 'senal1'};
final playlists = <J>[
  {
    'id': 'pl1',
    'name': 'Para programar',
    'description': '',
    'tracks': <String>['kilo0', 'kilo1', 'ciber0', 'horizonte2'],
  },
  {
    'id': 'pl2',
    'name': 'Viaje nocturno',
    'description': '',
    'tracks': <String>['senal0', 'marea0', 'horizonte4'],
  },
];
final roots = <J>[
  {'id': 'r1', 'path': '/home/pi/Musica', 'enabled': true},
];
var scanning = false;
var scanDone = 0;

final sockets = <WebSocket>[];

J trackOf(String id) => track(id);
J playerJson() => {
  'track': queue.isEmpty ? null : trackOf(queue[qi]),
  'position_ms': posMs,
  'duration_ms': queue.isEmpty ? 0 : trackOf(queue[qi])['duration_ms'],
  'playing': playing,
  'volume': volume,
  'shuffle': shuffle,
  'repeat': repeat,
};
J queueJson() => {
  'items': [
    for (var i = 0; i < queue.length; i++)
      {
        'position': (i + 1).toDouble(),
        'priority': nextFlags.contains('$i:${queue[i]}') ? 1 : 0,
        'source': 'user',
        'current': i == qi,
        'track': trackOf(queue[i]),
      },
  ],
  'shuffle': shuffle,
  'repeat': repeat,
};

void emit(String type, Object? data) {
  final msg = jsonEncode({
    'type': type,
    'data': data,
    'at': DateTime.now().toUtc().toIso8601String(),
  });
  for (final s in [...sockets]) {
    s.add(msg);
  }
}

void stateChanged() => emit('player.state_changed', playerJson());

void startTrack(int i) {
  qi = i;
  posMs = 0;
  playing = true;
  emit('player.track_started', {'track_id': queue[qi]});
  stateChanged();
}

void advance() {
  if (repeat == 'one') {
    posMs = 0;
  } else if (shuffle && queue.length > 1) {
    startTrack((qi + 1 + DateTime.now().millisecond) % queue.length);
  } else if (qi < queue.length - 1) {
    startTrack(qi + 1);
  } else if (repeat == 'all') {
    startTrack(0);
  } else {
    playing = false;
    posMs = 0;
    stateChanged();
  }
}

List<String> resolve(String ctx) {
  final i = ctx.indexOf(':');
  final kind = ctx.substring(0, i), id = ctx.substring(i + 1);
  return switch (kind) {
    'track' => [id],
    'album' => [
      for (final t in tracks)
        if (t['album_id'] == id) t['id'] as String,
    ],
    'artist' => [
      for (final t in tracks)
        if ((t['artists'] as List).first['id'] == id) t['id'] as String,
    ],
    'playlist' => [
      ...playlists.firstWhere((p) => p['id'] == id)['tracks'] as List<String>,
    ],
    'likes' => likes.toList(),
    _ => <String>[],
  };
}

J playlistJson(J p, {bool items = false}) {
  final ts = p['tracks'] as List<String>;
  return {
    'id': p['id'],
    'name': p['name'],
    'description': p['description'],
    'created_at': '2026-10-01T00:00:00Z',
    'updated_at': '2026-10-01T00:00:00Z',
    'item_count': ts.length,
    if (items)
      'items': [
        for (var i = 0; i < ts.length; i++)
          {
            'position': (i + 1).toDouble(),
            'added_at': '',
            'track': trackOf(ts[i]),
          },
      ],
  };
}

J page(List<J> all, String? cursor, int limit) {
  final off = int.tryParse(cursor ?? '') ?? 0;
  final end = (off + limit).clamp(0, all.length);
  return {
    'items': all.sublist(off, end),
    'next_cursor': end >= all.length ? '' : '$end',
    'total': all.length,
  };
}

Future<void> main(List<String> args) async {
  final port = args.isEmpty ? 7600 : int.parse(args.first);
  final server = await HttpServer.bind(InternetAddress.loopbackIPv4, port);
  stdout.writeln('fake arpegiod en http://127.0.0.1:$port');

  Timer.periodic(const Duration(seconds: 1), (_) {
    if (playing && queue.isNotEmpty) {
      posMs += 1000;
      if (posMs >= (trackOf(queue[qi])['duration_ms'] as int)) {
        advance();
      } else {
        emit('player.position', {
          'position_ms': posMs,
          'duration_ms': trackOf(queue[qi])['duration_ms'],
        });
      }
    }
    if (scanning) {
      scanDone += 100;
      emit('library.scan_progress', {
        'root': 'r1',
        'done': scanDone,
        'total': 1408,
      });
      if (scanDone >= 1408) {
        scanning = false;
        emit('library.scan_finished', {'added': 0});
      }
    }
  });

  await for (final req in server) {
    unawaited(
      handle(req).catchError((Object e) {
        stderr.writeln('error: $e');
        req.response
          ..statusCode = 500
          ..close();
      }),
    );
  }
}

/// Serves the Flutter web build (build/web) so app and API share one origin.
Future<void> serveStatic(HttpRequest req) async {
  const types = {
    'html': 'text/html; charset=utf-8',
    'js': 'text/javascript',
    'json': 'application/json',
    'wasm': 'application/wasm',
    'png': 'image/png',
    'ttf': 'font/ttf',
    'otf': 'font/otf',
    'svg': 'image/svg+xml',
  };
  final rel = req.uri.path == '/' ? 'index.html' : req.uri.path.substring(1);
  var f = File('build/web/$rel');
  if (rel.contains('..') || !f.existsSync()) f = File('build/web/index.html');
  if (!f.existsSync()) {
    req.response
      ..statusCode = 404
      ..write('Falta build/web: ejecuta "flutter build web" primero.');
    return req.response.close();
  }
  final ext = f.path.split('.').last;
  req.response.headers.contentType = ContentType.parse(
    types[ext] ?? 'application/octet-stream',
  );
  await req.response.addStream(f.openRead());
  await req.response.close();
}

Future<void> handle(HttpRequest req) async {
  final res = req.response;
  res.headers
    ..set('access-control-allow-origin', '*')
    ..set('access-control-allow-methods', 'GET,POST,PUT,PATCH,DELETE,OPTIONS')
    ..set('access-control-allow-headers', 'content-type');
  if (req.method == 'OPTIONS') {
    res.statusCode = 204;
    return res.close();
  }
  if (!req.uri.path.startsWith('/api/')) return serveStatic(req);
  const base = '/api/v1';
  final path = req.uri.path.startsWith(base)
      ? req.uri.path.substring(base.length)
      : req.uri.path;
  final seg = path.split('/').where((s) => s.isNotEmpty).toList();
  final q = req.uri.queryParameters;
  final raw = await utf8.decoder.bind(req).join();
  final body = raw.isEmpty ? <String, dynamic>{} : jsonDecode(raw) as J;
  final m = req.method;

  Future<void> send(Object? data, [int status = 200]) async {
    res.statusCode = status;
    if (data != null) {
      res.headers.contentType = ContentType.json;
      res.write(jsonEncode(data));
    }
    await res.close();
  }

  Future<void> err(int status, String code, String msg) => send({
    'error': {'code': code, 'message': msg},
  }, status);

  if (seg.length == 1 && seg[0] == 'events') {
    final ws = await WebSocketTransformer.upgrade(req);
    sockets.add(ws);
    ws.add(
      jsonEncode({
        'type': 'snapshot',
        'data': {
          'player': playerJson(),
          'queue': queueJson(),
          'library': {
            'scanning': scanning,
            'done': scanDone,
            'total': 1408,
            'pending_jobs': 0,
          },
        },
        'at': DateTime.now().toUtc().toIso8601String(),
      }),
    );
    ws.listen((_) {}, onDone: () => sockets.remove(ws));
    return;
  }

  switch ((m, seg.isEmpty ? '' : seg[0], seg.length)) {
    case ('GET', 'tracks', 1):
      return send(page(tracks, q['cursor'], int.parse(q['limit'] ?? '50')));
    case ('GET', 'tracks', 2):
      final t = tracks.where((t) => t['id'] == seg[1]);
      return t.isEmpty ? err(404, 'not_found', 'track') : send(t.first);
    case ('GET', 'tracks', 3):
      return err(404, 'not_found', 'sin letra');
    case ('GET', 'albums', 1):
      return send(
        page(
          [for (final a in _albums) albumJson(a.$1, withTracks: false)],
          q['cursor'],
          int.parse(q['limit'] ?? '50'),
        ),
      );
    case ('GET', 'albums', 2):
      return _albums.any((a) => a.$1 == seg[1])
          ? send(albumJson(seg[1]))
          : err(404, 'not_found', 'album');
    case ('GET', 'artists', 1):
      return send(
        page(
          [
            for (final e in artists.entries) {'id': e.key, 'name': e.value},
          ],
          q['cursor'],
          int.parse(q['limit'] ?? '50'),
        ),
      );
    case ('GET', 'artists', 2):
      final name = artists[seg[1]];
      if (name == null) return err(404, 'not_found', 'artist');
      return send({
        'id': seg[1],
        'name': name,
        'albums': [
          for (final a in _albums.where((a) => a.$3 == name))
            albumJson(a.$1, withTracks: false),
        ],
        'tracks': [for (final t in tracks.where((t) => t['artist'] == name)) t],
      });
    case ('GET', 'search', _):
      final s = (q['q'] ?? '').toLowerCase();
      bool hit(String v) => v.toLowerCase().contains(s);
      return send({
        'tracks': [
          for (final t in tracks.where(
            (t) => hit(t['title']) || hit(t['artist']) || hit(t['album']),
          ))
            {
              'id': t['id'],
              'title': t['title'],
              'artist': t['artist'],
              'album': t['album'],
              'album_id': t['album_id'],
              'duration_ms': t['duration_ms'],
            },
        ],
        'albums': [
          for (final a in _albums.where((a) => hit(a.$2) || hit(a.$3)))
            {'id': a.$1, 'title': a.$2, 'artist': a.$3},
        ],
        'artists': [
          for (final e in artists.entries.where((e) => hit(e.value)))
            {'id': e.key, 'title': e.value},
        ],
      });
    case ('GET', 'home', _):
      return send({
        'recently_played': [
          for (final id in [
            'horizonte0',
            'ruido0',
            'senal0',
            'kilo0',
            'ciber0',
            'marea0',
          ])
            trackOf(id),
        ],
        'most_played': [
          for (final id in ['kilo2', 'horizonte2', 'senal1']) trackOf(id),
        ],
        'recently_added': [
          for (final id in ['kilo2', 'ciber1', 'senal3']) trackOf(id),
        ],
        'recent_albums': [
          for (final a in _albums.take(4)) albumJson(a.$1, withTracks: false),
        ],
        'playlists': [for (final p in playlists) playlistJson(p)],
      });
    case ('GET', 'version', _):
      return send({
        'version': 'dev-sim',
        'commit': 'fake',
        'go_version': 'dart',
        'os': 'sim',
        'arch': 'sim',
      });
    case ('POST', 'queue', 3) when seg[2] == 'play':
      final at = double.parse(seg[1]).toInt() - 1;
      if (at < 0 || at >= queue.length) return err(404, 'not_found', 'cola');
      startTrack(at);
      emit('queue.changed', null);
      return send(playerJson());
    case ('GET', 'player', 1):
      return send(playerJson());
    case ('POST', 'player', 2):
      switch (seg[1]) {
        case 'play':
          if (body['context'] != null) {
            queue = resolve(body['context'] as String);
            final start = (body['start'] as String?)?.replaceFirst(
              'track:',
              '',
            );
            qi = start == null
                ? 0
                : queue.indexOf(start).clamp(0, queue.length - 1);
            nextFlags.clear();
            posMs = 0;
            emit('queue.changed', null);
            emit('player.track_started', {'track_id': queue[qi]});
          }
          playing = true;
        case 'pause':
          playing = false;
        case 'next':
          advance();
        case 'previous':
          posMs > 3000
              ? posMs = 0
              : startTrack((qi - 1).clamp(0, queue.length - 1));
      }
      stateChanged();
      return send(playerJson());
    case ('PUT', 'player', 2):
      switch (seg[1]) {
        case 'position':
          posMs = body['ms'] as int;
        case 'volume':
          volume = body['level'] as int;
        case 'shuffle':
          shuffle = body['enabled'] as bool;
        case 'repeat':
          repeat = body['mode'] as String;
      }
      stateChanged();
      return send(playerJson());
    case ('GET', 'queue', 1):
      return send(queueJson());
    case ('POST', 'queue', 1):
      final ids = [...(body['track_ids'] as List? ?? const []).cast<String>()];
      if (body['next'] == true) {
        queue.insertAll(qi + 1, ids);
        nextFlags.add('${qi + 1}:${ids.first}');
      } else {
        queue.addAll(ids);
      }
      emit('queue.changed', null);
      return send(queueJson());
    case ('DELETE', 'queue', 1):
      queue = [queue[qi]];
      qi = 0;
      nextFlags.clear();
      emit('queue.changed', null);
      return send(queueJson());
    case ('PATCH', 'queue', 2):
      final from = double.parse(seg[1]).toInt() - 1;
      final id = queue.removeAt(from);
      final to = body['index'] as int;
      queue.insert(to, id);
      if (from == qi) {
        qi = to;
      } else if (from < qi && to >= qi) {
        qi--;
      } else if (from > qi && to <= qi) {
        qi++;
      }
      emit('queue.changed', null);
      return send(queueJson());
    case ('DELETE', 'queue', 2):
      final at = double.parse(seg[1]).toInt() - 1;
      if (at == qi) {
        return err(400, 'invalid', 'no se puede quitar la que suena');
      }
      queue.removeAt(at);
      if (at < qi) qi--;
      emit('queue.changed', null);
      return send(queueJson());
    case ('GET', 'playlists', 1):
      return send({
        'items': [for (final p in playlists) playlistJson(p)],
      });
    case ('POST', 'playlists', 1):
      final p = {
        'id': 'pl${playlists.length + 1}',
        'name': body['name'],
        'description': '',
        'tracks': <String>[],
      };
      playlists.add(p);
      emit('playlist.changed', {'id': p['id']});
      return send(playlistJson(p), 201);
    case (_, 'playlists', _) when seg.length >= 2:
      final idx = playlists.indexWhere((p) => p['id'] == seg[1]);
      if (idx < 0) return err(404, 'not_found', 'playlist');
      final p = playlists[idx];
      final ts = p['tracks'] as List<String>;
      if (seg.length == 2) {
        if (m == 'GET') return send(playlistJson(p, items: true));
        if (m == 'PATCH') {
          p['name'] = body['name'] ?? p['name'];
        } else if (m == 'DELETE') {
          playlists.removeAt(idx);
        }
      } else if (m == 'POST') {
        ts.addAll((body['track_ids'] as List).cast<String>());
      } else if (m == 'PATCH') {
        ts.insert(
          body['index'] as int,
          ts.removeAt((body['position'] as num).toInt() - 1),
        );
      } else if (m == 'DELETE') {
        ts.removeAt((body['position'] as num).toInt() - 1);
      }
      emit('playlist.changed', {'id': seg[1]});
      return m == 'DELETE' && seg.length == 2
          ? send(null, 204)
          : send(playlistJson(p, items: true));
    case ('GET', 'likes', 1):
      return send({
        'items': [for (final id in likes) trackOf(id)],
      });
    case ('PUT', 'likes', 3):
      likes.add(seg[2]);
      emit('like.changed', {'kind': 'track', 'id': seg[2], 'liked': true});
      return send(null, 204);
    case ('DELETE', 'likes', 3):
      likes.remove(seg[2]);
      emit('like.changed', {'kind': 'track', 'id': seg[2], 'liked': false});
      return send(null, 204);
    case ('GET', 'library', 2) when seg[1] == 'roots':
      return send({'items': roots});
    case ('POST', 'library', 2) when seg[1] == 'roots':
      final r = {
        'id': 'r${roots.length + 1}',
        'path': body['path'],
        'enabled': true,
      };
      roots.add(r);
      emit('library.roots_changed', null);
      return send(r, 201);
    case ('DELETE', 'library', 3):
      roots.removeWhere((r) => r['id'] == seg[2]);
      emit('library.roots_changed', null);
      return send(null, 204);
    case ('POST', 'library', 2) when seg[1] == 'scan':
      scanning = true;
      scanDone = 0;
      return send({'started': true}, 202);
    case ('GET', 'library', 2) when seg[1] == 'status':
      return send({
        'scanning': scanning,
        'done': scanDone,
        'total': 1408,
        'pending_jobs': 0,
      });
    case ('POST', 'radio', _):
      queue = [for (final t in tracks.take(8)) t['id'] as String];
      qi = 0;
      emit('queue.changed', null);
      startTrack(0);
      return send(playerJson());
  }
  return err(404, 'not_found', 'ruta desconocida: $m $path');
}
