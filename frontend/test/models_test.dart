import 'package:arpegio_ui/data/models.dart';
import 'package:flutter_test/flutter_test.dart';

const _track = {
  'id': 't1',
  'title': 'Noches de Neón',
  'artist': 'Synthwave Collective',
  'album': 'Horizonte',
  'album_id': 'al1',
  'track_no': 1,
  'duration_ms': 252000,
  'artists': [
    {'id': 'a1', 'name': 'Synthwave Collective', 'role': 'main'},
  ],
};

void main() {
  test('Track parses and exposes artistId', () {
    final t = Track.fromJson(_track);
    expect(t.title, 'Noches de Neón');
    expect(t.albumId, 'al1');
    expect(t.durationMs, 252000);
    expect(t.artistId, 'a1');
  });

  test('PlayerState parses a missing track and defaults', () {
    final p = PlayerState.fromJson({
      'playing': true,
      'volume': 40,
      'repeat': 'all',
    });
    expect(p.track, isNull);
    expect(p.playing, isTrue);
    expect(p.volume, 40);
    expect(p.repeat, 'all');
  });

  test('QueueState finds current and upcoming', () {
    final q = QueueState.fromJson({
      'items': [
        for (var i = 0; i < 3; i++)
          {
            'position': i + 1.0,
            'priority': 0,
            'current': i == 1,
            'track': _track,
          },
      ],
    });
    expect(q.currentIndex, 1);
    expect(q.upcoming, hasLength(1));
  });

  test('Album parses artwork and tracks', () {
    final a = Album.fromJson({
      'id': 'al1',
      'title': 'Horizonte',
      'artwork_id': 'art',
      'tracks': [_track],
    });
    expect(a.artworkId, 'art');
    expect(a.durationMs, 252000);
  });

  test('SearchResult parses grouped hits', () {
    final r = SearchResult.fromJson({
      'tracks': [
        {'id': 't1', 'title': 'x', 'artist': 'y', 'album': 'z'},
      ],
    });
    expect(r.tracks.single.id, 't1');
    expect(r.albums, isEmpty);
  });
}
