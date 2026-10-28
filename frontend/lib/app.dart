import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'design/theme.dart';
import 'design/tokens.dart';
import 'screens/detail.dart';
import 'screens/home.dart';
import 'screens/library.dart';
import 'screens/now_playing.dart';
import 'screens/queue.dart';
import 'screens/search.dart';
import 'screens/settings.dart';
import 'state/providers.dart';
import 'widgets/frame.dart';

class ArpegioApp extends ConsumerWidget {
  const ArpegioApp({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    ref.watch(syncProvider); // opens the WebSocket
    final calm = ref.watch(calmProvider);
    return MaterialApp(
      debugShowCheckedModeBanner: false,
      title: 'Arpegio',
      theme: buildTheme(calm: calm),
      home: const Shell(),
    );
  }
}

class _KeyIntent extends Intent {
  const _KeyIntent(this.run);
  final void Function(WidgetRef ref) run;
}

/// Ignores the shortcut while a text field has focus.
class _KeyAction extends Action<_KeyIntent> {
  _KeyAction(this.ref);
  final WidgetRef ref;

  @override
  bool isEnabled(_KeyIntent intent) {
    final f = FocusManager.instance.primaryFocus?.context;
    return f == null || f.findAncestorWidgetOfExactType<EditableText>() == null;
  }

  @override
  Object? invoke(_KeyIntent intent) {
    intent.run(ref);
    return null;
  }
}

/// 800x480 frame: status bar, rail, content and mini player. Scales to fit in dev windows.
class Shell extends ConsumerWidget {
  const Shell({super.key});

  static final _keys = <ShortcutActivator, _KeyIntent>{
    const SingleActivator(LogicalKeyboardKey.space): _KeyIntent(
      (r) => r.read(playerProvider.notifier).toggle(),
    ),
    const SingleActivator(LogicalKeyboardKey.arrowLeft): _KeyIntent(
      (r) => r.read(playerProvider.notifier).previous(),
    ),
    const SingleActivator(LogicalKeyboardKey.arrowRight): _KeyIntent(
      (r) => r.read(playerProvider.notifier).next(),
    ),
    const SingleActivator(LogicalKeyboardKey.escape): _KeyIntent((r) {
      final nav = r.read(navProvider.notifier);
      if (!nav.back() && r.read(navProvider).dest == Dest.now) {
        nav.go(r.read(navProvider).last);
      }
    }),
    for (var i = 0; i < Dest.values.length; i++)
      SingleActivator(LogicalKeyboardKey(LogicalKeyboardKey.digit1.keyId + i)):
          _KeyIntent((r) => r.read(navProvider.notifier).go(Dest.values[i])),
  };

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    assert(() {
      final s = MediaQuery.sizeOf(context);
      if (s != const Size(AppSize.screenW, AppSize.screenH)) {
        debugPrint(
          'Arpegio: pantalla de ${s.width}x${s.height}; el diseño es 800x480 (escalado para desarrollo).',
        );
      }
      return true;
    }());
    return Shortcuts(
      shortcuts: _keys,
      child: Actions(
        actions: {_KeyIntent: _KeyAction(ref)},
        child: Focus(
          autofocus: true,
          child: Material(
            color: Colors.black,
            child: FittedBox(
              child: SizedBox(
                width: AppSize.screenW,
                height: AppSize.screenH,
                child: const _Frame(),
              ),
            ),
          ),
        ),
      ),
    );
  }
}

class _Frame extends ConsumerWidget {
  const _Frame();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final nav = ref.watch(navProvider);
    final connected = ref.watch(connectedProvider);
    final now = nav.dest == Dest.now;
    final detail = nav.detail;
    return ClipRect(
      child: Stack(
        children: [
          Column(
            children: [
              const StatusBar(),
              Expanded(
                child: Row(
                  children: [
                    const NavRail(),
                    Expanded(
                      child: Column(
                        children: [
                          if (!connected) const ConnectionBanner(),
                          Expanded(
                            child: Stack(
                              children: [
                                // The five fixed destinations stay alive; details cover them.
                                IndexedStack(
                                  index: now ? 0 : nav.dest.index - 1,
                                  children: const [
                                    HomeScreen(),
                                    SearchScreen(),
                                    LibraryScreen(),
                                    QueueScreen(),
                                    SettingsScreen(),
                                  ],
                                ),
                                if (detail != null)
                                  Positioned.fill(
                                    child: DetailScreen(
                                      key: ValueKey(
                                        '${detail.kind}${detail.id}${nav.stack.length}',
                                      ),
                                      detail: detail,
                                    ),
                                  ),
                                // Now Playing slides up from the bottom.
                                Positioned.fill(
                                  child: IgnorePointer(
                                    ignoring: !now,
                                    child: AnimatedSlide(
                                      offset: now
                                          ? Offset.zero
                                          : const Offset(0, 1),
                                      duration: AppMotion.base,
                                      curve: Curves.easeOut,
                                      child: TickerMode(
                                        enabled: now,
                                        child: const ColoredBox(
                                          color: AppColors.void000,
                                          child: NowPlayingScreen(),
                                        ),
                                      ),
                                    ),
                                  ),
                                ),
                              ],
                            ),
                          ),
                        ],
                      ),
                    ),
                  ],
                ),
              ),
              if (!now) const MiniPlayer(),
            ],
          ),
          const Toast(),
        ],
      ),
    );
  }
}
