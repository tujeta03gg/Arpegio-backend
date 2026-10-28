import 'dart:async';
import 'dart:convert';

import 'package:arpegio_ui/data/events_client.dart';
import 'package:arpegio_ui/data/models.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:web_socket_channel/web_socket_channel.dart';

class FakeChannel extends Fake implements WebSocketChannel {
  final ctl = StreamController<dynamic>();
  @override
  Stream<dynamic> get stream => ctl.stream;
}

void main() {
  test('emits snapshot, then reconnects after the socket drops', () async {
    final channels = <FakeChannel>[];
    final client = EventsClient(
      'ws://x/events',
      backoff: const [0],
      connect: (_) {
        final c = FakeChannel();
        channels.add(c);
        return c;
      },
    );
    final states = <bool>[];
    final frames = <EventFrame>[];
    client.connection.listen(states.add);
    client.frames.listen(frames.add);

    client.start();
    channels[0].ctl.add(jsonEncode({'type': 'snapshot', 'data': {}, 'at': ''}));
    await Future<void>.delayed(Duration.zero);
    expect(states, [true]);
    expect(frames.single.type, 'snapshot');

    await channels[0].ctl.close(); // daemon goes away
    await Future<void>.delayed(const Duration(milliseconds: 50));
    expect(states, [true, false]);
    expect(channels, hasLength(2)); // reconnected

    channels[1].ctl.add(jsonEncode({'type': 'snapshot', 'data': {}, 'at': ''}));
    await Future<void>.delayed(Duration.zero);
    expect(states, [true, false, true]);
    client.close();
  });
}
