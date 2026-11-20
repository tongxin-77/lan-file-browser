import 'package:material_ui/material_ui.dart';

import '../theme.dart';

/// 底部「灯晕」拇指区：从底边升起的一圈暖光，所有操作放在这里（设计稿原则 1）。
class LampGlow extends StatelessWidget {
  const LampGlow({super.key, required this.child});

  final Widget child;

  @override
  Widget build(BuildContext context) {
    return DecoratedBox(
      decoration: BoxDecoration(
        gradient: RadialGradient(
          center: Alignment.bottomCenter,
          radius: 1.4,
          colors: [
            NightColors.wick.withValues(alpha: 0.16),
            NightColors.wick.withValues(alpha: 0.05),
            NightColors.quilt.withValues(alpha: 0),
          ],
          stops: const [0, 0.55, 1],
        ),
      ),
      child: SafeArea(
        top: false,
        child: Padding(
          padding: const EdgeInsets.fromLTRB(20, 16, 20, 12),
          child: child,
        ),
      ),
    );
  }
}
