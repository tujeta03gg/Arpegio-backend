import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../data/api_client.dart';
import '../data/models.dart';
import '../design/icons.dart';
import '../design/tokens.dart';
import '../design/typography.dart';
import '../l10n/strings.dart';
import '../state/providers.dart';
import '../widgets/artwork.dart';
import '../widgets/controls.dart';
import '../widgets/format.dart';
import '../widgets/rows.dart';
import '../widgets/track_menu.dart';
import 'home.dart' show playTrack;

enum _Tab { songs, albums, artists, playlists }

/// Row with a round cover and a name (artists).
class ArtistRow extends StatelessWidget {
  const ArtistRow({
    super.key,
    required this.name,
    required this.onTap,
    this.subtitle,
  });
  final String name;
  final String? subtitle;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) => Pressable(
    onTap: onTap,
    scale: 1,
    pressedColor: AppColors.void200,
    label: name,
    child: Container(
      height: AppSize.rowH,
      padding: const EdgeInsets.only(left: AppSpace.s2, right: AppSpace.s2),
      decoration: const BoxDecoration(
        border: Border(bottom: BorderSide(color: AppColors.void300)),
      ),
      child: Row(
        children: [
          Artwork(
            artworkId: '',
            seed: name,
            size: AppSize.coverRow,
            round: true,
          ),
          const SizedBox(width: AppSpace.s3),
          Expanded(
            child: Column(
              mainAxisAlignment: MainAxisAlignment.center,
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  name,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: AppText.body,
                ),
                if (subtitle != null)
                  Text(
                    subtitle!,
                    style: AppText.bodySm.copyWith(color: AppColors.inkMuted),
                  ),
              ],
            ),
          ),
          const ArpegioIcon(
            'next',
            size: AppSize.iconSm,
            color: AppColors.inkMuted,
          ),
        ],
      ),
    ),
  );
}

class LibraryScreen extends ConsumerStatefulWidget {
  const LibraryScreen({super.key});
  @override
  ConsumerState<LibraryScreen> createState() => _LibraryScreenState();
}

class _LibraryScreenState extends ConsumerState<LibraryScreen> {
  _Tab _tab = _Tab.songs;
  final _scroll = ScrollController();

  @override
  void initState() {
    super.initState();
    _scroll.addListener(_maybeLoadMore);
  }

  @override
  void dispose() {
    _scroll.dispose();
    super.dispose();
  }

  /// Next page when the scroll passes 80%.
  void _maybeLoadMore() {
    if (!_scroll.hasClients ||
        _scroll.position.pixels < _scroll.position.maxScrollExtent * 0.8) {
      return;
    }
    switch (_tab) {
      case _Tab.songs:
        ref.read(tracksProvider.notifier).loadMore();
      case _Tab.albums:
        ref.read(albumsProvider.notifier).loadMore();
      case _Tab.artists:
        ref.read(artistsProvider.notifier).loadMore();
      case _Tab.playlists:
    }
  }

  @override
  Widget build(BuildContext context) {
    final nav = ref.read(navProvider.notifier);
    // `total` counts the whole catalog; fall back to what is loaded if the API omits it.
    int total(PagedState<Object?> p) => p.total > 0 ? p.total : p.items.length;
    final count = switch (_tab) {
      _Tab.songs => total(ref.watch(tracksProvider)),
      _Tab.albums => total(ref.watch(albumsProvider)),
      _Tab.artists => total(ref.watch(artistsProvider)),
      _Tab.playlists => ref.watch(playlistsProvider).value?.length ?? 0,
    };
    final unit = switch (_tab) {
      _Tab.songs => 'CANCIONES',
      _Tab.albums => 'ÁLBUMES',
      _Tab.artists => 'ARTISTAS',
      _Tab.playlists => 'PLAYLISTS',
    };
    return Padding(
      padding: const EdgeInsets.fromLTRB(
        AppSpace.s4,
        AppSpace.s4,
        AppSpace.s4,
        0,
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          ScreenHeader(
            S.library,
            trailing: Text(
              '${fmtCount(count)} $unit',
              style: AppText.monoStatus.copyWith(color: AppColors.inkMuted),
            ),
          ),
          ChipBar<_Tab>(
            value: _tab,
            onChanged: (v) {
              setState(() => _tab = v);
              if (_scroll.hasClients) _scroll.jumpTo(0);
            },
            items: const [
              (_Tab.songs, S.songs),
              (_Tab.albums, S.albums),
              (_Tab.artists, S.artists),
              (_Tab.playlists, S.playlists),
            ],
          ),
          const SizedBox(height: AppSpace.s3),
          Expanded(
            child: switch (_tab) {
              _Tab.songs => _list<Track>(
                ref.watch(tracksProvider),
                (t) => TrackRow(track: t, onTap: () => playTrack(ref, t)),
              ),
              _Tab.albums => _albums(ref.watch(albumsProvider), nav),
              _Tab.artists => _list<Artist>(
                ref.watch(artistsProvider),
                (a) => ArtistRow(
                  name: a.name,
                  onTap: () => nav.open(Detail(DetailKind.artist, a.id)),
                ),
              ),
              _Tab.playlists => _playlists(nav),
            },
          ),
        ],
      ),
    );
  }

  Widget _list<T>(PagedState<T> s, Widget Function(T) row) {
    if (s.items.isEmpty) {
      return s.done
          ? const EmptyNote(S.nothingPlaying, icon: 'library')
          : const SkeletonRows();
    }
    return ListView.builder(
      controller: _scroll,
      itemCount: s.items.length + (s.done ? 0 : 1),
      itemExtent: AppSize.rowH,
      itemBuilder: (_, i) =>
          i < s.items.length ? row(s.items[i]) : const SizedBox.shrink(),
    );
  }

  Widget _albums(PagedState<Album> s, NavNotifier nav) {
    if (s.items.isEmpty) {
      return s.done
          ? const EmptyNote(S.nothingPlaying, icon: 'library')
          : const SkeletonRows();
    }
    return GridView.builder(
      controller: _scroll,
      gridDelegate: const SliverGridDelegateWithMaxCrossAxisExtent(
        maxCrossAxisExtent: AppSize.cardW,
        mainAxisExtent: 208,
        mainAxisSpacing: AppSpace.s3,
        crossAxisSpacing: AppSpace.s3,
      ),
      itemCount: s.items.length,
      itemBuilder: (_, i) {
        final a = s.items[i];
        return AlbumCard(
          artworkId: a.artworkId,
          title: a.title,
          subtitle: a.artist,
          onTap: () => nav.open(Detail(DetailKind.album, a.id)),
        );
      },
    );
  }

  Widget _playlists(NavNotifier nav) {
    final lists = ref.watch(playlistsProvider).value ?? const <Playlist>[];
    return ListView(
      controller: _scroll,
      children: [
        Align(
          alignment: Alignment.centerLeft,
          child: Padding(
            padding: const EdgeInsets.only(bottom: AppSpace.s2),
            child: ArButton(
              S.newPlaylist,
              icon: 'plus',
              ghost: true,
              onTap: () async {
                final name = await promptText(
                  context,
                  S.newPlaylist,
                  S.playlistName,
                  S.create,
                );
                if (name == null || name.trim().isEmpty) return;
                try {
                  await ref.read(apiProvider).createPlaylist(name.trim());
                } on ApiException catch (e) {
                  ref.read(toastProvider.notifier).error(e);
                }
              },
            ),
          ),
        ),
        ArtistRowFavorites(
          onTap: () => nav.open(const Detail(DetailKind.liked)),
        ),
        for (final p in lists)
          ArtistRow(
            name: p.name,
            subtitle: S.songCount(p.itemCount),
            onTap: () => nav.open(Detail(DetailKind.playlist, p.id)),
          ),
      ],
    );
  }
}

class ArtistRowFavorites extends StatelessWidget {
  const ArtistRowFavorites({super.key, required this.onTap});
  final VoidCallback onTap;
  @override
  Widget build(BuildContext context) =>
      ArtistRow(name: S.favorites, subtitle: 'Me gusta', onTap: onTap);
}
