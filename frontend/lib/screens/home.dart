import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../data/models.dart';
import '../l10n/strings.dart';
import '../state/providers.dart';
import '../widgets/rows.dart';

/// Plays [t] inside its album when we know it, or alone.
void playTrack(WidgetRef ref, Track t) {
  final n = ref.read(playerProvider.notifier);
  t.albumId.isEmpty
      ? n.playContext('track:${t.id}')
      : n.playContext('album:${t.albumId}', start: t.id);
}

class HomeScreen extends ConsumerWidget {
  const HomeScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final home = ref.watch(homeProvider);
    final lists = ref.watch(playlistsProvider).value ?? const <Playlist>[];
    final nav = ref.read(navProvider.notifier);
    Widget shelf(List<Track> ts) => Shelf(
      children: [
        for (final t in ts)
          AlbumCard(
            albumId: t.albumId,
            seed: t.album,
            title: t.title,
            subtitle: t.artist,
            onTap: () => playTrack(ref, t),
          ),
      ],
    );
    return ScreenScroll(
      children: [
        ScreenHeader(S.greeting(DateTime.now().hour)),
        ...home.when(
          loading: () => [const SkeletonRows()],
          error: (_, _) => [const EmptyNote(S.failed, icon: 'server')],
          data: (h) => [
            if (h.recentlyPlayed.isNotEmpty) ...[
              const SectionLabel(S.recentlyPlayed, first: true),
              shelf(h.recentlyPlayed),
            ],
            if (h.mostPlayed.isNotEmpty) ...[
              const SectionLabel(S.mostPlayed),
              shelf(h.mostPlayed),
            ],
            if (h.recentlyAdded.isNotEmpty) ...[
              const SectionLabel(S.recentlyAdded),
              for (final t in h.recentlyAdded)
                TrackRow(track: t, onTap: () => playTrack(ref, t)),
            ],
          ],
        ),
        const SectionLabel(S.yourPlaylists),
        Shelf(
          children: [
            AlbumCard(
              albumId: '',
              title: S.favorites,
              subtitle: 'Me gusta',
              onTap: () => nav.open(const Detail(DetailKind.liked)),
            ),
            for (final p in lists)
              AlbumCard(
                albumId: '',
                title: p.name,
                subtitle: 'Playlist · ${S.songCount(p.itemCount)}',
                onTap: () => nav.open(Detail(DetailKind.playlist, p.id)),
              ),
          ],
        ),
      ],
    );
  }
}
