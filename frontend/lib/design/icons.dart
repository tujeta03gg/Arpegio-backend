import 'package:flutter/widgets.dart';
import 'package:flutter_svg/flutter_svg.dart';

import 'tokens.dart';

/// One of the 22 system icons (`assets/icons/<name>.svg`), recolored with srcIn.
class ArpegioIcon extends StatelessWidget {
  const ArpegioIcon(
    this.name, {
    super.key,
    this.size = AppSize.iconMd,
    this.color = AppColors.ink,
  });
  final String name;
  final double size;
  final Color color;

  @override
  Widget build(BuildContext context) => SvgPicture.asset(
    'assets/icons/$name.svg',
    width: size,
    height: size,
    colorFilter: ColorFilter.mode(color, BlendMode.srcIn),
  );
}
