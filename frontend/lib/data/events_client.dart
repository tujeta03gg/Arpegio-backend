import 'dart:async';
import 'dart:convert';

import 'package:web_socket_channel/web_socket_channel.dart';

import 'models.dart';

typedef SocketFactory = WebSocketChannel Function(Uri uri);

/// WebSocket `/events` with reconnection (1, 2, 5, 10 s). The first frame of
/// every connection is the `snapshot`.
class EventsClient {
  EventsClient(
    this.url, {
    SocketFactory? connect,
    this.backoff = const [1, 2, 5, 10],
  }) : _connect = connect ?? WebSocketChannel.connect;
  final String url;
  final List<int> backoff;
  final SocketFactory _connect;

  final _frames = StreamController<EventFrame>.broadcast();
  final _status = StreamController<bool>.broadcast();
  StreamSubscription<dynamic>? _sub;
  Timer? _retry;
  int _attempt = 0;
  bool _closed = false;

  Stream<EventFrame> get frames => _frames.stream;

  /// true on connect (first frame received), false when the socket drops.
  Stream<bool> get connection => _status.stream;

  void start() {
    if (_closed) return;
    try {
      final ch = _connect(Uri.parse(url));
      _sub = ch.stream.listen(
        (raw) {
          try {
            final f = EventFrame.fromJson(jsonDecode(raw as String) as Json);
            if (f.type == 'snapshot') {
              _attempt = 0;
              _status.add(true);
            }
            _frames.add(f);
          } on FormatException {
            // Malformed frame: skip it, keep the connection.
          }
        },
        onError: (_) => _lost(),
        onDone: _lost,
        cancelOnError: true,
      );
    } on Exception {
      _lost();
    }
  }

  void _lost() {
    if (_closed) return;
    _sub?.cancel();
    _sub = null;
    _status.add(false);
    _retry?.cancel();
    final secs =
        backoff[_attempt < backoff.length ? _attempt : backoff.length - 1];
    _attempt++;
    _retry = Timer(Duration(seconds: secs), start);
  }

  void close() {
    _closed = true;
    _retry?.cancel();
    _sub?.cancel();
    _frames.close();
    _status.close();
  }
}
