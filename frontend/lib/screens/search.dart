import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../data/models.dart';
import '../design/tokens.dart';
import '../l10n/strings.dart';
import '../state/providers.dart';
import '../widgets/controls.dart';
import '../widgets/rows.dart';
import 'library.dart' show ArtistRow;

enum _Tab { songs, albums, artists }

class SearchScreen extends ConsumerStatefulWidget {
  const SearchScreen({super.key});
  @override
  ConsumerState<SearchScreen> createState() => _SearchScreenState();
}

class _SearchScreenState extends ConsumerState<SearchScreen> {
  Timer? _debounce;
  _Tab _tab = _Tab.songs;

  void _onChanged(String q) {
    _debounce?.cancel();
    _debounce = Timer(
      const Duration(milliseconds: 250),
      () => ref.read(searchQueryProvider.notifier).set(q),
    );
  }

  @override
  void dispose() {
    _debounce?.cancel();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final q = ref.watch(searchQueryProvider).trim();
    final res = ref.watch(searchResultProvider);
    final nav = ref.read(navProvider.notifier);
    final r = res.value ?? const SearchResult();
    final empty = switch (_tab) {
      _Tab.songs => r.tracks.isEmpty,
      _Tab.albums => r.albums.isEmpty,
      _Tab.artists => r.artists.isEmpty,
    };
    return ScreenScroll(
      children: [
        const ScreenHeader(S.search),
        SearchField(
          hint: S.searchHint,
          initial: ref.read(searchQueryProvider),
          onChanged: _onChanged,
        ),
        const SizedBox(height: AppSpace.s3),
        ChipBar<_Tab>(
          value: _tab,
          onChanged: (v) => setState(() => _tab = v),
          items: const [
            (_Tab.songs, S.songs),
            (_Tab.albums, S.albums),
            (_Tab.artists, S.artists),
          ],
        ),
        const SizedBox(height: AppSpace.s3),
        if (q.length < 2)
          const EmptyNote(S.searchHint, icon: 'search')
        else if (res.isLoading && res.value == null)
          const SkeletonRows(count: 4)
        else if (empty)
          EmptyNote(S.noResults(q), hint: S.noResultsHint, icon: 'search')
        else
          switch (_tab) {
            _Tab.songs => Column(
              children: [
                for (final h in r.tracks)
                  TrackRow(
                    track: h.toTrack(),
                    onTap: () => ref
                        .read(playerProvider.notifier)
                        .playContext('track:${h.id}'),
                  ),
              ],
            ),
            _Tab.albums => CardWrap(
              children: [
                for (final h in r.albums)
                  AlbumCard(
                    artworkId: h.artworkId,
                    title: h.title,
                    subtitle: h.artist,
                    onTap: () => nav.open(Detail(DetailKind.album, h.id)),
                  ),
              ],
            ),
            _Tab.artists => Column(
              children: [
                for (final h in r.artists)
                  ArtistRow(
                    name: h.title,
                    onTap: () => nav.open(Detail(DetailKind.artist, h.id)),
                  ),
              ],
            ),
          },
      ],
    );
  }
}
