import 'dart:async';

import 'package:device_info_plus/device_info_plus.dart';
import 'package:material_ui/material_ui.dart';
import 'package:package_info_plus/package_info_plus.dart';

import '../api/api_exception.dart';
import '../api/lfb_client.dart';
import '../models/server.dart';
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
    await _offer(client, ping, await client.appLatest(), manual: false);
  }

  /// 设置里的“检查更新”（02 §5.2）：有新版直接弹更新框（返回 null），
  /// 否则返回给用户看的结果；忽略“稍后”记录与 24 小时间隔。
  Future<String?> checkNow(LfbClient client) async {
    try {
      final ping = await client.ping();
      final latest = await client.appLatest();
      if (await _offer(client, ping, latest, manual: true)) return null;
      if (latest == null) {
        return '电脑端暂时没有可用的 App 版本信息'
            '（电脑端尚未从 GitHub 取到正式版 Release，或其检查更新失败 / 已关闭）';
      }
      return '已是最新版本';
    } on LfbException catch (e) {
      return '检查失败：${e.message}';
    } on Object catch (e) {
      return '检查失败：$e';
    }
  }

  /// 需要更新时弹更新框并返回 true；无需更新返回 false。
  Future<bool> _offer(
    LfbClient client,
    PingInfo ping,
    AppRelease? latest, {
    required bool manual,
  }) async {
    final current = (await PackageInfo.fromPlatform()).version;
    final decision = decideUpdate(
      current: current,
      latest: latest?.version,
      minAppVersion: ping.minAppVersion,
    );
    if (decision == UpdateDecision.none) return false;
    final forced = decision == UpdateDecision.forced;
    if (!manual && !forced && latest?.version == _skipped) return false;

    final abis = (await DeviceInfoPlugin().androidInfo).supportedAbis;
    final abi = latest == null ? null : chooseAbi(abis, latest.apks);
    final ctx = _context();
    if (ctx == null || !ctx.mounted) return false;
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
    return true;
  }
}
