import 'package:flutter/services.dart';

/// 强制横屏或竖屏（播放器、看图的“旋转屏幕”按钮）；系统关闭自动旋转时也生效。
Future<void> lockOrientation({required bool landscape}) =>
    SystemChrome.setPreferredOrientations(
      landscape
          ? const [
              DeviceOrientation.landscapeLeft,
              DeviceOrientation.landscapeRight,
            ]
          : const [DeviceOrientation.portraitUp],
    );
