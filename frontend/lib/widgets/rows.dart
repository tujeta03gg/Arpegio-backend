import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../data/models.dart';
import '../design/chamfer.dart';
import '../design/icons.dart';
import '../design/tokens.dart';
import '../design/typography.dart';
import '../l10n/strings.dart';
import '../state/providers.dart';
import 'artwork.dart';
import 'controls.dart';
import 'format.dart';
import 'track_menu.dart';

/// 56 px row: cover, title, "Artist · Album", duration, menu.
class TrackRow extends ConsumerWidget {
  const TrackRow({
    super.key,
    required this.track,
    required this.onTap,
    this.leading,
    this.tag,
    this.showAlbum = true,
  });
  final Track track;
  final VoidCallback onTap;

  /// Replaces the cover (e.g. track number in an album).
  final Widget? leading;

  /// Small label next to the title, e.g. "Siguiente".
  final String? tag;
  final bool showAlbum;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final isCurrent = ref.watch(
      playerProvider.select((p) => p.track?.id == track.id),
    );
    final playing = ref.watch(playerProvider.select((p) => p.playing));
    final missing = track.missing; // file gone: dimmed and not playable
    final sub = missing
        ? S.missingFile
        : showAlbum && track.album.isNotEmpty
        ? '${track.artist} · ${track.album}'
        : track.artist;
    return Pressable(
      onTap: missing ? null : onTap,
      onLongPress: () => showTrackMenu(context, ref, track),
      scale: 1,
      pressedColor: AppColors.void200,
      label: track.title,
      child: Container(
        height: AppSize.rowH,
        padding: const EdgeInsets.only(left: AppSpace.s2),
        decoration: BoxDecoration(
          color: isCurrent ? AppColors.neonCyanWash : null,
          border: const Border(bottom: BorderSide(color: AppColors.void300)),
        ),
        child: Row(
          children: [
            leading ??
                Artwork(
                  artworkId: track.artworkId,
                  seed: track.album,
                  size: AppSize.coverRow,
                ),
            const SizedBox(width: AppSpace.s3),
            Expanded(
              child: Column(
                mainAxisAlignment: MainAxisAlignment.center,
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    children: [
                      if (isCurrent) ...[
                        Equalizer(
                          active: playing && ref.watch(visualizerProvider),
                        ),
                        const SizedBox(width: AppSpace.s2),
                      ],
                      Flexible(
                        child: Text(
                          track.title,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: AppText.body.copyWith(
                            color: missing
                                ? AppColors.inkDim
                                : isCurrent
                                ? AppColors.neonCyan
                                : AppColors.ink,
                          ),
                        ),
                      ),
                      if (tag case final tag?) ...[
                        const SizedBox(width: AppSpace.s2),
                        TagChip(tag),
                      ],
                    ],
                  ),
                  Text(
                    sub,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: AppText.bodySm.copyWith(
                      color: missing ? AppColors.inkDim : AppColors.inkMuted,
                    ),
                  ),
                ],
              ),
            ),
            const SizedBox(width: AppSpace.s3),
            if (track.durationMs > 0 && !missing)
              Text(
                fmtTime(track.durationMs),
                style: AppText.monoTime.copyWith(color: AppColors.inkMuted),
              ),
            ArIconButton(
              'more',
              size: AppSize.iconSm,
              muted: true,
              label: 'Más opciones',
              onTap: () => showTrackMenu(context, ref, track),
            ),
          ],
        ),
      ),
    );
  }
}

class TagChip extends StatelessWidget {
  const TagChip(this.text, {super.key});
  final String text;

  @override
  Widget build(BuildContext context) => Chamfered(
    small: true,
    child: ColoredBox(
      color: AppColors.void300,
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: AppSpace.s2),
        child: Text(
          text.toUpperCase(),
          style: AppText.label.copyWith(color: AppColors.ink),
        ),
      ),
    ),
  );
}

/// 152 wide card with a 136 chamfered cover.
class AlbumCard extends StatelessWidget {
  const AlbumCard({
    super.key,
    required this.artworkId,
    required this.title,
    required this.subtitle,
    required this.onTap,
    this.seed,
  });
  final String artworkId;
  final String title;
  final String subtitle;
  final VoidCallback onTap;
  final String? seed;

  @override
  Widget build(BuildContext context) => Pressable(
    onTap: onTap,
    scale: 0.97,
    label: title,
    child: SizedBox(
      width: AppSize.cardW,
      child: Chamfered(
        child: ColoredBox(
          color: AppColors.void200,
          child: Padding(
            padding: const EdgeInsets.all(AppSpace.s2),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              mainAxisSize: MainAxisSize.min,
              children: [
                Artwork(
                  artworkId: artworkId,
                  seed: seed ?? title,
                  size: AppSize.cardCover,
                  chamfer: true,
                ),
                const SizedBox(height: AppSpace.s2),
                Padding(
                  padding: const EdgeInsets.fromLTRB(
                    AppSpace.s1,
                    0,
                    AppSpace.s1,
                    AppSpace.s1,
                  ),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(
                        title,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: AppText.body,
                      ),
                      Text(
                        subtitle,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: AppText.bodySm.copyWith(
                          color: AppColors.inkMuted,
                        ),
                      ),
                    ],
                  ),
                ),
              ],
            ),
          ),
        ),
      ),
    ),
  );
}

/// Horizontal strip of cards.
class Shelf extends StatelessWidget {
  const Shelf({super.key, required this.children});
  final List<Widget> children;

  @override
  Widget build(BuildContext context) => SizedBox(
    height: 208,
    child: ListView.separated(
      scrollDirection: Axis.horizontal,
      itemCount: children.length,
      separatorBuilder: (_, _) => const SizedBox(width: AppSpace.s3),
      itemBuilder: (_, i) => children[i],
    ),
  );
}

class SectionLabel extends StatelessWidget {
  const SectionLabel(this.text, {super.key, this.first = false});
  final String text;
  final bool first;

  @override
  Widget build(BuildContext context) => Padding(
    padding: EdgeInsets.only(top: first ? 0 : AppSpace.s6, bottom: AppSpace.s2),
    child: Text(
      text.toUpperCase(),
      style: AppText.label.copyWith(color: AppColors.inkMuted),
    ),
  );
}

/// Title of a screen with an optional trailing widget.
class ScreenHeader extends StatelessWidget {
  const ScreenHeader(this.title, {super.key, this.trailing});
  final String title;
  final Widget? trailing;

  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.only(bottom: AppSpace.s3),
    child: Row(
      children: [
        Expanded(
          child: Text(
            title,
            style: AppText.titleLg,
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
          ),
        ),
        ?trailing,
      ],
    ),
  );
}

/// Padded scrollable body shared by the screens.
class ScreenScroll extends StatelessWidget {
  const ScreenScroll({super.key, required this.children, this.controller});
  final List<Widget> children;
  final ScrollController? controller;

  @override
  Widget build(BuildContext context) => ListView(
    controller: controller,
    padding: const EdgeInsets.all(AppSpace.s4),
    children: children,
  );
}

class EmptyNote extends StatelessWidget {
  const EmptyNote(this.title, {super.key, this.hint, this.icon = 'note'});
  final String title;
  final String? hint;
  final String icon;

  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.symmetric(vertical: AppSpace.s8),
    child: Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        ArpegioIcon(icon, size: 40, color: AppColors.inkMuted),
        const SizedBox(height: AppSpace.s3),
        Text(title, style: AppText.bodyLg, textAlign: TextAlign.center),
        if (hint != null)
          Text(
            hint!,
            style: AppText.bodySm.copyWith(color: AppColors.inkMuted),
            textAlign: TextAlign.center,
          ),
      ],
    ),
  );
}

/// Simple placeholder rows while data loads (no full-screen spinner).
class SkeletonRows extends StatelessWidget {
  const SkeletonRows({super.key, this.count = 5});
  final int count;

  @override
  Widget build(BuildContext context) => Column(
    children: [
      for (var i = 0; i < count; i++)
        Container(
          height: AppSize.rowH,
          padding: const EdgeInsets.only(left: AppSpace.s2),
          alignment: Alignment.centerLeft,
          child: Row(
            children: [
              const SizedBox.square(
                dimension: AppSize.coverRow,
                child: ColoredBox(color: AppColors.void200),
              ),
              const SizedBox(width: AppSpace.s3),
              Expanded(
                child: Column(
                  mainAxisAlignment: MainAxisAlignment.center,
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    FractionallySizedBox(
                      widthFactor: 0.5,
                      child: Container(height: 12, color: AppColors.void200),
                    ),
                    const SizedBox(height: AppSpace.s2),
                    FractionallySizedBox(
                      widthFactor: 0.3,
                      child: Container(height: 10, color: AppColors.void200),
                    ),
                  ],
                ),
              ),
            ],
          ),
        ),
    ],
  );
}

/// Wrapped grid of [AlbumCard]s (non-scrolling; lives inside a ListView).
class CardWrap extends StatelessWidget {
  const CardWrap({super.key, required this.children});
  final List<Widget> children;

  @override
  Widget build(BuildContext context) =>
      Wrap(spacing: AppSpace.s3, runSpacing: AppSpace.s3, children: children);
}
