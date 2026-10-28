import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../design/chamfer.dart';
import '../design/icons.dart';
import '../design/theme.dart';
import '../design/tokens.dart';
import '../design/typography.dart';
import '../state/providers.dart';
import 'format.dart';

/// Tappable with press scale, pressed background and a keyboard focus glow.
class Pressable extends StatefulWidget {
  const Pressable({
    super.key,
    required this.child,
    this.onTap,
    this.onLongPress,
    this.scale = 0.96,
    this.pressedColor,
    this.circle = false,
    this.label,
  });
  final Widget child;
  final VoidCallback? onTap;
  final VoidCallback? onLongPress;
  final double scale;
  final Color? pressedColor;
  final bool circle;
  final String? label;

  @override
  State<Pressable> createState() => _PressableState();
}

class _PressableState extends State<Pressable> {
  bool _down = false;
  bool _focus = false;

  void _set(bool v) {
    if (_down != v) setState(() => _down = v);
  }

  @override
  Widget build(BuildContext context) {
    final enabled = widget.onTap != null;
    return Semantics(
      button: true,
      label: widget.label,
      enabled: enabled,
      child: FocusableActionDetector(
        enabled: enabled,
        onShowFocusHighlight: (v) => setState(() => _focus = v),
        actions: {
          ActivateIntent: CallbackAction<ActivateIntent>(
            onInvoke: (_) => widget.onTap?.call(),
          ),
        },
        child: GestureDetector(
          behavior: HitTestBehavior.opaque,
          onTapDown: enabled ? (_) => _set(true) : null,
          onTapUp: (_) => _set(false),
          onTapCancel: () => _set(false),
          onTap: widget.onTap,
          onLongPress: widget.onLongPress,
          child: AnimatedScale(
            scale: _down ? widget.scale : 1,
            duration: AppMotion.fast,
            child: DecoratedBox(
              decoration: BoxDecoration(
                color: _down ? widget.pressedColor : null,
                shape: widget.circle ? BoxShape.circle : BoxShape.rectangle,
                boxShadow: _focus ? context.glows.focus : null,
              ),
              child: widget.child,
            ),
          ),
        ),
      ),
    );
  }
}

/// 44x44 icon button. [active] paints it magenta with a 4 px dot (modes), [muted] uses inkMuted.
class ArIconButton extends StatelessWidget {
  const ArIconButton(
    this.icon, {
    super.key,
    this.onTap,
    this.active = false,
    this.muted = false,
    this.size = AppSize.iconMd,
    this.label,
    this.badge,
    this.color,
  });
  final String icon;
  final VoidCallback? onTap;
  final bool active;
  final bool muted;
  final double size;
  final String? label;
  final String? badge;
  final Color? color;

  @override
  Widget build(BuildContext context) {
    final c = onTap == null
        ? AppColors.inkDim
        : color ??
              (active
                  ? AppColors.neonMagenta
                  : (muted ? AppColors.inkMuted : AppColors.ink));
    return Pressable(
      onTap: onTap,
      circle: true,
      scale: 0.92,
      pressedColor: AppColors.void200,
      label: label,
      child: SizedBox.square(
        dimension: AppSize.touchMin,
        child: Stack(
          alignment: Alignment.center,
          children: [
            ArpegioIcon(icon, size: size, color: c),
            if (active)
              Positioned(
                bottom: 4,
                child: DecoratedBox(
                  decoration: BoxDecoration(color: c, shape: BoxShape.circle),
                  child: const SizedBox.square(dimension: 4),
                ),
              ),
            if (badge != null)
              Positioned(
                top: 2,
                right: 6,
                child: Text(
                  badge!,
                  style: AppText.monoStatus.copyWith(color: c),
                ),
              ),
          ],
        ),
      ),
    );
  }
}

/// Round cyan play button: 64 with glow, or 44 without it.
class PlayButton extends StatelessWidget {
  const PlayButton({
    super.key,
    required this.playing,
    this.onTap,
    this.small = false,
  });
  final bool playing;
  final VoidCallback? onTap;
  final bool small;

  @override
  Widget build(BuildContext context) {
    final d = small ? AppSize.touchMin : AppSize.touchPlay;
    return Pressable(
      onTap: onTap,
      circle: true,
      scale: 0.94,
      label: playing ? 'Pausa' : 'Reproducir',
      child: Container(
        width: d,
        height: d,
        decoration: BoxDecoration(
          color: onTap == null ? AppColors.void300 : AppColors.neonCyan,
          shape: BoxShape.circle,
          boxShadow: small || onTap == null ? null : context.glows.cyan,
        ),
        child: Center(
          child: ArpegioIcon(
            playing ? 'pause' : 'play',
            size: small ? AppSize.iconMd : 28,
            color: onTap == null ? AppColors.inkDim : AppColors.onNeon,
          ),
        ),
      ),
    );
  }
}

/// shuffle · (previous · play · next) · repeat, wired to the player.
class TransportControls extends ConsumerWidget {
  const TransportControls({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final p = ref.watch(playerProvider);
    final on = ref.watch(connectedProvider) && p.track != null;
    final n = ref.read(playerProvider.notifier);
    return Row(
      mainAxisAlignment: MainAxisAlignment.spaceBetween,
      children: [
        ArIconButton(
          'shuffle',
          muted: true,
          active: p.shuffle,
          label: 'Aleatorio',
          onTap: on ? () => n.setShuffle(!p.shuffle) : null,
        ),
        Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            ArIconButton(
              'prev',
              label: 'Anterior',
              onTap: on ? n.previous : null,
            ),
            const SizedBox(width: AppSpace.s6),
            PlayButton(playing: p.playing, onTap: on ? n.toggle : null),
            const SizedBox(width: AppSpace.s6),
            ArIconButton('next', label: 'Siguiente', onTap: on ? n.next : null),
          ],
        ),
        ArIconButton(
          'repeat',
          muted: true,
          active: p.repeat != 'off',
          badge: p.repeat == 'one' ? '1' : null,
          label: 'Repetir',
          onTap: on ? n.cycleRepeat : null,
        ),
      ],
    );
  }
}

/// 4 px track with a 44 px touch area. Seeks on release, not while dragging.
class ProgressBar extends StatefulWidget {
  const ProgressBar({
    super.key,
    required this.positionMs,
    required this.durationMs,
    this.onSeek,
    this.line = false,
  });
  final int positionMs;
  final int durationMs;
  final ValueChanged<int>? onSeek;

  /// 2 px passive line (mini player).
  final bool line;

  @override
  State<ProgressBar> createState() => _ProgressBarState();
}

class _ProgressBarState extends State<ProgressBar> {
  double? _drag; // 0..1 while the finger is down

  double get _frac => widget.durationMs <= 0
      ? 0
      : (widget.positionMs / widget.durationMs).clamp(0.0, 1.0);

  void _update(Offset local, double w) =>
      setState(() => _drag = (local.dx / w).clamp(0.0, 1.0));

  void _commit() {
    final d = _drag;
    setState(() => _drag = null);
    if (d != null) widget.onSeek?.call((d * widget.durationMs).round());
  }

  @override
  Widget build(BuildContext context) {
    final f = _drag ?? _frac;
    if (widget.line) {
      return RepaintBoundary(
        child: SizedBox(
          height: 2,
          child: Stack(
            children: [
              const Positioned.fill(
                child: ColoredBox(color: AppColors.void300),
              ),
              Positioned.fill(
                child: FractionallySizedBox(
                  alignment: Alignment.centerLeft,
                  widthFactor: f,
                  child: const ColoredBox(color: AppColors.neonCyan),
                ),
              ),
            ],
          ),
        ),
      );
    }
    final shown = _drag == null
        ? widget.positionMs
        : (_drag! * widget.durationMs).round();
    final enabled = widget.onSeek != null && widget.durationMs > 0;
    return RepaintBoundary(
      child: Row(
        children: [
          Text(
            fmtTime(shown),
            style: AppText.monoTime.copyWith(color: AppColors.inkMuted),
          ),
          const SizedBox(width: AppSpace.s3),
          Expanded(
            child: LayoutBuilder(
              builder: (context, c) {
                final w = c.maxWidth;
                return Listener(
                  behavior: HitTestBehavior.opaque,
                  onPointerDown: enabled
                      ? (e) => _update(e.localPosition, w)
                      : null,
                  onPointerMove: enabled
                      ? (e) => _update(e.localPosition, w)
                      : null,
                  onPointerUp: enabled ? (_) => _commit() : null,
                  onPointerCancel: (_) => setState(() => _drag = null),
                  child: SizedBox(
                    height: AppSize.touchMin,
                    child: Stack(
                      clipBehavior: Clip.none,
                      alignment: Alignment.centerLeft,
                      children: [
                        Container(
                          height: AppSize.trackH,
                          decoration: BoxDecoration(
                            color: AppColors.void300,
                            borderRadius: BorderRadius.circular(AppRadius.pill),
                          ),
                        ),
                        Container(
                          height: AppSize.trackH,
                          width: w * f,
                          decoration: BoxDecoration(
                            color: AppColors.neonCyan,
                            borderRadius: BorderRadius.circular(AppRadius.pill),
                          ),
                        ),
                        Positioned(
                          left: w * f - AppSize.thumb / 2,
                          child: Container(
                            width: AppSize.thumb,
                            height: AppSize.thumb,
                            decoration: BoxDecoration(
                              color: AppColors.neonCyan,
                              shape: BoxShape.circle,
                              boxShadow: context.glows.cyan,
                            ),
                          ),
                        ),
                      ],
                    ),
                  ),
                );
              },
            ),
          ),
          const SizedBox(width: AppSpace.s3),
          Text(
            fmtTime(widget.durationMs),
            style: AppText.monoTime.copyWith(color: AppColors.inkMuted),
          ),
        ],
      ),
    );
  }
}

/// Four bars; only animates while [active].
class Equalizer extends StatefulWidget {
  const Equalizer({
    super.key,
    required this.active,
    this.color = AppColors.neonCyan,
  });
  final bool active;
  final Color color;

  @override
  State<Equalizer> createState() => _EqualizerState();
}

class _EqualizerState extends State<Equalizer>
    with SingleTickerProviderStateMixin {
  late final AnimationController _c = AnimationController(
    vsync: this,
    duration: const Duration(milliseconds: 1800),
  );
  static const _speed = [1.0, 1.3, 0.8, 1.1];
  static const _phase = [0.0, 0.3, 0.6, 0.15];

  @override
  void initState() {
    super.initState();
    if (widget.active) _c.repeat();
  }

  @override
  void didUpdateWidget(Equalizer old) {
    super.didUpdateWidget(old);
    if (widget.active && !_c.isAnimating) _c.repeat();
    if (!widget.active && _c.isAnimating) _c.stop();
  }

  @override
  void dispose() {
    _c.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => RepaintBoundary(
    child: SizedBox(
      width: 18,
      height: 14,
      child: AnimatedBuilder(
        animation: _c,
        builder: (_, _) => Row(
          mainAxisAlignment: MainAxisAlignment.spaceBetween,
          crossAxisAlignment: CrossAxisAlignment.end,
          children: [
            for (var i = 0; i < 4; i++)
              Transform.scale(
                scaleY: widget.active
                    ? 0.2 +
                          0.8 *
                              (0.5 +
                                  0.5 *
                                      math.sin(
                                        2 *
                                            math.pi *
                                            (_c.value * _speed[i] * 2 +
                                                _phase[i]),
                                      ))
                    : 0.35,
                alignment: Alignment.bottomCenter,
                child: SizedBox(
                  width: 3,
                  height: 14,
                  child: ColoredBox(color: widget.color),
                ),
              ),
          ],
        ),
      ),
    ),
  );
}

/// Single-select filter chip, 36 high, chamfered.
class ArChip extends StatelessWidget {
  const ArChip(
    this.text, {
    super.key,
    required this.selected,
    required this.onTap,
  });
  final String text;
  final bool selected;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) => Pressable(
    onTap: onTap,
    child: ClipPath(
      clipper: const ShapeBorderClipper(shape: chamferSm),
      child: Container(
        height: AppSize.chipH,
        padding: const EdgeInsets.symmetric(horizontal: AppSpace.s3),
        color: selected ? AppColors.neonCyan : AppColors.void200,
        child: Center(
          widthFactor: 1,
          child: Text(
            text.toUpperCase(),
            style: AppText.label.copyWith(
              color: selected ? AppColors.onNeon : AppColors.inkMuted,
            ),
          ),
        ),
      ),
    ),
  );
}

class ChipBar<T> extends StatelessWidget {
  const ChipBar({
    super.key,
    required this.items,
    required this.value,
    required this.onChanged,
  });
  final List<(T, String)> items;
  final T value;
  final ValueChanged<T> onChanged;

  @override
  Widget build(BuildContext context) => Wrap(
    spacing: AppSpace.s2,
    children: [
      for (final (v, label) in items)
        ArChip(label, selected: v == value, onTap: () => onChanged(v)),
    ],
  );
}

/// Chamfered text button. Primary (cyan) or ghost (void200).
class ArButton extends StatelessWidget {
  const ArButton(
    this.text, {
    super.key,
    this.icon,
    this.onTap,
    this.ghost = false,
  });
  final String text;
  final String? icon;
  final VoidCallback? onTap;
  final bool ghost;

  @override
  Widget build(BuildContext context) {
    final fg = onTap == null
        ? AppColors.inkDim
        : (ghost ? AppColors.ink : AppColors.onNeon);
    return Pressable(
      onTap: onTap,
      child: ClipPath(
        clipper: const ShapeBorderClipper(shape: chamferSm),
        child: Container(
          constraints: const BoxConstraints(minHeight: AppSize.touchMin),
          padding: const EdgeInsets.symmetric(horizontal: AppSpace.s4),
          color: ghost || onTap == null
              ? AppColors.void200
              : AppColors.neonCyan,
          child: Row(
            mainAxisSize: MainAxisSize.min,
            mainAxisAlignment: MainAxisAlignment.center,
            children: [
              if (icon != null) ...[
                ArpegioIcon(icon!, size: AppSize.iconSm, color: fg),
                const SizedBox(width: AppSpace.s2),
              ],
              Text(
                text.toUpperCase(),
                style: AppText.label.copyWith(color: fg),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class SearchField extends StatefulWidget {
  const SearchField({
    super.key,
    required this.onChanged,
    this.hint = '',
    this.initial = '',
  });
  final ValueChanged<String> onChanged;
  final String hint;
  final String initial;

  @override
  State<SearchField> createState() => _SearchFieldState();
}

class _SearchFieldState extends State<SearchField> {
  late final _ctl = TextEditingController(text: widget.initial);
  final _focus = FocusNode();

  @override
  void initState() {
    super.initState();
    _focus.addListener(() => setState(() {}));
  }

  @override
  void dispose() {
    _ctl.dispose();
    _focus.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final on = _focus.hasFocus;
    return Container(
      height: AppSize.touchMin,
      padding: const EdgeInsets.symmetric(horizontal: AppSpace.s3),
      decoration: BoxDecoration(
        color: AppColors.void100,
        border: Border.all(color: on ? AppColors.neonCyan : AppColors.void300),
        borderRadius: BorderRadius.circular(AppRadius.sm),
      ),
      child: Row(
        children: [
          ArpegioIcon(
            'search',
            size: AppSize.iconSm,
            color: on ? AppColors.neonCyan : AppColors.inkMuted,
          ),
          const SizedBox(width: AppSpace.s2),
          Expanded(
            child: TextField(
              controller: _ctl,
              focusNode: _focus,
              onChanged: widget.onChanged,
              style: AppText.body,
              cursorColor: AppColors.neonCyan,
              decoration: InputDecoration(
                isCollapsed: true,
                border: InputBorder.none,
                hintText: widget.hint,
                hintStyle: AppText.body.copyWith(color: AppColors.inkMuted),
              ),
            ),
          ),
        ],
      ),
    );
  }
}

/// 56 px row with a 44x24 switch; the whole row is tappable.
class SettingToggle extends StatelessWidget {
  const SettingToggle({
    super.key,
    required this.title,
    required this.subtitle,
    required this.value,
    required this.onChanged,
  });
  final String title;
  final String subtitle;
  final bool value;
  final ValueChanged<bool> onChanged;

  @override
  Widget build(BuildContext context) => Pressable(
    onTap: () => onChanged(!value),
    scale: 1,
    pressedColor: AppColors.void200,
    label: title,
    child: Container(
      constraints: const BoxConstraints(minHeight: AppSize.rowH),
      decoration: const BoxDecoration(
        border: Border(bottom: BorderSide(color: AppColors.void300)),
      ),
      child: Row(
        children: [
          Expanded(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(title, style: AppText.body),
                Text(
                  subtitle,
                  style: AppText.bodySm.copyWith(color: AppColors.inkMuted),
                ),
              ],
            ),
          ),
          const SizedBox(width: AppSpace.s3),
          AnimatedContainer(
            duration: AppMotion.base,
            width: 44,
            height: 24,
            padding: const EdgeInsets.all(3),
            alignment: value ? Alignment.centerRight : Alignment.centerLeft,
            decoration: BoxDecoration(
              color: value ? AppColors.neonCyan : AppColors.void300,
              borderRadius: BorderRadius.circular(AppRadius.pill),
            ),
            child: DecoratedBox(
              decoration: BoxDecoration(
                color: value ? AppColors.onNeon : AppColors.inkMuted,
                shape: BoxShape.circle,
              ),
              child: const SizedBox.square(dimension: 18),
            ),
          ),
        ],
      ),
    ),
  );
}
