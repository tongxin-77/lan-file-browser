import 'file_item.dart';

/// `GET /api/continue` 的条目（03 §4.8）：按「共享+目录」聚合的最近一条进度。
class ContinueItem {
  const ContinueItem({
    required this.share,
    required this.path,
    required this.name,
    required this.position,
    required this.duration,
    required this.watched,
    required this.updatedAt,
    required this.deviceName,
    this.next,
  });

  factory ContinueItem.fromJson(Map<String, dynamic> j) => ContinueItem(
    share: j['share'] as String,
    path: j['path'] as String,
    name: j['name'] as String? ?? '',
    position: (j['position'] as num?)?.toDouble() ?? 0,
    duration: (j['duration'] as num?)?.toDouble() ?? 0,
    watched: j['watched'] as bool? ?? false,
    updatedAt: DateTime.tryParse(j['updatedAt'] as String? ?? ''),
    deviceName: j['deviceName'] as String? ?? '',
    next: j['next'] is Map
        ? PathItem.fromJson((j['next'] as Map).cast<String, dynamic>())
        : null,
  );

  final String share;
  final String path;
  final String name;
  final double position;
  final double duration;
  final bool watched;
  final DateTime? updatedAt;

  /// 上报该记录的设备名（家庭共享进度）。
  final String deviceName;

  /// 已看完时的下一集（服务端保证 watched 为真时才有）。
  final PathItem? next;

  /// 看完且有下一集时直接播下一集。
  bool get playsNext => watched && next != null;

  double get fraction =>
      duration > 0 ? (position / duration).clamp(0.0, 1.0) : 0.0;

  /// 点击后要播放的文件：路径、文件名、该文件已有的进度。
  ({String path, String name, WatchProgress? progress}) get target => playsNext
      ? (path: next!.path, name: next!.item.name, progress: next!.item.progress)
      : (
          path: path,
          name: name,
          progress: WatchProgress(
            position: position,
            duration: duration,
            watched: watched,
          ),
        );
}
