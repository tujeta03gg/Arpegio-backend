import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../data/api_client.dart';
import '../data/models.dart';
import '../design/chamfer.dart';
import '../design/icons.dart';
import '../design/tokens.dart';
import '../design/typography.dart';
import '../l10n/strings.dart';
import '../state/providers.dart';
import 'controls.dart';

/// Bottom sheet with the actions of a track.
Future<void> showTrackMenu(BuildContext context, WidgetRef ref, Track t) {
  return showModalBottomSheet<void>(
    context: context,
    backgroundColor: Colors.transparent,
    barrierColor: const Color(0x99000000),
    isScrollControlled: true,
    constraints: const BoxConstraints(maxWidth: AppSize.screenW),
    builder: (ctx) => _TrackMenu(track: t, parentRef: ref),
  );
}

/// Bottom sheet shell shared by the menus: elevated surface with `lift`.
class SheetFrame extends StatelessWidget {
  const SheetFrame({
    super.key,
    required this.title,
    this.subtitle,
    required this.children,
  });
  final String title;
  final String? subtitle;
  final List<Widget> children;

  @override
  Widget build(BuildContext context) => SafeArea(
    child: ConstrainedBox(
      constraints: const BoxConstraints(maxHeight: 380),
      child: DecoratedBox(
        decoration: const ShapeDecoration(
          color: AppColors.void200,
          shape: chamferMd,
          shadows: AppGlow.lift,
        ),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(
                AppSpace.s4,
                AppSpace.s3,
                AppSpace.s4,
                AppSpace.s2,
              ),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    title,
                    style: AppText.body,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                  ),
                  if (subtitle != null)
                    Text(
                      subtitle!,
                      style: AppText.bodySm.copyWith(color: AppColors.inkMuted),
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                    ),
                ],
              ),
            ),
            const Divider(height: 1, color: AppColors.void300),
            Flexible(child: ListView(shrinkWrap: true, children: children)),
          ],
        ),
      ),
    ),
  );
}

class MenuItem extends StatelessWidget {
  const MenuItem(
    this.icon,
    this.text, {
    super.key,
    required this.onTap,
    this.color = AppColors.ink,
  });
  final String icon;
  final String text;
  final VoidCallback onTap;
  final Color color;

  @override
  Widget build(BuildContext context) => Pressable(
    onTap: onTap,
    scale: 1,
    pressedColor: AppColors.void300,
    child: SizedBox(
      height: AppSize.touchMin,
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: AppSpace.s4),
        child: Row(
          children: [
            ArpegioIcon(icon, size: AppSize.iconSm, color: color),
            const SizedBox(width: AppSpace.s3),
            Expanded(
              child: Text(text, style: AppText.body.copyWith(color: color)),
            ),
          ],
        ),
      ),
    ),
  );
}

class _TrackMenu extends StatelessWidget {
  const _TrackMenu({required this.track, required this.parentRef});
  final Track track;
  final WidgetRef parentRef;

  @override
  Widget build(BuildContext context) {
    final ref = parentRef;
    final liked = ref.read(likesProvider).contains(track.id);
    void close() => Navigator.of(context).pop();
    return SheetFrame(
      title: track.title,
      subtitle: track.artist,
      children: [
        MenuItem(
          'play',
          S.playNext,
          onTap: () {
            close();
            ref.read(queueProvider.notifier).add(track.id, next: true);
          },
        ),
        MenuItem(
          'queue',
          S.addToQueue,
          onTap: () {
            close();
            ref.read(queueProvider.notifier).add(track.id);
          },
        ),
        MenuItem(
          'plus',
          S.addToPlaylist,
          onTap: () {
            close();
            showPlaylistPicker(context, ref, track);
          },
        ),
        if (track.albumId.isNotEmpty)
          MenuItem(
            'library',
            S.goAlbum,
            onTap: () {
              close();
              ref
                  .read(navProvider.notifier)
                  .open(Detail(DetailKind.album, track.albumId));
            },
          ),
        if (track.artistId != null)
          MenuItem(
            'note',
            S.goArtist,
            onTap: () {
              close();
              ref
                  .read(navProvider.notifier)
                  .open(Detail(DetailKind.artist, track.artistId!));
            },
          ),
        MenuItem(
          'wifi',
          S.startRadio,
          onTap: () {
            close();
            ref.read(playerProvider.notifier).radio('track:${track.id}');
          },
        ),
        MenuItem(
          liked ? 'heart-fill' : 'heart',
          liked ? S.unlike : S.like,
          color: liked ? AppColors.neonMagenta : AppColors.ink,
          onTap: () {
            close();
            ref.read(likesProvider.notifier).toggle(track.id);
          },
        ),
      ],
    );
  }
}

Future<void> showPlaylistPicker(BuildContext context, WidgetRef ref, Track t) {
  return showModalBottomSheet<void>(
    context: context,
    backgroundColor: Colors.transparent,
    barrierColor: const Color(0x99000000),
    isScrollControlled: true,
    constraints: const BoxConstraints(maxWidth: AppSize.screenW),
    builder: (ctx) => Consumer(
      builder: (ctx, ref, _) {
        final lists = ref.watch(playlistsProvider);
        return SheetFrame(
          title: S.addToPlaylist,
          subtitle: t.title,
          children: [
            MenuItem(
              'plus',
              S.newPlaylist,
              onTap: () async {
                final name = await promptText(
                  ctx,
                  S.newPlaylist,
                  S.playlistName,
                  S.create,
                );
                if (name == null || name.trim().isEmpty) return;
                try {
                  final api = ref.read(apiProvider);
                  final p = await api.createPlaylist(name.trim());
                  await api.playlistAdd(p.id, [t.id]);
                  ref.read(toastProvider.notifier).show(S.addedPlaylist);
                } on ApiException catch (e) {
                  ref.read(toastProvider.notifier).error(e);
                }
                if (ctx.mounted) Navigator.of(ctx).pop();
              },
            ),
            for (final p in lists.value ?? const <Playlist>[])
              MenuItem(
                'queue',
                p.name,
                onTap: () async {
                  Navigator.of(ctx).pop();
                  try {
                    await ref.read(apiProvider).playlistAdd(p.id, [t.id]);
                    ref.read(toastProvider.notifier).show(S.addedPlaylist);
                  } on ApiException catch (e) {
                    ref.read(toastProvider.notifier).error(e);
                  }
                },
              ),
          ],
        );
      },
    ),
  );
}

/// Small dialog asking for one line of text (physical keyboard). Returns null on cancel.
Future<String?> promptText(
  BuildContext context,
  String title,
  String hint,
  String action, {
  String initial = '',
}) {
  final ctl = TextEditingController(text: initial);
  return showDialog<String>(
    context: context,
    barrierColor: const Color(0x99000000),
    builder: (ctx) => Dialog(
      backgroundColor: AppColors.void200,
      shape: chamferMd,
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 480),
        child: Padding(
          padding: const EdgeInsets.all(AppSpace.s4),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(title, style: AppText.title),
              const SizedBox(height: AppSpace.s3),
              TextField(
                controller: ctl,
                autofocus: true,
                style: AppText.body,
                cursorColor: AppColors.neonCyan,
                onSubmitted: (v) => Navigator.of(ctx).pop(v),
                decoration: InputDecoration(
                  hintText: hint,
                  hintStyle: AppText.body.copyWith(color: AppColors.inkMuted),
                  enabledBorder: const UnderlineInputBorder(
                    borderSide: BorderSide(color: AppColors.void300),
                  ),
                  focusedBorder: const UnderlineInputBorder(
                    borderSide: BorderSide(color: AppColors.neonCyan),
                  ),
                ),
              ),
              const SizedBox(height: AppSpace.s4),
              Row(
                mainAxisAlignment: MainAxisAlignment.end,
                children: [
                  ArButton(
                    S.cancel,
                    ghost: true,
                    onTap: () => Navigator.of(ctx).pop(),
                  ),
                  const SizedBox(width: AppSpace.s2),
                  ArButton(
                    action,
                    onTap: () => Navigator.of(ctx).pop(ctl.text),
                  ),
                ],
              ),
            ],
          ),
        ),
      ),
    ),
  );
}

/// Yes/no confirmation. Returns true when confirmed.
Future<bool> confirmDialog(
  BuildContext context,
  String text,
  String action,
) async {
  final r = await showDialog<bool>(
    context: context,
    barrierColor: const Color(0x99000000),
    builder: (ctx) => Dialog(
      backgroundColor: AppColors.void200,
      shape: chamferMd,
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 420),
        child: Padding(
          padding: const EdgeInsets.all(AppSpace.s4),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(text, style: AppText.bodyLg),
              const SizedBox(height: AppSpace.s4),
              Row(
                mainAxisAlignment: MainAxisAlignment.end,
                children: [
                  ArButton(
                    S.cancel,
                    ghost: true,
                    onTap: () => Navigator.of(ctx).pop(false),
                  ),
                  const SizedBox(width: AppSpace.s2),
                  ArButton(action, onTap: () => Navigator.of(ctx).pop(true)),
                ],
              ),
            ],
          ),
        ),
      ),
    ),
  );
  return r ?? false;
}
