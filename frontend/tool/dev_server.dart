// Serves build/web and proxies /api (REST + WebSocket) to a real arpegiod, so Chrome sees one origin.
// dart run tool/dev_server.dart [port=7601] [backend=http://127.0.0.1:7600]
import 'dart:async';
import 'dart:io';

const _types = {
  'html': 'text/html; charset=utf-8',
  'js': 'text/javascript',
  'json': 'application/json',
  'wasm': 'application/wasm',
  'png': 'image/png',
  'ttf': 'font/ttf',
  'otf': 'font/otf',
  'svg': 'image/svg+xml',
};

Future<void> main(List<String> args) async {
  final port = args.isNotEmpty ? int.parse(args[0]) : 7601;
  final backend = Uri.parse(args.length > 1 ? args[1] : 'http://127.0.0.1:7600');
  final client = HttpClient();
  final server = await HttpServer.bind(InternetAddress.loopbackIPv4, port);
  stdout.writeln('app en http://127.0.0.1:$port  ->  arpegiod en $backend');
  await for (final req in server) {
    unawaited(_handle(req, client, backend).catchError((Object e) {
      stderr.writeln('error: $e');
      try {
        req.response
          ..statusCode = 502
          ..close();
      } on Object {
        // Response already started.
      }
    }));
  }
}

Future<void> _handle(HttpRequest req, HttpClient client, Uri backend) async {
  if (!req.uri.path.startsWith('/api/')) return _static(req);
  if (WebSocketTransformer.isUpgradeRequest(req)) {
    final up = await WebSocket.connect(backend.replace(scheme: 'ws', path: req.uri.path).toString());
    final ws = await WebSocketTransformer.upgrade(req);
    unawaited(ws.pipe(up).catchError((Object _) {}));
    unawaited(up.pipe(ws).catchError((Object _) {}));
    return;
  }
  final out = await client.openUrl(req.method, backend.replace(path: req.uri.path, query: req.uri.query));
  req.headers.forEach((n, v) {
    if (n != 'host') out.headers.set(n, v);
  });
  await out.addStream(req);
  final res = await out.close();
  req.response.statusCode = res.statusCode;
  res.headers.forEach((n, v) {
    if (n != 'transfer-encoding') req.response.headers.set(n, v);
  });
  await req.response.addStream(res);
  await req.response.close();
}

Future<void> _static(HttpRequest req) async {
  final rel = req.uri.path == '/' ? 'index.html' : req.uri.path.substring(1);
  var f = File('build/web/$rel');
  if (rel.contains('..') || !f.existsSync()) f = File('build/web/index.html');
  if (!f.existsSync()) {
    req.response
      ..statusCode = 404
      ..write('Falta build/web: ejecuta "flutter build web" primero.');
    return req.response.close();
  }
  req.response.headers.contentType = ContentType.parse(_types[f.path.split('.').last] ?? 'application/octet-stream');
  await req.response.addStream(f.openRead());
  await req.response.close();
}
