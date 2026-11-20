import 'dart:async';

import 'package:flutter/services.dart';
import 'package:material_ui/material_ui.dart';
import 'package:media_kit/media_kit.dart';
import 'package:media_kit_video/media_kit_video.dart';
import 'package:wakelock_plus/wakelock_plus.dart';

import '../api/api_exception.dart';
import '../api/lfb_client.dart';
import '../app.dart' show vaultController;
import '../services/platform.dart';
import '../theme.dart';
import '../widgets/file_tile.dart';

/// libmpv 选项（docs/05-playback.md 4.2），须在 open 之前设置。
/// demuxer-max-bytes 由 PlayerConfiguration.bufferSize 设置；hwdec 保持 media_kit 默认 auto-safe。
const _mpvOptions = {
  'cache': 'yes',
  'demuxer-max-back-bytes': '33554432', // 32 MiB
  'demuxer-readahead-secs': '60',
  'cache-secs': '60',
  'network-timeout': '15',
  'stream-lavf-o': 'reconnect=1,reconnect_streamed=1,reconnect_on_network_error=1,reconnect_delay_max=5',
};

/// 断线续播退避（05 §4.3）：1/2/4/8 秒，共 4 次（约 15 秒）后提示失败。
const _retryDelays = [1, 2, 4, 8];

/// 视频播放：横屏全屏、硬解、屏幕常亮 + WifiLock、断线自动续播。
class PlayerPage extends StatefulWidget {
  const PlayerPage({
    super.key,
    required this.client,
    required this.shareId,
    required this.path,
    required this.title,
  });

  final LfbClient client;
  final String shareId;
  final String path;
  final String title;

  @override
  State<PlayerPage> createState() => _PlayerPageState();
}

class _PlayerPageState extends State<PlayerPage> with WidgetsBindingObserver {
  final _player = Player(
    configuration: const PlayerConfiguration(
      bufferSize: 150 * 1024 * 1024, // 手机 150 MiB（默认仅 32 MB）
    ),
  );
  late final _video = VideoController(_player);
  final _subs = <StreamSubscription<Object?>>[];

  Duration _position = Duration.zero;
  Duration _duration = Duration.zero;
  bool _playing = false;
  bool _buffering = false;

  /// 断点：最近一次有效播放位置，续播时从这里开始。
  Duration _resumeAt = Duration.zero;
  int _retry = 0;
  Timer? _retryTimer;
  bool _attempting = false;
  bool _reconnecting = false;
  String? _failure;

  DateTime? _bufferingSince;
  DateTime _bufferChangedAt = DateTime.now();
  Timer? _stallTimer;

  bool _controls = true;
  Timer? _hideTimer;
  double? _dragSeconds;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    SystemChrome.setPreferredOrientations([
      DeviceOrientation.landscapeLeft,
      DeviceOrientation.landscapeRight,
    ]);
    SystemChrome.setEnabledSystemUIMode(SystemUiMode.immersiveSticky);
    WakelockPlus.enable();
    PlatformService.acquireWifiLock();

    final s = _player.stream;
    _subs.addAll([
      s.position.listen(_onPosition),
      s.duration.listen((d) => setState(() => _duration = d)),
      s.playing.listen((p) {
        setState(() => _playing = p);
        if (p) _scheduleHide();
      }),
      s.buffering.listen((b) {
        _bufferingSince = b ? DateTime.now() : null;
        setState(() => _buffering = b);
      }),
      s.buffer.listen((_) => _bufferChangedAt = DateTime.now()),
      s.error.listen((_) => _onDisconnect()),
      s.completed.listen((done) {
        // 远未到片尾就「播完」，多半是连接中断
        if (done &&
            _duration > Duration.zero &&
            _resumeAt < _duration - const Duration(seconds: 5)) {
          _onDisconnect();
        }
      }),
    ]);
    // 缓冲超 10 秒且无下载进展 → 视为断线（05 §4.3）
    _stallTimer = Timer.periodic(const Duration(seconds: 1), (_) {
      final since = _bufferingSince;
      final now = DateTime.now();
      if (since != null &&
          now.difference(since).inSeconds >= 10 &&
          now.difference(_bufferChangedAt).inSeconds >= 10) {
        _bufferingSince = now;
        _onDisconnect();
      }
    });
    _start();
  }

  Future<void> _start() async {
    final native = _player.platform;
    if (native is NativePlayer) {
      for (final e in _mpvOptions.entries) {
        await native.setProperty(e.key, e.value);
      }
    }
    await _open(Duration.zero);
  }

  Future<void> _open(Duration start) => _player.open(
    Media(
      widget.client.fileUrl(widget.shareId, widget.path).toString(),
      start: start > Duration.zero ? start : null,
    ),
  );

  void _onPosition(Duration p) {
    if (!mounted) return;
    setState(() => _position = p);
    if (p <= Duration.zero) return; // 出错时位置可能归零，不覆盖断点
    if (_playing) vaultController.touch(); // 播放中不算空闲（04 §8）
    if (_reconnecting && p > _resumeAt + const Duration(seconds: 2)) {
      // 续播后确实在前进：恢复正常
      _retry = 0;
      setState(() => _reconnecting = false);
    }
    _resumeAt = p;
  }

  /// 进入「重新连接中」：按退避间隔安排一次重试。
  void _onDisconnect() {
    if (!mounted || _failure != null) return;
    if (_attempting || (_retryTimer?.isActive ?? false)) return;
    if (_retry >= _retryDelays.length) {
      setState(() {
        _reconnecting = false;
        _failure = '连接中断。请确认电脑已开机且手机与电脑在同一网络。';
        _controls = true;
      });
      return;
    }
    final delay = Duration(seconds: _retryDelays[_retry++]);
    setState(() => _reconnecting = true);
    _retryTimer = Timer(delay, _attemptResume);
  }

  Future<void> _attemptResume() async {
    _attempting = true;
    try {
      await widget.client.verify(timeout: const Duration(seconds: 3));
      await _open(_resumeAt); // 用新 URL 从断点打开，避免先从 0 播放再跳
      _attempting = false;
    } on LfbException catch (e) {
      _attempting = false;
      // 401：已由 onUnauthorized 回连接页，不再重试
      if (e is ApiException && e.isUnauthorized) return;
      _onDisconnect();
    }
  }

  void _retryManually() {
    setState(() => _failure = null);
    _retry = 0;
    _onDisconnect();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    // 进入后台释放 WifiLock 并暂停；回到前台重新获取（05 §2.4）
    if (state == AppLifecycleState.paused) {
      PlatformService.releaseWifiLock();
      _player.pause();
    } else if (state == AppLifecycleState.resumed) {
      PlatformService.acquireWifiLock();
    }
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    for (final s in _subs) {
      s.cancel();
    }
    _retryTimer?.cancel();
    _stallTimer?.cancel();
    _hideTimer?.cancel();
    WakelockPlus.disable();
    PlatformService.releaseWifiLock();
    SystemChrome.setPreferredOrientations([DeviceOrientation.portraitUp]);
    SystemChrome.setEnabledSystemUIMode(SystemUiMode.edgeToEdge);
    _player.dispose();
    super.dispose();
  }

  void _scheduleHide() {
    _hideTimer?.cancel();
    _hideTimer = Timer(const Duration(seconds: 3), () {
      if (mounted && _playing && _dragSeconds == null) {
        setState(() => _controls = false);
      }
    });
  }

  void _toggleControls() {
    setState(() => _controls = !_controls);
    if (_controls) _scheduleHide();
  }

  Future<void> _seek(double seconds) async {
    final target = Duration(milliseconds: (seconds * 1000).round());
    _resumeAt = target;
    await _player.seek(target);
    _scheduleHide();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: Colors.black,
      body: Stack(
        fit: StackFit.expand,
        children: [
          Video(controller: _video, controls: NoVideoControls),
          GestureDetector(
            behavior: HitTestBehavior.opaque,
            onTap: _toggleControls,
          ),
          if (_buffering && !_reconnecting && _failure == null)
            const Center(child: CircularProgressIndicator()),
          if (_reconnecting && _failure == null)
            Center(child: _pill(const Text('重新连接中…'))),
          if (_failure != null) Center(child: _failurePanel()),
          if (_controls) ..._controlsOverlay(),
        ],
      ),
    );
  }

  Widget _pill(Widget child) => DecoratedBox(
    decoration: BoxDecoration(
      color: Colors.black.withValues(alpha: 0.55),
      borderRadius: BorderRadius.circular(24),
    ),
    child: Padding(
      padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 10),
      child: DefaultTextStyle.merge(
        style: const TextStyle(color: NightColors.moon),
        child: child,
      ),
    ),
  );

  Widget _failurePanel() => _pill(
    Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        Text(_failure!),
        const SizedBox(height: 8),
        Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            TextButton(
              onPressed: () => Navigator.of(context).maybePop(),
              child: const Text('返回'),
            ),
            FilledButton(onPressed: _retryManually, child: const Text('重试')),
          ],
        ),
      ],
    ),
  );

  List<Widget> _controlsOverlay() {
    final max = _duration.inMilliseconds / 1000;
    final value = (_dragSeconds ?? _position.inMilliseconds / 1000).clamp(
      0.0,
      max > 0 ? max : 0.0,
    );
    const numStyle = TextStyle(
      color: NightColors.moon,
      fontFeatures: [FontFeature.tabularFigures()],
    );
    return [
      // 顶部：返回 + 标题
      Positioned(
        left: 0,
        right: 0,
        top: 0,
        child: DecoratedBox(
          decoration: const BoxDecoration(
            gradient: LinearGradient(
              begin: Alignment.topCenter,
              end: Alignment.bottomCenter,
              colors: [Color(0x99000000), Color(0x00000000)],
            ),
          ),
          child: SafeArea(
            bottom: false,
            child: Row(
              children: [
                IconButton(
                  tooltip: '返回',
                  color: NightColors.moon,
                  icon: const Icon(Icons.arrow_back),
                  onPressed: () => Navigator.of(context).maybePop(),
                ),
                Expanded(
                  child: Text(
                    widget.title,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: const TextStyle(color: NightColors.moon),
                  ),
                ),
              ],
            ),
          ),
        ),
      ),
      // 底部：进度条；播放键在左下角（双手拇指）
      Positioned(
        left: 0,
        right: 0,
        bottom: 0,
        child: DecoratedBox(
          decoration: const BoxDecoration(
            gradient: LinearGradient(
              begin: Alignment.bottomCenter,
              end: Alignment.topCenter,
              colors: [Color(0x99000000), Color(0x00000000)],
            ),
          ),
          child: SafeArea(
            top: false,
            child: Padding(
              padding: const EdgeInsets.fromLTRB(8, 16, 16, 8),
              child: Row(
                children: [
                  IconButton(
                    iconSize: 36,
                    color: NightColors.wick,
                    icon: Icon(_playing ? Icons.pause : Icons.play_arrow),
                    onPressed: () {
                      _player.playOrPause();
                      _scheduleHide();
                    },
                  ),
                  Text(
                    formatDuration(
                      Duration(milliseconds: (value * 1000).round()),
                    ),
                    style: numStyle,
                  ),
                  Expanded(
                    child: Slider(
                      value: value,
                      max: max > 0 ? max : 1,
                      onChanged: max > 0
                          ? (v) => setState(() => _dragSeconds = v)
                          : null,
                      // 松手才跳转，落点精确
                      onChangeEnd: max > 0
                          ? (v) {
                              setState(() => _dragSeconds = null);
                              _seek(v);
                            }
                          : null,
                    ),
                  ),
                  Text(formatDuration(_duration), style: numStyle),
                ],
              ),
            ),
          ),
        ),
      ),
    ];
  }
}
