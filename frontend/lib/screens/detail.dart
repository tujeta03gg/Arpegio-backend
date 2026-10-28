import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../data/api_client.dart';
import '../data/models.dart';
import '../design/tokens.dart';
import '../design/typography.dart';
import '../l10n/strings.dart';
import '../state/providers.dart';
import '../widgets/artwork.dart';
import '../widgets/controls.dart';
import '../widgets/rows.dart';
import '../widgets/track_menu.dart';

/// Album, artist, playlist or "Favoritas" on top of the current destination.
class DetailScreen extends ConsumerWidget {
  const DetailScreen({super.key, required this.detail});
  final Detail detail;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final id = detail.id;
    final body = switch (detail.kind) {
      DetailKind.album => _async(
        ref.watch(albumDetailProvider(id)),
        (a) => _AlbumBody(album: a),
      ),
      DetailKind.artist => _async(
        ref.watch(artistDetailProvider(id)),
        (a) => _ArtistBody(artist: a),
      ),
      DetailKind.playlist => _async(
        ref.watch(playlistDetailProvider(id)),
        (p) => _PlaylistBody(playlist: p),
      ),
      DetailKind.liked => _async(
        ref.watch(likedTracksProvider),
        (ts) => _LikedBody(tracks: ts),
      ),
    };
    return ColoredBox(color: AppColors.void000, child: body);
  }

  Widget _async<T>(AsyncValue<T> v, Widget Function(T) build) => v.when(
    data: build,
    loading: () => const ScreenScroll(children: [SkeletonRows()]),
    error: (e, _) => Center(
      child: EmptyNote(
        e is ApiException && e.isNotFound ? S.notFound : S.failed,
        icon: 'server',
      ),
    ),
  );
}

/// Back button + cover + texts + Reproducir, shared by all details.
class _Head extends ConsumerWidget {
  const _Head({
    required this.kicker,
    required this.title,
    required this.subtitle,
    required this.cover,
    this.onPlay,
    this.actions = const [],
  });
  final String kicker;
  final String title;
  final String subtitle;
  final Widget cover;
  final VoidCallback? onPlay;
  final List<Widget> actions;

  @override
  Widget build(BuildContext context, WidgetRef ref) => Padding(
    padding: const EdgeInsets.only(bottom: AppSpace.s4),
    child: Row(
      crossAxisAlignment: CrossAxisAlignment.end,
      children: [
        Align(
          alignment: Alignment.topLeft,
          child: ArIconButton(
            'back',
            label: S.back,
            onTap: ref.read(navProvider.notifier).back,
          ),
        ),
        const SizedBox(width: AppSpace.s2),
        cover,
        const SizedBox(width: AppSpace.s4),
        Expanded(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                kicker.toUpperCase(),
                style: AppText.label.copyWith(color: AppColors.neonCyan),
              ),
              Text(
                title,
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
                style: AppText.titleLg,
              ),
              Text(
                subtitle,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: AppText.body.copyWith(color: AppColors.inkMuted),
              ),
            ],
          ),
        ),
        const SizedBox(width: AppSpace.s3),
        if (onPlay != null) ArButton(S.play, icon: 'play', onTap: onPlay),
        ...actions,
      ],
    ),
  );
}

String _summary(int n, int ms, [String prefix = '']) =>
    '$prefix${S.songCount(n)} · ${(ms / 60000).round()} min';

class _AlbumBody extends ConsumerWidget {
  const _AlbumBody({required this.album});
  final Album album;

  @override
  Widget build(BuildContext context, WidgetRef ref) => ScreenScroll(
    children: [
      _Head(
        kicker: 'Álbum',
        title: album.title,
        subtitle:
            '${album.artist} · ${_summary(album.tracks.length, album.durationMs)}',
        cover: Artwork(
          artworkId: album.artworkId,
          seed: album.title,
          size: AppSize.coverDetail,
          chamfer: true,
        ),
        onPlay: album.tracks.isEmpty
            ? null
            : () => ref
                  .read(playerProvider.notifier)
                  .playContext('album:${album.id}'),
      ),
      for (final t in album.tracks)
        TrackRow(
          track: t,
          showAlbum: false,
          leading: SizedBox.square(
            dimension: AppSize.coverRow,
            child: Center(
              child: Text(
                '${t.trackNo}',
                style: AppText.monoTime.copyWith(color: AppColors.inkMuted),
              ),
            ),
          ),
          onTap: () => ref
              .read(playerProvider.notifier)
              .playContext('album:${album.id}', start: t.id),
        ),
    ],
  );
}

class _ArtistBody extends ConsumerWidget {
  const _ArtistBody({required this.artist});
  final Artist artist;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final nav = ref.read(navProvider.notifier);
    return ScreenScroll(
      children: [
        _Head(
          kicker: 'Artista',
          title: artist.name,
          subtitle:
              '${artist.albums.length} ${artist.albums.length == 1 ? 'álbum' : 'álbumes'}',
          cover: Artwork(
            artworkId: '',
            seed: artist.name,
            size: AppSize.coverDetail,
            round: true,
          ),
          onPlay: artist.tracks.isEmpty && artist.albums.isEmpty
              ? null
              : () => ref
                    .read(playerProvider.notifier)
                    .playContext('artist:${artist.id}'),
        ),
        if (artist.albums.isNotEmpty) ...[
          const SectionLabel(S.albums, first: true),
          Shelf(
            children: [
              for (final a in artist.albums)
                AlbumCard(
                  artworkId: a.artworkId,
                  title: a.title,
                  subtitle: a.year > 0 ? '${a.year}' : a.artist,
                  onTap: () => nav.open(Detail(DetailKind.album, a.id)),
                ),
            ],
          ),
        ],
        if (artist.tracks.isNotEmpty) ...[
          const SectionLabel(S.popular),
          for (final t in artist.tracks)
            TrackRow(
              track: t,
              onTap: () => ref
                  .read(playerProvider.notifier)
                  .playContext('artist:${artist.id}', start: t.id),
            ),
        ],
      ],
    );
  }
}

class _LikedBody extends ConsumerWidget {
  const _LikedBody({required this.tracks});
  final List<Track> tracks;

  @override
  Widget build(BuildContext context, WidgetRef ref) => ScreenScroll(
    children: [
      _Head(
        kicker: 'Playlist',
        title: S.favorites,
        subtitle: _summary(
          tracks.length,
          tracks.fold(0, (s, t) => s + t.durationMs),
        ),
        cover: const Artwork(
          artworkId: '',
          seed: S.favorites,
          size: AppSize.coverDetail,
          chamfer: true,
        ),
        onPlay: tracks.isEmpty
            ? null
            : () => ref.read(playerProvider.notifier).playContext('likes:'),
      ),
      for (final t in tracks)
        TrackRow(
          track: t,
          onTap: () => ref
              .read(playerProvider.notifier)
              .playContext('likes:', start: t.id),
        ),
    ],
  );
}

class _PlaylistBody extends ConsumerStatefulWidget {
  const _PlaylistBody({required this.playlist});
  final Playlist playlist;
  @override
  ConsumerState<_PlaylistBody> createState() => _PlaylistBodyState();
}

class _PlaylistBodyState extends ConsumerState<_PlaylistBody> {
  late List<PlaylistItem> _items = widget.playlist.items;

  @override
  void didUpdateWidget(_PlaylistBody old) {
    super.didUpdateWidget(old);
    _items = widget.playlist.items;
  }

  Future<void> _reorder(int from, int to) async {
    if (from == to) return;
    final moved = _items[from];
    setState(() => _items = ([..._items]..removeAt(from))..insert(to, moved));
    try {
      await ref
          .read(apiProvider)
          .playlistMove(widget.playlist.id, moved.position, to);
    } on ApiException catch (e) {
      ref.read(toastProvider.notifier).error(e);
      ref.invalidate(playlistDetailProvider(widget.playlist.id));
    }
  }

  Future<void> _rename() async {
    final name = await promptText(
      context,
      S.rename,
      S.playlistName,
      S.rename,
      initial: widget.playlist.name,
    );
    if (name == null || name.trim().isEmpty) return;
    try {
      await ref
          .read(apiProvider)
          .renamePlaylist(widget.playlist.id, name.trim());
    } on ApiException catch (e) {
      ref.read(toastProvider.notifier).error(e);
    }
  }

  Future<void> _delete() async {
    if (!await confirmDialog(
      context,
      '${S.delete} “${widget.playlist.name}”?',
      S.delete,
    )) {
      return;
    }
    try {
      await ref.read(apiProvider).deletePlaylist(widget.playlist.id);
      ref.read(navProvider.notifier).back();
    } on ApiException catch (e) {
      ref.read(toastProvider.notifier).error(e);
    }
  }

  @override
  Widget build(BuildContext context) {
    final p = widget.playlist;
    final n = ref.read(playerProvider.notifier);
    return CustomScrollView(
      slivers: [
        SliverPadding(
          padding: const EdgeInsets.fromLTRB(
            AppSpace.s4,
            AppSpace.s4,
            AppSpace.s4,
            0,
          ),
          sliver: SliverToBoxAdapter(
            child: _Head(
              kicker: 'Playlist',
              title: p.name,
              subtitle: _summary(
                _items.length,
                _items.fold(0, (s, i) => s + i.track.durationMs),
              ),
              cover: Artwork(
                artworkId: '',
                seed: p.name,
                size: AppSize.coverDetail,
                chamfer: true,
              ),
              onPlay: _items.isEmpty
                  ? null
                  : () => n.playContext('playlist:${p.id}'),
              actions: [
                ArIconButton(
                  'settings',
                  muted: true,
                  label: S.rename,
                  onTap: _rename,
                ),
                ArIconButton(
                  'close',
                  muted: true,
                  label: S.delete,
                  onTap: _delete,
                ),
              ],
            ),
          ),
        ),
        SliverPadding(
          padding: const EdgeInsets.symmetric(horizontal: AppSpace.s4),
          sliver: SliverReorderableList(
            itemCount: _items.length,
            onReorderItem: _reorder,
            itemBuilder: (_, i) {
              final it = _items[i];
              return ReorderableDelayedDragStartListener(
                key: ValueKey(it.position),
                index: i,
                child: TrackRow(
                  track: it.track,
                  onTap: () =>
                      n.playContext('playlist:${p.id}', start: it.track.id),
                ),
              );
            },
          ),
        ),
        const SliverToBoxAdapter(child: SizedBox(height: AppSpace.s4)),
      ],
    );
  }
}
