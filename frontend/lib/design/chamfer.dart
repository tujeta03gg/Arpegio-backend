import 'package:flutter/material.dart';

import 'tokens.dart';

/// Cut top-left and bottom-right corners: the visual signature.
const chamferMd = BeveledRectangleBorder(
  borderRadius: BorderRadius.only(
    topLeft: Radius.circular(AppRadius.chamferMd),
    bottomRight: Radius.circular(AppRadius.chamferMd),
  ),
);

const chamferSm = BeveledRectangleBorder(
  borderRadius: BorderRadius.only(
    topLeft: Radius.circular(AppRadius.chamferSm),
    bottomRight: Radius.circular(AppRadius.chamferSm),
  ),
);

/// Clips [child] to a chamfered shape.
class Chamfered extends StatelessWidget {
  const Chamfered({super.key, required this.child, this.small = false});
  final Widget child;
  final bool small;

  @override
  Widget build(BuildContext context) => ClipPath(
    clipper: ShapeBorderClipper(shape: small ? chamferSm : chamferMd),
    child: child,
  );
}
