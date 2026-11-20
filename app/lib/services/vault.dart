import 'dart:async';

import 'package:flutter/widgets.dart';

import '../api/lfb_client.dart';
import '../models/share.dart';
import 'platform.dart';

/// 加密片库会话（docs/04-vault.md §8）：只在内存；切后台、锁屏、空闲 [idle] 立即上锁。
///
/// 上锁时：通知服务端 `/api/vault/lock`、清除客户端会话、取消防截屏、回调 [onLocked]
/// （由 App 清空图片内存缓存并回到首页）。
class VaultController extends ChangeNotifier with WidgetsBindingObserver {
  VaultController({
    DateTime Function()? now,
    Future<void> Function(bool on)? setSecure,
    this.idle = const Duration(minutes: 10),
    this.tick = const Duration(seconds: 15),
  }) : _now = now ?? DateTime.now,
       _setSecure = setSecure ?? PlatformService.setSecure;

  final DateTime Function() _now;
  final Future<void> Function(bool on) _setSecure;
  final Duration idle;

  /// 空闲检查间隔；为 null 时不启动计时器（测试手动调用 [checkIdle]）。
  final Duration? tick;

  void Function()? onLocked;

  LfbClient? _client;
  List<Share> _shares = const [];
  DateTime _lastActive = DateTime.fromMillisecondsSinceEpoch(0);
  Timer? _timer;

  bool get unlocked => _client?.vault != null;

  /// 解锁得到的片库（与普通共享同格式）。
  List<Share> get shares => _shares;

  /// 用 [password] 解锁 [client] 所连电脑上密码匹配的片库；不匹配返回 false。
  Future<bool> unlock(LfbClient client, String password) async {
    final r = await client.unlockVault(password);
    final session = r.session;
    if (session == null) return false;
    if (_client != null && _client != client) lock();
    _client = client
      ..vault = VaultAccess(session, {for (final s in r.shares) s.id})
      ..onVaultLost = lock;
    _shares = r.shares;
    _lastActive = _now();
    final every = tick;
    _timer?.cancel();
    if (every != null) _timer = Timer.periodic(every, (_) => checkIdle());
    unawaited(_setSecure(true));
    notifyListeners();
    return true;
  }

  /// 立即上锁；未解锁时无操作。
  void lock() {
    final client = _client;
    final session = client?.vault?.session;
    _timer?.cancel();
    _timer = null;
    _client = null;
    if (client == null || session == null) return;
    client
      ..vault = null
      ..onVaultLost = null;
    _shares = const [];
    unawaited(client.lockVault(session).catchError((Object _) {}));
    unawaited(_setSecure(false));
    onLocked?.call();
    notifyListeners();
  }

  /// 记录一次用户操作（触摸、播放进行中）。
  void touch() => _lastActive = _now();

  /// 空闲满 [idle] 则上锁。
  void checkIdle() {
    if (unlocked && _now().difference(_lastActive) >= idle) lock();
  }

  /// 切后台（hidden / paused，含锁屏）立即上锁；inactive（下拉通知栏等）不算。
  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.hidden ||
        state == AppLifecycleState.paused ||
        state == AppLifecycleState.detached) {
      lock();
    }
  }

  @override
  void dispose() {
    _timer?.cancel();
    super.dispose();
  }
}
