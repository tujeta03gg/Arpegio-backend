import 'package:flutter/material.dart';

import 'tokens.dart';
import 'typography.dart';

/// Resolves glows in one place: "Reducir brillos" turns them all into [].
class ArpegioGlows extends ThemeExtension<ArpegioGlows> {
  const ArpegioGlows({required this.calm});
  final bool calm;

  List<BoxShadow> get cyan => calm ? const [] : AppGlow.cyan;
  List<BoxShadow> get magenta => calm ? const [] : AppGlow.magenta;
  List<BoxShadow> get focus =>
      AppGlow.focus; // keyboard focus must stay visible

  @override
  ArpegioGlows copyWith({bool? calm}) => ArpegioGlows(calm: calm ?? this.calm);
  @override
  ArpegioGlows lerp(ArpegioGlows? other, double t) => other ?? this;
}

extension GlowsX on BuildContext {
  ArpegioGlows get glows =>
      Theme.of(this).extension<ArpegioGlows>() ??
      const ArpegioGlows(calm: false);
}

ThemeData buildTheme({bool calm = false}) => ThemeData(
  useMaterial3: true,
  brightness: Brightness.dark,
  scaffoldBackgroundColor: AppColors.void000,
  canvasColor: AppColors.void000,
  fontFamily: 'Chakra Petch',
  splashFactory: NoSplash.splashFactory,
  highlightColor: Colors.transparent,
  colorScheme: const ColorScheme.dark(
    primary: AppColors.neonCyan,
    onPrimary: AppColors.onNeon,
    surface: AppColors.void100,
    onSurface: AppColors.ink,
    error: AppColors.signalError,
  ),
  textTheme: TextTheme(bodyMedium: AppText.body),
  extensions: [ArpegioGlows(calm: calm)],
);
