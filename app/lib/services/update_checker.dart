import 'dart:async';

import 'package:device_info_plus/device_info_plus.dart';
import 'package:material_ui/material_ui.dart';
import 'package:package_info_plus/package_info_plus.dart';

import '../api/lfb_client.dart';
import '../widgets/update_dialog.dart';
import 'app_update.dart';

/// 连接成功后检查 App 更新与电脑端版本兼容（02 §5.2、5.3），之后每天一次；失败静默。
class UpdateChecker {
  UpdateChecker(this._context);

  /// 弹对话框用的上下文（App 的根 Navigator）。
  final BuildContext? Function() _context;

  LfbClient? _client;
  DateTime? _lastCheck;
  Timer? _timer;
  bool _running = false;
  String? _skipped; // 本次运行中用户点过“稍后”的版本
  bool _serverWarned = false;

  /// 换到 [client] 所连电脑并立即检查一次；之后每小时看一下是否满 24 小时。
  void start(LfbClient client) {
    if (!identical(client, _client)) _lastCheck = null;
    _client = client;
    _timer ??= Timer.periodic(const Duration(hours: 1), (_) => _maybeCheck());
    _maybeCheck();
  }

  Future<void> _maybeCheck() async {
    final client = _client;
    if (client == null || _running) return;
    if (!shouldCheckUpdate(_lastCheck, DateTime.now())) return;
    _running = true;
    _lastCheck = DateTime.now();
    try {
      await _check(client);
    } on Object {
      // 检查失败静默忽略，不影响主功能
    } finally {
      _running = false;
    }
  }

  Future<void> _check(LfbClient client) async {
    final ping = await client.ping();
    if (!_serverWarned && needsServerUpgrade(ping.version)) {
      _serverWarned = true;
      final ctx = _context();
      if (ctx != null && ctx.mounted) {
        await showServerUpgradeDialog(ctx, ping.version);
      }
    }
    final current = (await PackageInfo.fromPlatform()).version;
    final latest = await client.appLatest();
    final decision = decideUpdate(
      current: current,
      latest: latest?.version,
      minAppVersion: ping.minAppVersion,
    );
    if (decision == UpdateDecision.none) return;
    final forced = decision == UpdateDecision.forced;
    if (!forced && latest?.version == _skipped) return;

    final abis = (await DeviceInfoPlugin().androidInfo).supportedAbis;
    final abi = latest == null ? null : chooseAbi(abis, latest.apks);
    final ctx = _context();
    if (ctx == null || !ctx.mounted) return;
    final offer = UpdateOffer(
      version: latest?.version,
      notes: latest?.notes ?? '',
      forced: forced,
      apkUrl: abi == null ? null : Uri.parse(latest!.apks[abi]!),
      sha256: abi == null ? null : latest!.sha256[abi],
      headers: {
        if (client.token != null) 'Authorization': 'Bearer ${client.token}',
      },
    );
    final installing = await showUpdateDialog(ctx, offer);
    if (!installing && !forced) _skipped = latest?.version;
    // 强制更新框被关掉（如上锁回首页时）→ 下个整点再弹
    if (!installing && forced) _lastCheck = null;
  }
}
