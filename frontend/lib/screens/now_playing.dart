import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../design/icons.dart';
import '../design/tokens.dart';
import '../design/typography.dart';
import '../l10n/strings.dart';
import '../state/providers.dart';
import '../widgets/artwork.dart';
import '../widgets/controls.dart';
import '../widgets/rows.dart';
import '../widgets/track_menu.dart';

class NowPlayingScreen extends ConsumerWidget {
  const NowPlayingScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final p = ref.watch(playerProvider);
    final t = p.track;
    if (t == null) {
      return const Center(
        child: EmptyNote(S.nothingPlaying, hint: S.nothingPlayingHint),
      );
    }
    final liked = ref.watch(likesProvider.select((l) => l.contains(t.id)));
    final on = ref.watch(connectedProvider);
    final viz = ref.watch(visualizerProvider);
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: AppSpace.s6),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.center,
        children: [
          Artwork(
            albumId: t.albumId,
            seed: t.album,
            size: AppSize.coverNow,
            chamfer: true,
          ),
          const SizedBox(width: AppSpace.s6),
          Expanded(
            child: SizedBox(
              height: AppSize.coverNow,
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  Row(
                    children: [
                      Text(
                        (p.playing ? S.nowPlaying : S.paused).toUpperCase(),
                        style: AppText.label.copyWith(
                          color: AppColors.neonCyan,
                        ),
                      ),
                      const SizedBox(width: AppSpace.s2),
                      Equalizer(active: p.playing && viz),
                      const Spacer(),
                      ArIconButton(
                        'volume',
                        muted: true,
                        label: S.volume,
                        onTap: () => _volumeSheet(context),
                      ),
                    ],
                  ),
                  Row(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Expanded(
                        child: Text(
                          t.title,
                          maxLines: 2,
                          overflow: TextOverflow.ellipsis,
                          style: AppText.title,
                        ),
                      ),
                      ArIconButton(
                        liked ? 'heart-fill' : 'heart',
                        color: liked
                            ? AppColors.neonMagenta
                            : AppColors.inkMuted,
                        label: liked ? S.unlike : S.like,
                        onTap: on
                            ? () =>
                                  ref.read(likesProvider.notifier).toggle(t.id)
                            : null,
                      ),
                    ],
                  ),
                  Text(
                    t.artist,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: AppText.bodyLg.copyWith(color: AppColors.inkMuted),
                  ),
                  const SizedBox(height: AppSpace.s1),
                  // The API sends no codec/bitrate yet, so the technical line is just the album.
                  Text(
                    t.album.toUpperCase(),
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: AppText.monoStatus.copyWith(
                      color: AppColors.inkMuted,
                    ),
                  ),
                  const Spacer(),
                  Consumer(
                    builder: (_, ref, _) {
                      final pos = ref.watch(positionProvider);
                      return ProgressBar(
                        positionMs: pos,
                        durationMs: p.durationMs > 0
                            ? p.durationMs
                            : t.durationMs,
                        onSeek: on
                            ? ref.read(playerProvider.notifier).seek
                            : null,
                      );
                    },
                  ),
                  const TransportControls(),
                ],
              ),
            ),
          ),
        ],
      ),
    );
  }

  void _volumeSheet(BuildContext context) {
    showModalBottomSheet<void>(
      context: context,
      backgroundColor: Colors.transparent,
      barrierColor: const Color(0x99000000),
      constraints: const BoxConstraints(maxWidth: AppSize.screenW),
      builder: (_) => const _VolumeSheet(),
    );
  }
}

/// Volume slider; sends the level 150 ms after the last change.
class _VolumeSheet extends ConsumerStatefulWidget {
  const _VolumeSheet();
  @override
  ConsumerState<_VolumeSheet> createState() => _VolumeSheetState();
}

class _VolumeSheetState extends ConsumerState<_VolumeSheet> {
  late double _v = ref.read(playerProvider).volume.toDouble();
  Timer? _debounce;

  void _change(double v) {
    setState(() => _v = v);
    _debounce?.cancel();
    _debounce = Timer(
      const Duration(milliseconds: 150),
      () => ref.read(playerProvider.notifier).setVolume(v.round()),
    );
  }

  @override
  void dispose() {
    _debounce?.cancel();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => SheetFrame(
    title: S.volume,
    children: [
      Padding(
        padding: const EdgeInsets.all(AppSpace.s4),
        child: Row(
          children: [
            const ArpegioIcon('volume', color: AppColors.inkMuted),
            Expanded(
              child: SliderTheme(
                data: SliderTheme.of(context).copyWith(
                  trackHeight: AppSize.trackH,
                  activeTrackColor: AppColors.neonCyan,
                  inactiveTrackColor: AppColors.void300,
                  thumbColor: AppColors.neonCyan,
                  overlayColor: Colors.transparent,
                  thumbShape: const RoundSliderThumbShape(
                    enabledThumbRadius: AppSize.thumb / 2,
                    elevation: 0,
                    pressedElevation: 0,
                  ),
                ),
                child: Slider(value: _v, min: 0, max: 100, onChanged: _change),
              ),
            ),
            SizedBox(
              width: 40,
              child: Text(
                '${_v.round()}',
                textAlign: TextAlign.right,
                style: AppText.monoTime,
              ),
            ),
          ],
        ),
      ),
    ],
  );
}
