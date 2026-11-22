import 'dart:async';

import 'package:device_info_plus/device_info_plus.dart';
import 'package:material_ui/material_ui.dart';
import 'package:package_info_plus/package_info_plus.dart';

import '../api/lfb_client.dart';
import '../models/server.dart';
import '../widgets/update_dialog.dart';
import 'app_update.dart';

/// 连接成功后从 GitHub 检查 App 更新并检查电脑端版本兼容（02 §5.2、5.3），之后每天一次；失败静默。
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
    final ping = await _ping(client);
    GithubRelease? latest;
    try {
      latest = await fetchRelease(await _currentVersion());
    } on UpdateException {
      // 自动检查失败静默；强制更新仍要提示（只能去浏览器下载）
    }
    if (ping != null && !_serverWarned && needsServerUpgrade(ping.version)) {
      _serverWarned = true;
      final ctx = _context();
      if (ctx != null && ctx.mounted) {
        await showServerUpgradeDialog(
          ctx,
          ping.version,
          latest?.htmlUrl ?? releasesPageUrl,
        );
      }
    }
    await _offer(ping, latest, manual: false);
  }

  /// 设置里的“检查更新”（02 §5.2）：有新版直接弹更新框（返回 null），
  /// 否则返回给用户看的结果（[failed] 为真时面板另给“在浏览器中打开下载页”）；
  /// 忽略“稍后”记录与 24 小时间隔。
  Future<({String text, bool failed})?> checkNow(LfbClient client) async {
    try {
      final ping = await _ping(client);
      final latest = await fetchRelease(await _currentVersion());
      if (await _offer(ping, latest, manual: true)) return null;
      return (text: '已是最新版本', failed: false);
    } on UpdateException catch (e) {
      return (text: '检查失败：${e.message}', failed: true);
    } on Object catch (e) {
      return (text: '检查失败：$e', failed: true);
    }
  }

  /// 电脑端版本与 minAppVersion 只用于兼容提示；连不上电脑不影响从 GitHub 检查。
  Future<PingInfo?> _ping(LfbClient client) async {
    try {
      return await client.ping();
    } on Object {
      return null;
    }
  }

  Future<String> _currentVersion() async =>
      (await PackageInfo.fromPlatform()).version;

  /// 需要更新时弹更新框并返回 true；无需更新返回 false。
  Future<bool> _offer(
    PingInfo? ping,
    GithubRelease? latest, {
    required bool manual,
  }) async {
    final decision = decideUpdate(
      current: await _currentVersion(),
      latest: latest?.version,
      minAppVersion: ping?.minAppVersion,
    );
    if (decision == UpdateDecision.none) return false;
    final forced = decision == UpdateDecision.forced;
    if (!manual && !forced && latest?.version == _skipped) return false;

    final abis = (await DeviceInfoPlugin().androidInfo).supportedAbis;
    final abi = latest == null ? null : chooseAbi(abis, latest.apks);
    final ctx = _context();
    if (ctx == null || !ctx.mounted) return false;
    final checksums = latest?.checksumsUrl;
    final offer = UpdateOffer(
      version: latest?.version,
      notes: latest?.notes ?? '',
      forced: forced,
      releaseUrl: latest?.htmlUrl ?? releasesPageUrl,
      apkUrl: abi == null ? null : Uri.parse(latest!.apks[abi]!),
      apkName: abi == null ? null : apkAssetName(abi),
      checksumsUrl: checksums == null ? null : Uri.parse(checksums),
    );
    final installing = await showUpdateDialog(ctx, offer);
    if (!installing && !forced) _skipped = latest?.version;
    // 强制更新框被关掉（如上锁回首页时）→ 下个整点再弹
    if (!installing && forced) _lastCheck = null;
    return true;
  }
}
