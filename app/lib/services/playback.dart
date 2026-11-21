import '../models/file_item.dart';

/// 看完判定，与服务端一致（03 §4.8）：≥ 90%，或片长 ≥ 10 分钟且剩余 < 120 秒。
bool isWatched(double position, double duration) =>
    duration > 0 &&
    (position >= duration * 0.9 ||
        (duration >= 600 && duration - position < 120));

/// 进入播放时的起播位置：无进度、已看完、接近片尾或刚开头（< 5 秒）都从头播。
Duration resumePosition(WatchProgress? p) {
  if (p == null ||
      p.watched ||
      p.position < 5 ||
      isWatched(p.position, p.duration)) {
    return Duration.zero;
  }
  return Duration(milliseconds: (p.position * 1000).round());
}

/// 观看进度上报节流：播放中每 [interval] 最多上报一次；暂停/退出/切集用 [flush] 立即上报。
/// 与上次上报位置相同（如暂停后退出）时不重复发送。
class ProgressReporter {
  ProgressReporter(
    this._send, {
    this.interval = const Duration(seconds: 10),
    DateTime Function()? clock,
  }) : _clock = clock ?? DateTime.now;

  final void Function(Duration position, Duration duration) _send;
  final Duration interval;
  final DateTime Function() _clock;

  DateTime? _lastAt;
  int? _lastSecond;

  /// 播放中的位置更新（每秒多次）：距上次上报满 [interval] 才上报。
  void onPosition(Duration position, Duration duration) {
    final now = _clock();
    final last = _lastAt;
    if (last == null) {
      // 起播后先计时，不立即上报
      _lastAt = now;
      return;
    }
    if (now.difference(last) >= interval) _report(position, duration, now);
  }

  /// 暂停、退出、切集：立即上报（位置未变则跳过）。
  void flush(Duration position, Duration duration) =>
      _report(position, duration, _clock());

  /// 切到新文件后重置计时。
  void reset() {
    _lastAt = null;
    _lastSecond = null;
  }

  void _report(Duration position, Duration duration, DateTime now) {
    _lastAt = now;
    if (duration <= Duration.zero || position <= Duration.zero) return;
    final sec = position.inSeconds;
    if (sec == _lastSecond) return;
    _lastSecond = sec;
    _send(position, duration);
  }
}

/// 剩余 ≤ 2 分钟：预取下一集（05 §4.4）。
bool shouldPrefetchNext(Duration position, Duration duration) =>
    duration > const Duration(minutes: 2) &&
    duration - position <= const Duration(minutes: 2);

/// 剩余 ≤ 5 秒：显示自动下一集卡片。
bool shouldShowUpNext(Duration position, Duration duration) =>
    duration > Duration.zero &&
    duration - position <= const Duration(seconds: 5);

enum UpNextPhase { idle, counting, cancelled, fired }

/// 片尾自动下一集倒计时状态机：idle → counting →（每秒 tick）→ fired；
/// counting 中可 cancel（本集内不再出现）或 playNow（立即 fired）。
class UpNextCountdown {
  UpNextCountdown({this.seconds = 5});

  final int seconds;
  UpNextPhase phase = UpNextPhase.idle;
  PathItem? next;
  int remaining = 0;

  /// 开始倒计时；已开始、已取消或已触发时忽略（返回 false）。
  bool start(PathItem item) {
    if (phase != UpNextPhase.idle) return false;
    next = item;
    remaining = seconds;
    phase = UpNextPhase.counting;
    return true;
  }

  /// 每秒调用；倒数到 0 时进入 fired 并返回 true（应切到下一集）。
  bool tick() {
    if (phase != UpNextPhase.counting) return false;
    remaining--;
    if (remaining > 0) return false;
    phase = UpNextPhase.fired;
    return true;
  }

  /// 立即播放：counting 时进入 fired 并返回 true。
  bool playNow() {
    if (phase != UpNextPhase.counting) return false;
    remaining = 0;
    phase = UpNextPhase.fired;
    return true;
  }

  void cancel() {
    if (phase == UpNextPhase.counting) phase = UpNextPhase.cancelled;
  }

  /// 切到新的一集后回到 idle。
  void reset() {
    phase = UpNextPhase.idle;
    next = null;
    remaining = 0;
  }
}

final _reEpisode = [
  RegExp(r'S\d+E(\d+)', caseSensitive: false),
  RegExp(r'第\s*(\d+)\s*[集话話]'),
  RegExp(r'(?:^|[^A-Za-z])EP?(\d+)', caseSensitive: false),
  RegExp(r'\[(\d{1,3})\]'),
  RegExp(r' - (\d{1,3}) '),
];

/// 片名展示：`星海旅人 S01E13 归途.mkv` → `第 13 集 · 归途`；
/// 解析不出集号时返回去掉扩展名的文件名。
String episodeLabel(String fileName) {
  final dot = fileName.lastIndexOf('.');
  final stem = dot > 0 ? fileName.substring(0, dot) : fileName;
  for (final re in _reEpisode) {
    final m = re.firstMatch(stem);
    if (m == null) continue;
    final n = int.parse(m.group(1)!);
    final title = stem
        .substring(m.end)
        .replaceAll(RegExp(r'[\[(（【].*?[\])）】]'), '')
        .replaceAll(RegExp(r'\b(?:\d{3,4}[pP]|[xXhH]\.?26[45])\b'), '')
        .replaceAll(RegExp(r'^[\s._\-·]+|[\s._\-·]+$'), '');
    return title.isEmpty ? '第 $n 集' : '第 $n 集 · $title';
  }
  return stem;
}

/// 可选倍速。
const playbackRates = [0.5, 0.75, 1.0, 1.25, 1.5, 2.0, 3.0];

/// 倍速显示：1.0×、0.75×、2.0×。
String formatRate(double rate) =>
    '${rate == rate.roundToDouble() ? rate.toStringAsFixed(1) : rate}×';
