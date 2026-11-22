import 'dart:math' as math;
import 'dart:ui' show Offset, Size;

import '../widgets/file_tile.dart' show formatDuration;

/// 播放器滑动手势（05 §4.5）。
enum PlayerGesture { none, seek, brightness, volume }

/// 起点距屏幕边缘小于此值（dp）不响应：避开系统返回手势与状态栏/导航栏下拉。
const gestureEdge = 24.0;

/// 横滑整屏宽对应的秒数；片长更短时整屏宽 = 全片。
const seekSecondsPerWidth = 90.0;

/// 按起点与已移动的位移判定手势：横向为主 → 拖动进度；纵向为主 → 左半屏亮度、右半屏音量。
PlayerGesture classifyGesture(Offset start, Offset delta, Size size) {
  if (start.dx < gestureEdge ||
      start.dx > size.width - gestureEdge ||
      start.dy < gestureEdge ||
      start.dy > size.height - gestureEdge) {
    return PlayerGesture.none;
  }
  if (delta.dx.abs() >= delta.dy.abs()) return PlayerGesture.seek;
  return start.dx < size.width / 2
      ? PlayerGesture.brightness
      : PlayerGesture.volume;
}

/// 横滑 [dx] 后的目标位置（秒），限制在 0…[duration]。
double seekTarget({
  required double from,
  required double dx,
  required double width,
  required double duration,
}) {
  if (duration <= 0 || width <= 0) return from;
  final span = math.min(seekSecondsPerWidth, duration);
  return (from + dx / width * span).clamp(0.0, duration);
}

/// 竖滑 [dy]（向下为正）后的亮度/音量：上滑整屏高 = +100%，限制在 0…1。
double adjustLevel(double from, double dy, double height) =>
    height <= 0 ? from : (from - dy / height).clamp(0.0, 1.0);

/// 相对变化“+01:30” / “-00:05”。
String formatSeekDelta(double seconds) =>
    '${seconds < 0 ? '-' : '+'}'
    '${formatDuration(Duration(seconds: seconds.abs().round()))}';
