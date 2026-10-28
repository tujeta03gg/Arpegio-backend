import 'dart:async';
import 'dart:io' show Directory, File;

import 'package:flutter/foundation.dart' show kIsWeb;
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../design/chamfer.dart';
import '../design/icons.dart';
import '../design/tokens.dart';
import '../design/typography.dart';
import '../l10n/strings.dart';
import '../state/providers.dart';
import 'artwork.dart';
import 'controls.dart';
import 'format.dart';

/// Battery percentage from /sys/class/power_supply, or null when there is none.
final batteryProvider = StreamProvider<int?>((ref) async* {
  Future<int?> read() async {
    if (kIsWeb) return null;
    try {
      final dir = Directory('/sys/class/power_supply');
      if (!dir.existsSync()) return null;
      for (final e in dir.listSync()) {
        final f = File('${e.path}/capacity');
        if (f.existsSync()) return int.tryParse(f.readAsStringSync().trim());
      }
    } on Object {
      return null;
    }
    return null;
  }

  yield await read();
  yield* Stream.periodic(const Duration(minutes: 1)).asyncMap((_) => read());
});

/// Wall clock "HH:MM" that refreshes on the minute.
class ClockText extends ConsumerStatefulWidget {
  const ClockText({super.key});
  @override
  ConsumerState<ClockText> createState() => _ClockTextState();
}

/// Source of "now"; tests override it to keep goldens stable.
final clockProvider = Provider<DateTime Function()>((_) => DateTime.now);

class _ClockTextState extends ConsumerState<ClockText> {
  Timer? _t;
  late DateTime _now = ref.read(clockProvider)();

  @override
  void initState() {
    super.initState();
    _schedule();
  }

  void _schedule() {
    final n = ref.read(clockProvider)();
    _t = Timer(Duration(seconds: 61 - n.second), () {
      if (!mounted) return;
      setState(() => _now = ref.read(clockProvider)());
      _schedule();
    });
  }

  @override
  void dispose() {
    _t?.cancel();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => Text(
    '${_now.hour.toString().padLeft(2, '0')}:${_now.minute.toString().padLeft(2, '0')}',
    style: AppText.monoStatus.copyWith(color: AppColors.inkMuted),
  );
}

/// 28 px bar: clock, indexing progress, arpegiod state, network, battery.
class StatusBar extends ConsumerWidget {
  const StatusBar({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final connected = ref.watch(connectedProvider);
    final lib = ref.watch(libraryStatusProvider);
    final battery = ref.watch(batteryProvider).value;
    final indexing = connected && lib.scanning;
    final dot = !connected
        ? AppColors.signalError
        : (indexing ? AppColors.neonAmber : AppColors.signalOk);
    final word = !connected ? S.offline : S.connected;
    final style = AppText.monoStatus.copyWith(color: AppColors.inkMuted);
    return Container(
      height: AppSize.statusbarH,
      padding: const EdgeInsets.symmetric(horizontal: AppSpace.s4),
      decoration: const BoxDecoration(
        color: AppColors.void100,
        border: Border(bottom: BorderSide(color: AppColors.void300)),
      ),
      child: Row(
        children: [
          const ClockText(),
          const Spacer(),
          if (indexing) ...[
            Text(
              S
                  .indexing(lib.done, lib.total)
                  .replaceFirstMapped(
                    RegExp(r'/(\d+)$'),
                    (m) => '/${fmtCount(int.parse(m[1]!))}',
                  ),
              style: style.copyWith(color: AppColors.neonAmber),
            ),
            const SizedBox(width: AppSpace.s3),
          ],
          DecoratedBox(
            decoration: BoxDecoration(color: dot, shape: BoxShape.circle),
            child: const SizedBox.square(dimension: 8),
          ),
          const SizedBox(width: AppSpace.s1),
          Text(word, style: style),
          const SizedBox(width: AppSpace.s3),
          ArpegioIcon(
            'wifi',
            size: AppSize.iconSm,
            color: connected ? AppColors.inkMuted : AppColors.inkDim,
          ),
          if (battery != null) ...[
            const SizedBox(width: AppSpace.s3),
            ArpegioIcon(
              'battery',
              size: AppSize.iconSm,
              color: battery <= 15 ? AppColors.neonAmber : AppColors.inkMuted,
            ),
            const SizedBox(width: AppSpace.s1),
            Text(
              '$battery%',
              style: style.copyWith(
                color: battery <= 15 ? AppColors.neonAmber : AppColors.inkMuted,
              ),
            ),
          ],
        ],
      ),
    );
  }
}

/// Left rail with the 5 fixed destinations.
class NavRail extends ConsumerWidget {
  const NavRail({super.key});

  static const items = <(Dest, String, String)>[
    (Dest.home, 'home', S.home),
    (Dest.search, 'search', S.search),
    (Dest.library, 'library', S.library),
    (Dest.queue, 'queue', S.queue),
    (Dest.settings, 'settings', S.settings),
  ];

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final dest = ref.watch(navProvider.select((n) => n.dest));
    return Container(
      width: AppSize.railW,
      decoration: const BoxDecoration(
        color: AppColors.void100,
        border: Border(right: BorderSide(color: AppColors.void300)),
      ),
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          for (final (d, icon, label) in items)
            Padding(
              padding: const EdgeInsets.symmetric(vertical: AppSpace.s1 / 2),
              child: Pressable(
                onTap: () => ref.read(navProvider.notifier).go(d),
                label: label,
                child: Container(
                  width: 64,
                  height: 60,
                  color: d == dest ? AppColors.neonCyanWash : null,
                  child: Column(
                    mainAxisAlignment: MainAxisAlignment.center,
                    children: [
                      ArpegioIcon(
                        icon,
                        color: d == dest
                            ? AppColors.neonCyan
                            : AppColors.inkMuted,
                      ),
                      const SizedBox(height: AppSpace.s1),
                      Text(
                        label,
                        style: AppText.caption.copyWith(
                          color: d == dest
                              ? AppColors.neonCyan
                              : AppColors.inkMuted,
                        ),
                      ),
                    ],
                  ),
                ),
              ),
            ),
        ],
      ),
    );
  }
}

/// 64 px bar at the bottom: opens Now Playing when tapped.
class MiniPlayer extends ConsumerWidget {
  const MiniPlayer({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final p = ref.watch(playerProvider);
    final t = p.track;
    if (t == null) return const SizedBox.shrink();
    final on = ref.watch(connectedProvider);
    void open() => ref.read(navProvider.notifier).go(Dest.now);
    return Container(
      height: AppSize.miniplayerH,
      decoration: const BoxDecoration(
        color: AppColors.void100,
        border: Border(top: BorderSide(color: AppColors.void300)),
      ),
      child: Column(
        children: [
          Consumer(
            builder: (_, ref, _) {
              final pos = ref.watch(positionProvider);
              return ProgressBar(
                line: true,
                positionMs: pos,
                durationMs: p.durationMs > 0 ? p.durationMs : t.durationMs,
              );
            },
          ),
          Expanded(
            child: Padding(
              padding: const EdgeInsets.only(
                left: AppSpace.s4 + 4,
                right: AppSpace.s4,
              ),
              child: Row(
                children: [
                  Pressable(
                    onTap: open,
                    label: 'Abrir Reproduciendo',
                    child: Artwork(
                      artworkId: t.artworkId,
                      seed: t.album,
                      size: AppSize.coverMini,
                    ),
                  ),
                  const SizedBox(width: AppSpace.s3),
                  Expanded(
                    child: Pressable(
                      onTap: open,
                      scale: 1,
                      child: Column(
                        mainAxisAlignment: MainAxisAlignment.center,
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Text(
                            t.title,
                            maxLines: 1,
                            overflow: TextOverflow.ellipsis,
                            style: AppText.body,
                          ),
                          Text(
                            t.artist,
                            maxLines: 1,
                            overflow: TextOverflow.ellipsis,
                            style: AppText.bodySm.copyWith(
                              color: AppColors.inkMuted,
                            ),
                          ),
                        ],
                      ),
                    ),
                  ),
                  PlayButton(
                    small: true,
                    playing: p.playing,
                    onTap: on ? ref.read(playerProvider.notifier).toggle : null,
                  ),
                  ArIconButton(
                    'next',
                    label: 'Siguiente',
                    onTap: on ? ref.read(playerProvider.notifier).next : null,
                  ),
                ],
              ),
            ),
          ),
        ],
      ),
    );
  }
}

/// Shown while arpegiod cannot be reached.
class ConnectionBanner extends StatelessWidget {
  const ConnectionBanner({super.key});

  @override
  Widget build(BuildContext context) => Container(
    height: AppSize.touchMin - 8,
    padding: const EdgeInsets.symmetric(horizontal: AppSpace.s4),
    color: AppColors.void200,
    child: Row(
      children: [
        const ArpegioIcon(
          'server',
          size: AppSize.iconSm,
          color: AppColors.signalError,
        ),
        const SizedBox(width: AppSpace.s2),
        Text(
          S.noConnection,
          style: AppText.bodySm.copyWith(color: AppColors.signalError),
        ),
      ],
    ),
  );
}

/// Transient notice at the bottom (3 s).
class Toast extends ConsumerWidget {
  const Toast({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final msg = ref.watch(toastProvider);
    if (msg == null) return const SizedBox.shrink();
    return Align(
      alignment: Alignment.bottomCenter,
      child: Padding(
        padding: const EdgeInsets.all(AppSpace.s4),
        child: DecoratedBox(
          decoration: const ShapeDecoration(
            color: AppColors.void200,
            shape: chamferMd,
            shadows: AppGlow.lift,
          ),
          child: Padding(
            padding: const EdgeInsets.symmetric(
              horizontal: AppSpace.s4,
              vertical: AppSpace.s3,
            ),
            child: Text(msg, style: AppText.body),
          ),
        ),
      ),
    );
  }
}
