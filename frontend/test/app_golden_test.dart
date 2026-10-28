import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:arpegio_ui/app.dart';
import 'package:arpegio_ui/data/api_client.dart';
import 'package:arpegio_ui/data/events_client.dart';
import 'package:arpegio_ui/data/models.dart';
import 'package:arpegio_ui/state/providers.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:mocktail/mocktail.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:web_socket_channel/web_socket_channel.dart';

class MockApi extends Mock implements ApiClient {}

class FakeChannel extends Fake implements WebSocketChannel {
  final ctl = StreamController<dynamic>();
  @override
  Stream<dynamic> get stream => ctl.stream;
}

Track _t(String id, String title, String album, String albumId, int sec) =>
    Track(
      id: id,
      title: title,
      artist: 'Synthwave Collective',
      album: album,
      albumId: albumId,
      trackNo: 1,
      durationMs: sec * 1000,
      artists: const [ArtistRef(id: 'a1', name: 'Synthwave Collective')],
    );

Future<void> _loadFonts() async {
  Future<void> load(String family, List<String> files) async {
    final l = FontLoader(family);
    for (final f in files) {
      l.addFont(
        Future.value(
          ByteData.view(File('assets/fonts/$f').readAsBytesSync().buffer),
        ),
      );
    }
    await l.load();
  }

  await load('Chakra Petch', [
    'ChakraPetch-400.ttf',
    'ChakraPetch-500.ttf',
    'ChakraPetch-600.ttf',
    'ChakraPetch-700.ttf',
  ]);
  await load('Share Tech Mono', ['ShareTechMono-400.ttf']);
}

void main() {
  late MockApi api;
  var channel = FakeChannel();
  final now = _t('t1', 'Noches de Neón', 'Horizonte', 'horizonte', 252);
  final others = [
    _t('t2', 'Autopista 404', 'Horizonte', 'horizonte', 221),
    _t('t3', 'Ciudad de cristal', 'Ruido Blanco', 'ruido', 218),
  ];

  setUpAll(_loadFonts);

  setUp(() {
    SharedPreferences.setMockInitialValues({});
    api = MockApi();
    when(() => api.baseUrl).thenReturn('http://127.0.0.1:7600');
    when(() => api.eventsUrl).thenReturn('ws://x/events');
    when(() => api.album(any())).thenAnswer(
      (i) async => Album(id: i.positionalArguments.first as String, title: 'A'),
    );
    when(() => api.home()).thenAnswer(
      (_) async => Home(
        recentlyPlayed: [now, ...others],
        mostPlayed: others,
        recentlyAdded: others,
      ),
    );
    when(() => api.playlists()).thenAnswer(
      (_) async => const [
        Playlist(id: 'p1', name: 'Para programar', itemCount: 24),
      ],
    );
    when(() => api.likedTracks()).thenAnswer((_) async => [now]);
    when(() => api.roots()).thenAnswer(
      (_) async => const [LibraryRoot(id: 'r1', path: '/home/pi/Musica')],
    );
    when(
      () => api.tracks(
        cursor: any(named: 'cursor'),
        limit: any(named: 'limit'),
      ),
    ).thenAnswer((_) async => Page([now, ...others], ''));
    when(
      () => api.albums(
        cursor: any(named: 'cursor'),
        limit: any(named: 'limit'),
      ),
    ).thenAnswer((_) async => const Page<Album>([], ''));
    when(
      () => api.artists(
        cursor: any(named: 'cursor'),
        limit: any(named: 'limit'),
      ),
    ).thenAnswer((_) async => const Page<Artist>([], ''));
    when(() => api.pause()).thenAnswer((_) async {});
    when(
      () => api.play(
        context: any(named: 'context'),
        start: any(named: 'start'),
      ),
    ).thenAnswer((_) async {});
  });

  Future<ProviderContainer> pump(WidgetTester tester) async {
    tester.view.physicalSize = const Size(800, 480);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    final prefs = await SharedPreferences.getInstance();
    final container = ProviderContainer(
      overrides: [
        prefsProvider.overrideWithValue(prefs),
        apiProvider.overrideWithValue(api),
        eventsProvider.overrideWithValue(
          EventsClient('ws://x/events', connect: (_) => channel = FakeChannel()),
        ),
      ],
    );
    addTearDown(container.dispose);
    await tester.pumpWidget(
      UncontrolledProviderScope(
        container: container,
        child: const ArpegioApp(),
      ),
    );
    channel.ctl.add(
      jsonEncode({
        'type': 'snapshot',
        'data': {
          'player': {
            'track': now.toJson(),
            'position_ms': 167000,
            'duration_ms': 252000,
            'playing': true,
            'volume': 80,
            'shuffle': false,
            'repeat': 'off',
          },
          'queue': {
            'items': [
              {
                'position': 1.0,
                'priority': 0,
                'current': true,
                'track': now.toJson(),
              },
              {
                'position': 2.0,
                'priority': 1,
                'current': false,
                'track': others[0].toJson(),
              },
              {
                'position': 3.0,
                'priority': 0,
                'current': false,
                'track': others[1].toJson(),
              },
            ],
          },
          'library': {
            'scanning': false,
            'done': 0,
            'total': 0,
            'pending_jobs': 0,
          },
        },
        'at': '',
      }),
    );
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 300));
    return container;
  }

  testWidgets('Now Playing shows the snapshot (golden)', (tester) async {
    await pump(tester);
    expect(find.text('Noches de Neón'), findsWidgets);
    expect(find.text('SONANDO AHORA'), findsOneWidget);
    expect(find.text('ARPEGIOD'), findsOneWidget);
    await expectLater(
      find.byType(ArpegioApp),
      matchesGoldenFile('goldens/now_playing.png'),
    );
  });

  testWidgets('Pause is optimistic and calls the API', (tester) async {
    final c = await pump(tester);
    await c.read(playerProvider.notifier).toggle();
    expect(c.read(playerProvider).playing, isFalse);
    verify(() => api.pause()).called(1);
  });

  testWidgets('Home, Queue, Settings and Library render (golden)', (
    tester,
  ) async {
    final c = await pump(tester);
    for (final d in [Dest.home, Dest.queue, Dest.settings, Dest.library]) {
      c.read(navProvider.notifier).go(d);
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 400));
      await expectLater(
        find.byType(ArpegioApp),
        matchesGoldenFile('goldens/${d.name}.png'),
      );
    }
  });

  testWidgets('Offline shows the banner', (tester) async {
    await pump(tester);
    await channel.ctl.close();
    await tester.pump(const Duration(milliseconds: 100));
    expect(
      find.text('Sin conexión con arpegiod. Reintentando…'),
      findsOneWidget,
    );
    await tester.pump(
      const Duration(seconds: 2),
    ); // let the retry timer fire before teardown
  });
}
