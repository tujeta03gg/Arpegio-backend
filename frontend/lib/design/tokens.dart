import 'package:flutter/painting.dart';

/// Neon theme colors. Every visual value in the app comes from `lib/design/`.
abstract final class AppColors {
  static const void000 = Color(0xFF07060D);
  static const void100 = Color(0xFF0E0C1C);
  static const void200 = Color(0xFF18153A);
  static const void300 = Color(0xFF2A2560);
  static const ink = Color(0xFFEAF6FF);
  static const inkMuted = Color(0xFF9A9CC9);
  static const inkDim = Color(0xFF5D5F8F);
  static const neonCyan = Color(0xFF00F0FF);
  static const neonCyanWash = Color(0x2400F0FF);
  static const neonMagenta = Color(0xFFFF3CDC);
  static const neonAmber = Color(0xFFFFB627);
  static const signalOk = Color(0xFFA6FF3D);
  static const signalError = Color(0xFFFF5470);
  static const onNeon = void000;
}

abstract final class AppSpace {
  static const double s1 = 4;
  static const double s2 = 8;
  static const double s3 = 12;
  static const double s4 = 16;
  static const double s6 = 24;
  static const double s8 = 32;
}

abstract final class AppRadius {
  static const double sm = 4;
  static const double md = 8;
  static const double pill = 999;
  static const double chamferSm = 6;
  static const double chamferMd = 12;
}

abstract final class AppSize {
  static const double screenW = 800;
  static const double screenH = 480;
  static const double touchMin = 44;
  static const double touchPlay = 64;
  static const double iconMd = 24;
  static const double iconSm = 18;
  static const double statusbarH = 28;
  static const double railW = 72;
  static const double miniplayerH = 64;
  static const double rowH = 56;
  static const double coverNow = 296;
  static const double trackH = 4;
  static const double thumb = 14;
  static const double coverRow = 40;
  static const double coverMini = 48;
  static const double cardW = 152;
  static const double cardCover = 136;
  static const double chipH = 36;
  static const double coverDetail = 120;
}

/// Static glows. With "Reducir brillos" the theme swaps them for [].
abstract final class AppGlow {
  static const cyan = [
    BoxShadow(color: Color(0x8C00F0FF), blurRadius: 6),
    BoxShadow(color: Color(0x4000F0FF), blurRadius: 18),
  ];
  static const magenta = [
    BoxShadow(color: Color(0x8CFF3CDC), blurRadius: 6),
    BoxShadow(color: Color(0x40FF3CDC), blurRadius: 18),
  ];
  static const focus = [
    BoxShadow(color: Color(0xFF00F0FF), spreadRadius: 2),
    BoxShadow(color: Color(0x7300F0FF), blurRadius: 12),
  ];
  static const lift = [
    BoxShadow(color: Color(0x99000000), offset: Offset(0, 8), blurRadius: 24),
  ];
}

/// Animation durations (120 to 180 ms).
abstract final class AppMotion {
  static const fast = Duration(milliseconds: 120);
  static const base = Duration(milliseconds: 180);
}

/// Colors of the generated covers (content, not UI tokens).
const generatedCoverPalettes = <List<Color>>[
  [Color(0xFFFF6B35), Color(0xFF2B1055), Color(0xFFFFD166)],
  [Color(0xFF06D6A0), Color(0xFF073B4C), Color(0xFFF8F4E3)],
  [Color(0xFFEF476F), Color(0xFF1B1B3A), Color(0xFFFFD23F)],
  [Color(0xFF3A86FF), Color(0xFF0B0F2E), Color(0xFFFF006E)],
  [Color(0xFF8338EC), Color(0xFFFFBE0B), Color(0xFF14081F)],
  [Color(0xFFF15BB5), Color(0xFF00BBF9), Color(0xFF120E2B)],
  [Color(0xFFE9C46A), Color(0xFF264653), Color(0xFFE76F51)],
  [Color(0xFF80FFDB), Color(0xFF5E60CE), Color(0xFF0D0221)],
];
