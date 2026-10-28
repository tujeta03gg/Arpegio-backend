import 'package:flutter/painting.dart';

import 'tokens.dart';

const _sans = 'Chakra Petch';
const _mono = 'Share Tech Mono';

TextStyle _s(
  String family,
  double size,
  double line,
  FontWeight w, [
  double spacing = 0,
]) => TextStyle(
  fontFamily: family,
  fontSize: size,
  height: line / size,
  fontWeight: w,
  letterSpacing: spacing,
  color: AppColors.ink,
);

abstract final class AppText {
  static final display = _s(_sans, 40, 44, FontWeight.w700, 0.4);
  static final titleLg = _s(_sans, 28, 32, FontWeight.w600);
  static final title = _s(_sans, 22, 28, FontWeight.w600);
  static final bodyLg = _s(_sans, 18, 24, FontWeight.w500);
  static final body = _s(_sans, 16, 22, FontWeight.w500);
  static final bodySm = _s(_sans, 14, 20, FontWeight.w400);

  /// Uppercase is applied by the caller (`.toUpperCase()`), as Flutter has no text-transform.
  static final label = _s(_sans, 12, 16, FontWeight.w600, 1.44);
  static final caption = _s(_sans, 12, 16, FontWeight.w600, 0.24);
  static final monoTime = _s(_mono, 14, 18, FontWeight.w400, 0.56);
  static final monoStatus = _s(_mono, 12, 16, FontWeight.w400, 0.72);
}
