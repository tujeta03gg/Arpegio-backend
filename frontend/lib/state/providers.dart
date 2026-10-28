import 'dart:async';

import 'package:flutter/foundation.dart' show kIsWeb;
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../data/api_client.dart';
import '../data/events_client.dart';
import '../data/models.dart';
import '../l10n/strings.dart';

// ---------------------------------------------------------------- plumbing

final apiProvider = Provider<ApiClient>((ref) {
  const configured = String.fromEnvironment('ARPEGIO_API');
  // On web the app is served by a dev server that also proxies /api, so use its own origin.
  final base = configured.isNotEmpty
      ? configured
      : (kIsWeb ? Uri.base.origin : 'http://127.0.0.1:7600');
  final c = ApiClient(base);
  ref.onDispose(c.close);
  return c;
});

/// Overridden in `main` once preferences are loaded.
final prefsProvider = Provider<SharedPreferences>(
  (_) => throw UnimplementedError('prefsProvider'),
);

/// Shows a short notice (bottom sheet of 3 s) and turns API errors into plain text.
class ToastNotifier extends Notifier<String?> {
  Timer? _t;
  @override
  String? build() {
    ref.onDispose(() => _t?.cancel());
    return null;
  }

  void show(String msg) {
    state = msg;
    _t?.cancel();
    _t = Timer(const Duration(seconds: 3), () => state = null);
  }

  void error(Object e) => show(friendlyError(e));
}

final toastProvider = NotifierProvider<ToastNotifier, String?>(
  ToastNotifier.new,
);

String friendlyError(Object e) {
  if (e is ApiException) {
    if (e.isNotFound) return S.notFound;
    if (e.status == 503) return S.unavailable;
  }
  return S.failed;
}

/// Counter that other providers watch to invalidate themselves.
class RevNotifier extends Notifier<int> {
  @override
  int build() => 0;
  void bump() => state++;
}

final catalogRevProvider = NotifierProvider<RevNotifier, int>(RevNotifier.new);
final playlistRevProvider = NotifierProvider<RevNotifier, int>(RevNotifier.new);
final rootsRevProvider = NotifierProvider<RevNotifier, int>(RevNotifier.new);

class FlagNotifier extends Notifier<bool> {
  FlagNotifier(this._key, this._default);
  final String? _key;
  final bool _default;
  @override
  bool build() => _key == null
      ? _default
      : (ref.read(prefsProvider).getBool(_key) ?? _default);
  void set(bool v) {
    state = v;
    if (_key != null) ref.read(prefsProvider).setBool(_key, v);
  }
}

final connectedProvider = NotifierProvider<FlagNotifier, bool>(
  () => FlagNotifier(null, false),
);
final visualizerProvider = NotifierProvider<FlagNotifier, bool>(
  () => FlagNotifier('viz', true),
);
final calmProvider = NotifierProvider<FlagNotifier, bool>(
  () => FlagNotifier('calm', false),
);

// ---------------------------------------------------------------- player

class PlayerNotifier extends Notifier<PlayerState> {
  ApiClient get _api => ref.read(apiProvider);

  @override
  PlayerState build() => const PlayerState();

  void set(PlayerState s) {
    state = s;
    ref.read(positionProvider.notifier).set(s.positionMs);
  }

  Future<void> refresh() async {
    try {
      set(await _api.player());
    } on ApiException {
      // The next event fixes it.
    }
  }

  /// Optimistic change; reverts and warns if the request fails.
  Future<void> _act(
    PlayerState optimistic,
    Future<void> Function() call,
  ) async {
    final before = state;
    state = optimistic;
    try {
      await call();
    } on ApiException catch (e) {
      state = before;
      ref.read(toastProvider.notifier).error(e);
    }
  }

  Future<void> toggle() => state.playing
      ? _act(state.copyWith(playing: false), _api.pause)
      : _act(state.copyWith(playing: true), () => _api.play());

  Future<void> next() => _plain(_api.next);
  Future<void> previous() => _plain(_api.previous);

  Future<void> _plain(Future<void> Function() call) async {
    try {
      await call();
    } on ApiException catch (e) {
      ref.read(toastProvider.notifier).error(e);
    }
  }

  Future<void> seek(int ms) {
    ref.read(positionProvider.notifier).set(ms);
    return _plain(() => _api.seek(ms));
  }

  Future<void> setShuffle(bool on) =>
      _act(state.copyWith(shuffle: on), () => _api.setShuffle(on));

  Future<void> cycleRepeat() {
    final n =
        const {'off': 'all', 'all': 'one', 'one': 'off'}[state.repeat] ?? 'off';
    return _act(state.copyWith(repeat: n), () => _api.setRepeat(n));
  }

  Future<void> setVolume(int level) =>
      _act(state.copyWith(volume: level), () => _api.setVolume(level));

  /// "album:ID", "playlist:ID", "artist:ID", "track:ID", "likes:".
  Future<void> playContext(String context, {String? start}) => _plain(
    () => _api.play(
      context: context,
      start: start == null ? null : 'track:$start',
    ),
  );

  Future<void> radio(String seed) => _plain(() => _api.radio(seed));
}

final playerProvider = NotifierProvider<PlayerNotifier, PlayerState>(
  PlayerNotifier.new,
);

/// Playback position in ms, on its own so only the progress bar repaints each second.
class PositionNotifier extends Notifier<int> {
  @override
  int build() => 0;
  void set(int ms) => state = ms;
}

final positionProvider = NotifierProvider<PositionNotifier, int>(
  PositionNotifier.new,
);

// ---------------------------------------------------------------- queue

class QueueNotifier extends Notifier<QueueState> {
  ApiClient get _api => ref.read(apiProvider);
  @override
  QueueState build() => const QueueState();

  void set(QueueState q) => state = q;

  Future<void> refresh() async {
    try {
      state = await _api.queue();
    } on ApiException {
      // Keep the last known queue.
    }
  }

  Future<void> _do(Future<void> Function() call) async {
    try {
      await call();
    } on ApiException catch (e) {
      ref.read(toastProvider.notifier).error(e);
      await refresh();
    }
  }

  Future<void> add(String trackId, {bool next = false}) async {
    await _do(() => _api.queueAdd([trackId], next: next));
    ref.read(toastProvider.notifier).show(next ? S.addedNext : S.added);
  }

  /// [from] and [to] are indexes in the full item list.
  Future<void> move(int from, int to) {
    final items = [...state.items];
    final it = items.removeAt(from);
    items.insert(to, it);
    state = QueueState(items: items);
    return _do(() => _api.queueMove(it.position, to));
  }

  Future<void> remove(QueueItem it) {
    state = QueueState(items: [...state.items]..remove(it));
    return _do(() => _api.queueRemove(it.position));
  }

  Future<void> clear() => _do(_api.queueClear);
}

final queueProvider = NotifierProvider<QueueNotifier, QueueState>(
  QueueNotifier.new,
);

// ---------------------------------------------------------------- likes

class LikesNotifier extends Notifier<Set<String>> {
  ApiClient get _api => ref.read(apiProvider);
  @override
  Set<String> build() => {};

  Future<void> refresh() async {
    try {
      state = {for (final t in await _api.likedTracks()) t.id};
    } on ApiException {
      // Keep what we have.
    }
  }

  Future<void> toggle(String id) async {
    final was = state.contains(id);
    state = was ? ({...state}..remove(id)) : {...state, id};
    try {
      await (was ? _api.unlike(id) : _api.like(id));
    } on ApiException catch (e) {
      state = was ? {...state, id} : ({...state}..remove(id));
      ref.read(toastProvider.notifier).error(e);
    }
  }

  void apply(String id, bool liked) =>
      state = liked ? {...state, id} : ({...state}..remove(id));
}

final likesProvider = NotifierProvider<LikesNotifier, Set<String>>(
  LikesNotifier.new,
);

// ---------------------------------------------------------------- library status

class LibraryStatusNotifier extends Notifier<LibraryStatus> {
  @override
  LibraryStatus build() => const LibraryStatus();
  void set(LibraryStatus s) => state = s;
}

final libraryStatusProvider =
    NotifierProvider<LibraryStatusNotifier, LibraryStatus>(
      LibraryStatusNotifier.new,
    );

// ---------------------------------------------------------------- events

final eventsProvider = Provider<EventsClient>((ref) {
  final c = EventsClient(ref.watch(apiProvider).eventsUrl);
  ref.onDispose(c.close);
  return c;
});

/// Wires WebSocket frames to the providers above. Watched once by the app.
final syncProvider = Provider<void>((ref) {
  final c = ref.watch(eventsProvider);
  final subs = [
    c.connection.listen(ref.read(connectedProvider.notifier).set),
    c.frames.listen((f) => dispatchEvent(ref, f)),
  ];
  ref.onDispose(() {
    for (final s in subs) {
      s.cancel();
    }
  });
  c.start();
});

void dispatchEvent(Ref ref, EventFrame f) {
  final d = f.data is Json ? f.data! as Json : const <String, dynamic>{};
  switch (f.type) {
    case 'snapshot':
      ref
          .read(playerProvider.notifier)
          .set(PlayerState.fromJson(d['player'] as Json));
      ref
          .read(queueProvider.notifier)
          .set(QueueState.fromJson(d['queue'] as Json));
      ref
          .read(libraryStatusProvider.notifier)
          .set(LibraryStatus.fromJson(d['library'] as Json));
      ref.read(likesProvider.notifier).refresh();
    case 'player.state_changed':
      ref.read(playerProvider.notifier).set(PlayerState.fromJson(d));
    case 'player.position':
      ref
          .read(positionProvider.notifier)
          .set((d['position_ms'] as num?)?.toInt() ?? 0);
    case 'player.track_started':
      ref.read(playerProvider.notifier).refresh();
    case 'queue.changed':
      ref.read(queueProvider.notifier).refresh();
    case 'catalog.updated':
      ref.read(catalogRevProvider.notifier).bump();
    case 'playlist.changed':
      ref.read(playlistRevProvider.notifier).bump();
    case 'like.changed':
      if (d['kind'] == 'track') {
        ref
            .read(likesProvider.notifier)
            .apply(d['id'] as String, d['liked'] as bool? ?? false);
      }
    case 'library.scan_progress':
      ref
          .read(libraryStatusProvider.notifier)
          .set(
            LibraryStatus(
              scanning: true,
              done: (d['done'] as num?)?.toInt() ?? 0,
              total: (d['total'] as num?)?.toInt() ?? 0,
            ),
          );
    case 'library.scan_finished':
      ref.read(libraryStatusProvider.notifier).set(const LibraryStatus());
      ref.read(catalogRevProvider.notifier).bump();
    case 'library.roots_changed':
      ref.read(rootsRevProvider.notifier).bump();
  }
}

// ---------------------------------------------------------------- navigation

enum Dest { now, home, search, library, queue, settings }

enum DetailKind { album, artist, playlist, liked }

class Detail {
  const Detail(this.kind, [this.id = '']);
  final DetailKind kind;
  final String id;
}

class NavState {
  const NavState({
    this.dest = Dest.now,
    this.stack = const [],
    this.last = Dest.home,
  });
  final Dest dest;
  final List<Detail> stack;
  final Dest last; // destination to return to from Now Playing

  Detail? get detail => stack.isEmpty ? null : stack.last;
}

class NavNotifier extends Notifier<NavState> {
  @override
  NavState build() => const NavState();

  void go(Dest d) =>
      state = NavState(dest: d, last: d == Dest.now ? state.last : d);

  /// Opens a detail on top of the current destination (leaving Now Playing for the last one).
  void open(Detail d) {
    final dest = state.dest == Dest.now ? state.last : state.dest;
    state = NavState(dest: dest, stack: [...state.stack, d], last: dest);
  }

  /// Pops the top detail. Returns false if there was none.
  bool back() {
    if (state.stack.isNotEmpty) {
      state = NavState(
        dest: state.dest,
        stack: state.stack.sublist(0, state.stack.length - 1),
        last: state.last,
      );
      return true;
    }
    return false;
  }
}

final navProvider = NotifierProvider<NavNotifier, NavState>(NavNotifier.new);

// ---------------------------------------------------------------- catalog data

class PagedState<T> {
  const PagedState({
    this.items = const [],
    this.cursor = '',
    this.loading = false,
    this.done = false,
  });
  final List<T> items;
  final String cursor;
  final bool loading;
  final bool done;
}

/// Cursor-paginated list, reset whenever the catalog changes.
class PagedNotifier<T> extends Notifier<PagedState<T>> {
  PagedNotifier(this._fetch);
  final Future<Page<T>> Function(ApiClient api, String cursor) _fetch;

  @override
  PagedState<T> build() {
    ref.watch(catalogRevProvider);
    Future.microtask(loadMore);
    return PagedState<T>();
  }

  Future<void> loadMore() async {
    if (!ref.mounted || state.loading || state.done) return;
    final cur = state;
    state = PagedState(items: cur.items, cursor: cur.cursor, loading: true);
    try {
      final p = await _fetch(ref.read(apiProvider), cur.cursor);
      if (!ref.mounted) return;
      state = PagedState(
        items: [...cur.items, ...p.items],
        cursor: p.nextCursor,
        done: p.nextCursor.isEmpty,
      );
    } on ApiException {
      if (ref.mounted) {
        state = PagedState(items: cur.items, cursor: cur.cursor, done: true);
      }
    }
  }
}

final tracksProvider =
    NotifierProvider<PagedNotifier<Track>, PagedState<Track>>(
      () => PagedNotifier((a, c) => a.tracks(cursor: c)),
    );
final albumsProvider =
    NotifierProvider<PagedNotifier<Album>, PagedState<Album>>(
      () => PagedNotifier((a, c) => a.albums(cursor: c)),
    );
final artistsProvider =
    NotifierProvider<PagedNotifier<Artist>, PagedState<Artist>>(
      () => PagedNotifier((a, c) => a.artists(cursor: c)),
    );

final homeProvider = FutureProvider.autoDispose<Home>((ref) {
  ref.watch(catalogRevProvider);
  return ref.watch(apiProvider).home();
});

final playlistsProvider = FutureProvider.autoDispose<List<Playlist>>((ref) {
  ref.watch(playlistRevProvider);
  return ref.watch(apiProvider).playlists();
});

final albumDetailProvider = FutureProvider.autoDispose.family<Album, String>((
  ref,
  id,
) {
  ref.watch(catalogRevProvider);
  return ref.watch(apiProvider).album(id);
});

final artistDetailProvider = FutureProvider.autoDispose.family<Artist, String>((
  ref,
  id,
) {
  ref.watch(catalogRevProvider);
  return ref.watch(apiProvider).artist(id);
});

final playlistDetailProvider = FutureProvider.autoDispose
    .family<Playlist, String>((ref, id) {
      ref.watch(playlistRevProvider);
      return ref.watch(apiProvider).playlist(id);
    });

final likedTracksProvider = FutureProvider.autoDispose<List<Track>>((ref) {
  ref.watch(likesProvider);
  return ref.watch(apiProvider).likedTracks();
});

final lyricsProvider = FutureProvider.autoDispose.family<Lyrics, String>(
  (ref, trackId) => ref.watch(apiProvider).lyrics(trackId),
);

final rootsProvider = FutureProvider.autoDispose<List<LibraryRoot>>((ref) {
  ref.watch(rootsRevProvider);
  return ref.watch(apiProvider).roots();
});

/// Tracks carry only `album_id`; the cover id lives on the album (see docs/huecos-api.md).
final artworkIdProvider = FutureProvider.family<String, String>((
  ref,
  albumId,
) async {
  if (albumId.isEmpty) return '';
  try {
    return (await ref.watch(apiProvider).album(albumId)).artworkId;
  } on ApiException {
    return '';
  }
});

class SearchQueryNotifier extends Notifier<String> {
  @override
  String build() => '';
  void set(String q) => state = q;
}

final searchQueryProvider = NotifierProvider<SearchQueryNotifier, String>(
  SearchQueryNotifier.new,
);

final searchResultProvider = FutureProvider.autoDispose<SearchResult>((ref) {
  ref.watch(catalogRevProvider);
  final q = ref.watch(searchQueryProvider).trim();
  if (q.length < 2) return const SearchResult();
  return ref.watch(apiProvider).search(q);
});
