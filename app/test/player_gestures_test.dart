import 'dart:ui';

import 'package:flutter_test/flutter_test.dart';
import 'package:lfb_app/services/player_gestures.dart';

void main() {
  const size = Size(800, 400);

  group('classifyGesture', () {
    test('横向为主 → 拖动进度，不分左右半屏', () {
      expect(
        classifyGesture(const Offset(100, 200), const Offset(30, 10), size),
        PlayerGesture.seek,
      );
      expect(
        classifyGesture(const Offset(700, 200), const Offset(-30, 29), size),
        PlayerGesture.seek,
      );
    });

    test('纵向为主：左半屏亮度、右半屏音量', () {
      expect(
        classifyGesture(const Offset(399, 200), const Offset(5, -30), size),
        PlayerGesture.brightness,
      );
      expect(
        classifyGesture(const Offset(400, 200), const Offset(5, 30), size),
        PlayerGesture.volume,
      );
    });

    test('起点在边缘 24dp 内不响应（系统返回手势、状态栏）', () {
      for (final start in const [
        Offset(10, 200),
        Offset(790, 200),
        Offset(400, 10),
        Offset(400, 390),
      ]) {
        expect(
          classifyGesture(start, const Offset(30, 0), size),
          PlayerGesture.none,
          reason: '$start',
        );
      }
      expect(
        classifyGesture(const Offset(24, 24), const Offset(30, 0), size),
        PlayerGesture.seek,
      );
    });
  });

  group('seekTarget', () {
    test('整屏宽 = 90 秒', () {
      expect(seekTarget(from: 600, dx: 800, width: 800, duration: 3600), 690);
      expect(seekTarget(from: 600, dx: -400, width: 800, duration: 3600), 555);
    });

    test('片长短于 90 秒时整屏宽 = 全片', () {
      expect(seekTarget(from: 0, dx: 400, width: 800, duration: 60), 30);
    });

    test('限制在 0…片长；时长未知时不动', () {
      expect(seekTarget(from: 10, dx: -800, width: 800, duration: 3600), 0);
      expect(seekTarget(from: 3590, dx: 800, width: 800, duration: 3600), 3600);
      expect(seekTarget(from: 5, dx: 800, width: 800, duration: 0), 5);
    });
  });

  test('adjustLevel：上滑整屏高 = +100%，限制在 0…1', () {
    expect(adjustLevel(0.5, -200, 400), 1.0);
    expect(adjustLevel(0.5, -100, 400), 0.75);
    expect(adjustLevel(0.5, 100, 400), 0.25);
    expect(adjustLevel(0.2, 400, 400), 0.0);
  });

  test('formatSeekDelta', () {
    expect(formatSeekDelta(90), '+01:30');
    expect(formatSeekDelta(-5.4), '-00:05');
    expect(formatSeekDelta(0), '+00:00');
    expect(formatSeekDelta(3725), '+1:02:05');
  });
}
