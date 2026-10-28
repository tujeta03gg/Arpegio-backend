import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../design/chamfer.dart';
import '../design/tokens.dart';
import '../state/providers.dart';

/// Cover of an album: `/artwork/{id}?size=` or, without artwork, the cover generated from [seed].
class Artwork extends ConsumerWidget {
  const Artwork({
    super.key,
    required this.albumId,
    required this.seed,
    required this.size,
    this.chamfer = false,
    this.radius = AppRadius.sm,
    this.round = false,
  });
  final String albumId;
  final String seed; // album title
  final double size;
  final bool chamfer;
  final double radius;
  final bool round;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final id = ref.watch(artworkIdProvider(albumId));
    final generated = GeneratedArt(seed: seed, size: size);
    final Widget img = id.when(
      data: (art) => art.isEmpty
          ? generated
          : Image.network(
              ref.read(apiProvider).artworkUrl(art, size <= 96 ? 96 : 300),
              width: size,
              height: size,
              fit: BoxFit.cover,
              cacheWidth: size.round(),
              cacheHeight: size.round(),
              gaplessPlayback: true,
              errorBuilder: (_, _, _) => generated,
            ),
      loading: () => SizedBox.square(
        dimension: size,
        child: const ColoredBox(color: AppColors.void200),
      ),
      error: (_, _) => generated,
    );
    final boxed = SizedBox.square(dimension: size, child: img);
    if (chamfer) return Chamfered(child: boxed);
    if (round) return ClipOval(child: boxed);
    return ClipRRect(borderRadius: BorderRadius.circular(radius), child: boxed);
  }
}

int _hash(String s) {
  var h = 2166136261;
  for (final c in s.codeUnits) {
    h ^= c;
    h = (h * 16777619) & 0xFFFFFFFF;
  }
  return h;
}

/// Port of `Arpegio.art()` from the design system.
class GeneratedArt extends StatelessWidget {
  const GeneratedArt({super.key, required this.seed, required this.size});
  final String seed;
  final double size;

  @override
  Widget build(BuildContext context) => RepaintBoundary(
    child: CustomPaint(size: Size.square(size), painter: _ArtPainter(seed)),
  );
}

class _ArtPainter extends CustomPainter {
  _ArtPainter(this.seed);
  final String seed;

  @override
  void paint(Canvas canvas, Size size) {
    canvas.scale(size.width / 100, size.height / 100);
    final h = _hash(seed);
    final p = generatedCoverPalettes[h % generatedCoverPalettes.length];
    final k = (h >> 8) % 6;
    Paint fill(Color c) => Paint()..color = c;
    Paint stroke(Color c) => Paint()
      ..color = c
      ..style = PaintingStyle.stroke
      ..strokeWidth = 8;
    canvas.drawRect(const Rect.fromLTWH(0, 0, 100, 100), fill(p[1]));
    switch (k) {
      case 0:
        canvas.drawCircle(const Offset(50, 44), 26, fill(p[0]));
        for (final y in [62.0, 74.0, 86.0]) {
          canvas.drawRect(Rect.fromLTWH(0, y, 100, 6), fill(p[2]));
        }
      case 1:
        for (var y = 0; y < 5; y++) {
          for (var x = 0; x < 5; x++) {
            canvas.drawCircle(
              Offset(14.0 + x * 18, 14.0 + y * 18),
              2.0 + ((x + y + h) % 4) * 2,
              fill(p[0]),
            );
          }
        }
      case 2:
        canvas.drawPath(
          Path()
            ..moveTo(0, 100)
            ..lineTo(50, 20)
            ..lineTo(100, 100)
            ..close(),
          fill(p[0]),
        );
        canvas.drawPath(
          Path()
            ..moveTo(20, 100)
            ..lineTo(60, 40)
            ..lineTo(100, 100)
            ..close(),
          fill(p[2]),
        );
      case 3:
        canvas.drawCircle(const Offset(50, 50), 38, stroke(p[0]));
        canvas.drawCircle(const Offset(50, 50), 20, stroke(p[2]));
        canvas.drawCircle(const Offset(50, 50), 5, fill(p[0]));
      case 4:
        canvas.drawRect(const Rect.fromLTWH(0, 0, 50, 100), fill(p[0]));
        canvas.drawCircle(const Offset(50, 50), 22, fill(p[2]));
      default:
        for (var i = 0; i < 8; i++) {
          final bh = (i + 1) * 9.0;
          canvas.drawRect(
            Rect.fromLTWH(8.0 + i * 11, 84 - bh, 7, bh),
            fill(p[i.isOdd ? 2 : 0]),
          );
        }
    }
  }

  @override
  bool shouldRepaint(_ArtPainter old) => old.seed != seed;
}
