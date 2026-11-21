import 'dart:async';

import 'package:flutter/services.dart';
import 'package:material_ui/material_ui.dart';
import 'package:media_kit/media_kit.dart';
import 'package:media_kit_video/media_kit_video.dart';
import 'package:wakelock_plus/wakelock_plus.dart';

import '../api/api_exception.dart';
import '../api/lfb_client.dart';
import '../app.dart' show vaultController;
import '../models/file_item.dart';
import '../models/subtitle.dart';
import '../services/orientation.dart';
import '../services/platform.dart';
import '../services/playback.dart';
import '../theme.dart';
import '../widgets/file_tile.dart';
import '../widgets/player_bottom_bar.dart';
import '../widgets/up_next_card.dart';

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

/// 视频播放：横屏全屏、硬解、屏幕常亮 + WifiLock、断线自动续播；
/// 观看进度上报、片尾自动下一集、选集、外挂字幕（P2）。
class PlayerPage extends StatefulWidget {
  const PlayerPage({
    super.key,
    required this.client,
    required this.shareId,
    required this.path,
    required this.title,
    this.startAt = Duration.zero,
  });

  final LfbClient client;
  final String shareId;
  final String path;
  final String title;

  /// 起播位置（续播，见 [resumePosition]）。
  final Duration startAt;

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

  /// 当前播放的文件（切集后改变）。
  late String _path = widget.path;
  late String _title = widget.title;

  /// 当前文件已加载（时长已知），外挂字幕须在此之后添加。
  bool _loaded = false;

  late final _reporter = ProgressReporter(_sendProgress);
  final _upNext = UpNextCountdown();
  Timer? _upNextTimer;
  Future<PathItem?>? _nextLookup;
  bool _prefetched = false;

  List<SubtitleInfo> _subtitles = const [];
  SubtitleTrack? _subtitleTrack; // 用户/自动选定的字幕；null = 播放器默认
  String _subtitleName = '默认';

  /// 倍速：切集、续播保持；不持久化，下次打开播放器为 1.0×。
  double _rate = 1.0;

  bool _drawer = false;
  List<PathItem>? _episodes;
  bool _landscape = true; // 进入即横屏，可用“旋转屏幕”切换

  /// 横竖屏切换：Activity 声明了 configChanges，不会重建，播放器保持不变。
  void _rotate() {
    setState(() => _landscape = !_landscape);
    lockOrientation(landscape: _landscape);
    _scheduleHide();
  }

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    lockOrientation(landscape: true);
    SystemChrome.setEnabledSystemUIMode(SystemUiMode.immersiveSticky);
    WakelockPlus.enable();
    PlatformService.acquireWifiLock();

    final s = _player.stream;
    _subs.addAll([
      s.position.listen(_onPosition),
      s.duration.listen((d) {
        setState(() => _duration = d);
        if (d > Duration.zero && !_loaded) {
          _loaded = true;
          _applySubtitle();
        }
      }),
      s.playing.listen((p) {
        setState(() => _playing = p);
        if (p) {
          _scheduleHide();
        } else {
          _reporter.flush(_resumeAt, _duration); // 暂停 / 播完立即上报
        }
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
        } else if (done) {
          _showUpNext();
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
    _resumeAt = widget.startAt;
    await _open(widget.startAt);
    _loadSubtitles();
  }

  Future<void> _open(Duration start) async {
    _loaded = false;
    await _player.open(
      Media(
        widget.client.fileUrl(widget.shareId, _path).toString(),
        start: start > Duration.zero ? start : null,
      ),
    );
    if (_rate != 1.0) await _player.setRate(_rate); // 切集、续播保持倍速
  }

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
    if (!_loaded && _duration > Duration.zero) {
      // 续播重开同一文件时时长可能不再变化，以位置前进为准补加字幕
      _loaded = true;
      _applySubtitle();
    }
    if (_reconnecting) return;
    if (_playing) _reporter.onPosition(p, _duration);
    if (!_prefetched && shouldPrefetchNext(p, _duration)) {
      _prefetched = true;
      _lookupNext().then((n) {
        if (n != null) widget.client.prefetch(widget.shareId, n.path);
      });
    }
    if (shouldShowUpNext(p, _duration)) _showUpNext();
  }

  // ---- 观看进度（03 §4.8）----

  void _sendProgress(Duration position, Duration duration) {
    widget.client
        .reportProgress(
          widget.shareId,
          _path,
          position: position,
          duration: duration,
        )
        .catchError((Object _) {}); // 上报失败不打扰播放
  }

  // ---- 下一集 ----

  /// 当前这一集的 /api/next，只请求一次；失败视为无下一集。
  Future<PathItem?> _lookupNext() => _nextLookup ??= widget.client
      .next(widget.shareId, _path)
      .then<PathItem?>((n) => n, onError: (Object _) => null);

  void _showUpNext() {
    if (_upNext.phase != UpNextPhase.idle) return;
    final path = _path;
    _lookupNext().then((n) {
      if (!mounted || n == null || path != _path) return;
      if (!_upNext.start(n)) return;
      setState(() {});
      _upNextTimer?.cancel();
      _upNextTimer = Timer.periodic(const Duration(seconds: 1), (_) {
        final fire = _upNext.tick();
        setState(() {});
        if (fire) _switchTo(n);
        if (_upNext.phase != UpNextPhase.counting) _upNextTimer?.cancel();
      });
    });
  }

  void _cancelUpNext() {
    _upNextTimer?.cancel();
    setState(_upNext.cancel);
  }

  void _playUpNextNow() {
    final n = _upNext.next;
    if (n != null && _upNext.playNow()) _switchTo(n);
  }

  Future<void> _playNext() async {
    final n = await _lookupNext();
    if (!mounted) return;
    if (n == null) {
      ScaffoldMessenger.of(context)
          .showSnackBar(const SnackBar(content: Text('已经是最后一集')));
      return;
    }
    _switchTo(n);
  }

  /// 切到另一集：先上报当前进度，再从该集上次位置打开。
  Future<void> _switchTo(PathItem item) async {
    _reporter.flush(_resumeAt, _duration);
    _reporter.reset();
    _upNextTimer?.cancel();
    _upNext.reset();
    _nextLookup = null;
    _prefetched = false;
    final start = item.path == _path
        ? Duration.zero
        : resumePosition(item.item.progress);
    setState(() {
      _path = item.path;
      _title = item.item.name;
      _subtitles = const [];
      _subtitleTrack = null;
      _subtitleName = '默认';
      _drawer = false;
      _episodes = null; // 进度已变，下次打开抽屉重新加载
      _position = start;
      _duration = Duration.zero;
      _failure = null;
      _reconnecting = false;
    });
    _retry = 0;
    _resumeAt = start;
    await _open(start);
    _loadSubtitles();
  }

  // ---- 选集 ----

  Future<void> _toggleDrawer() async {
    setState(() => _drawer = !_drawer);
    if (_drawer) _hideTimer?.cancel();
    final dir = parentPath(_path);
    if (!_drawer || _episodes != null) return;
    try {
      final page = await widget.client.list(widget.shareId, dir, limit: 1000);
      if (!mounted || page == null) return;
      setState(() {
        _episodes = [
          for (final it in page.items)
            if (it.kind == FileKind.video) PathItem(it, joinPath(dir, it.name)),
        ];
      });
    } on LfbException catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text(e.message)));
      }
    }
  }

  // ---- 字幕（03 §4.7）----

  Future<void> _loadSubtitles() async {
    final path = _path;
    try {
      final subs = await widget.client.subtitles(widget.shareId, path);
      if (!mounted || path != _path) return;
      setState(() => _subtitles = subs);
      final best = pickSubtitle(subs);
      if (best != null && _subtitleTrack == null) _chooseExternal(best);
    } on LfbException {
      // 没有外挂字幕也能播
    }
  }

  /// 外挂字幕经 /api/file?t= 直链给 mpv（t 在白名单内）。
  void _chooseExternal(SubtitleInfo s) {
    _subtitleTrack = SubtitleTrack.uri(
      widget.client.fileUrl(widget.shareId, s.path).toString(),
      title: subtitleLabel(s),
      language: s.lang.isEmpty ? null : s.lang,
    );
    setState(() => _subtitleName = subtitleLabel(s));
    _applySubtitle();
  }

  void _chooseTrack(SubtitleTrack t, String name) {
    _subtitleTrack = t;
    setState(() => _subtitleName = name);
    _applySubtitle();
  }

  void _applySubtitle() {
    final t = _subtitleTrack;
    if (_loaded && t != null) _player.setSubtitleTrack(t);
  }

  Future<void> _showRateMenu() async {
    _hideTimer?.cancel();
    final rate = await showModalBottomSheet<double>(
      context: context,
      backgroundColor: NightColors.pillow,
      showDragHandle: true,
      builder: (ctx) => ListView(
        shrinkWrap: true,
        children: [
          for (final r in playbackRates)
            ListTile(
              title: Text(formatRate(r)),
              trailing: r == _rate
                  ? const Icon(Icons.check, color: NightColors.wick)
                  : null,
              onTap: () => Navigator.of(ctx).pop(r),
            ),
        ],
      ),
    );
    if (rate != null && mounted) {
      setState(() => _rate = rate);
      await _player.setRate(rate);
    }
    _scheduleHide();
  }

  Future<void> _showSubtitleMenu() async {
    _hideTimer?.cancel();
    final embedded = _player.state.tracks.subtitle
        .where((t) => t.id != 'auto' && t.id != 'no' && !t.uri && !t.data)
        .toList();
    await showModalBottomSheet<void>(
      context: context,
      backgroundColor: NightColors.pillow,
      showDragHandle: true,
      builder: (ctx) {
        Widget option(String name, VoidCallback onTap) => ListTile(
          title: Text(name),
          trailing: name == _subtitleName
              ? const Icon(Icons.check, color: NightColors.wick)
              : null,
          onTap: () {
            Navigator.of(ctx).pop();
            onTap();
          },
        );
        return ListView(
          shrinkWrap: true,
          children: [
            option('关闭', () => _chooseTrack(SubtitleTrack.no(), '关闭')),
            for (final s in _subtitles)
              ListTile(
                title: Text(subtitleLabel(s)),
                subtitle: Text(
                  s.name,
                  style: const TextStyle(color: NightColors.ash),
                ),
                trailing: subtitleLabel(s) == _subtitleName
                    ? const Icon(Icons.check, color: NightColors.wick)
                    : null,
                onTap: () {
                  Navigator.of(ctx).pop();
                  _chooseExternal(s);
                },
              ),
            for (final (i, t) in embedded.indexed)
              option(
                '内嵌 · ${t.title ?? t.language ?? '字幕 ${i + 1}'}',
                () => _chooseTrack(
                  t,
                  '内嵌 · ${t.title ?? t.language ?? '字幕 ${i + 1}'}',
                ),
              ),
          ],
        );
      },
    );
    _scheduleHide();
  }

  /// 进入「重新连接中」：按退避间隔安排一次重试。
  void _onDisconnect() {
    if (!mounted || _failure != null) return;
    if (_attempting || (_retryTimer?.isActive ?? false)) return;
    if (_retry >= _retryDelays.length) {
      const failure = '连接中断。请确认电脑已开机且手机与电脑在同一网络。';
      setState(() {
        _reconnecting = false;
        _failure = failure;
        _controls = true;
      });
      connectionErrorText(failure).then((m) {
        if (mounted && _failure == failure) setState(() => _failure = m);
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
      // 轮询全部已保存地址，电脑换到其他已知 IP 也能续播（01 §6）
      await widget.client.reconnect();
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
    _upNextTimer?.cancel();
    _reporter.flush(_resumeAt, _duration); // 退出时上报
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
      if (mounted && _playing && _dragSeconds == null && !_drawer) {
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
          if (_upNext.phase == UpNextPhase.counting) ..._upNextOverlay(),
          if (_drawer) _episodeDrawer(),
        ],
      ),
    );
  }

  /// 片尾卡片（设计稿 4b）：画面变暗，卡片在右下角拇指处。
  List<Widget> _upNextOverlay() => [
    const IgnorePointer(child: ColoredBox(color: Color(0x66000000))),
    Positioned(
      right: 24,
      bottom: 24,
      child: SafeArea(
        child: UpNextCard(
          fileName: _upNext.next!.item.name,
          remaining: _upNext.remaining,
          total: _upNext.seconds,
          onCancel: _cancelUpNext,
          onPlayNow: _playUpNextNow,
        ),
      ),
    ),
  ];

  /// 选集抽屉（设计稿 4a）：同目录视频，当前集高亮，看完的集下方一道苔绿短线。
  Widget _episodeDrawer() {
    final eps = _episodes;
    return Positioned(
      top: 0,
      bottom: 0,
      right: 0,
      width: 320,
      child: Material(
        color: NightColors.quilt.withValues(alpha: 0.94),
        child: SafeArea(
          left: false,
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Padding(
                padding: const EdgeInsets.fromLTRB(20, 12, 8, 4),
                child: Row(
                  children: [
                    Text('选集', style: Theme.of(context).textTheme.titleLarge),
                    const SizedBox(width: 8),
                    if (eps != null)
                      Text(
                        '${eps.length} 集',
                        style: const TextStyle(color: NightColors.ash),
                      ),
                    const Spacer(),
                    IconButton(
                      tooltip: '关闭',
                      icon: const Icon(Icons.close),
                      onPressed: _toggleDrawer,
                    ),
                  ],
                ),
              ),
              Expanded(
                child: eps == null
                    ? const Center(child: CircularProgressIndicator())
                    : ListView.builder(
                        itemCount: eps.length,
                        itemBuilder: (context, i) {
                          final e = eps[i];
                          final current = e.path == _path;
                          final watched = e.item.progress?.watched ?? false;
                          return ListTile(
                            selected: current,
                            selectedColor: NightColors.wick,
                            title: Text(
                              episodeLabel(e.item.name),
                              maxLines: 1,
                              overflow: TextOverflow.ellipsis,
                            ),
                            subtitle: watched
                                ? Align(
                                    alignment: Alignment.centerLeft,
                                    child: Container(
                                      width: 24,
                                      height: 2,
                                      color: NightColors.moss,
                                    ),
                                  )
                                : e.item.progress != null
                                ? WatchBar(progress: e.item.progress!)
                                : null,
                            onTap: current ? null : () => _switchTo(e),
                          );
                        },
                      ),
              ),
            ],
          ),
        ),
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
                    _title,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: const TextStyle(color: NightColors.moon),
                  ),
                ),
                Flexible(
                  child: TextButton(
                    onPressed: _showSubtitleMenu,
                    style: TextButton.styleFrom(
                      foregroundColor: NightColors.moon,
                      minimumSize: const Size(48, 48),
                    ),
                    child: Text(
                      '字幕 · $_subtitleName',
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                    ),
                  ),
                ),
                IconButton(
                  tooltip: '旋转屏幕',
                  color: NightColors.moon,
                  icon: const Icon(Icons.screen_rotation),
                  onPressed: _rotate,
                ),
                const SizedBox(width: 8),
              ],
            ),
          ),
        ),
      ),
      // 底部控制栏
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
              child: PlayerBottomBar(
                playing: _playing,
                value: value,
                max: max,
                duration: _duration,
                rate: _rate,
                onRate: _showRateMenu,
                onPlayPause: () {
                  _player.playOrPause();
                  _scheduleHide();
                },
                onDrag: (v) => setState(() => _dragSeconds = v),
                onDragEnd: (v) {
                  setState(() => _dragSeconds = null);
                  _seek(v);
                },
                onEpisodes: _toggleDrawer,
                onNext: _playNext,
              ),
            ),
          ),
        ),
      ),
    ];
  }
}
