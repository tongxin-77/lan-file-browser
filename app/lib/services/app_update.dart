import 'dart:async';
import 'dart:io';

import 'package:crypto/crypto.dart';
import 'package:http/http.dart' as http;

import 'semver.dart';

/// App 要求的最低电脑端版本（02 §5.3）：服务端接口发生本 App 依赖的新增/变更时提升。
const minServerVersion = '0.0.0';

/// `GET /api/app/latest` 响应（02 §5.2）。
class AppRelease {
  const AppRelease({
    required this.version,
    required this.minAppVersion,
    required this.notes,
    required this.apks,
    required this.sha256,
  });

  factory AppRelease.fromJson(Map<String, dynamic> j) => AppRelease(
    version: j['version'] as String? ?? '',
    minAppVersion: j['minAppVersion'] as String?,
    notes: j['notes'] as String? ?? '',
    apks: ((j['apks'] as Map?) ?? const {}).cast<String, String>(),
    sha256: ((j['sha256'] as Map?) ?? const {}).cast<String, String>(),
  );

  final String version;
  final String? minAppVersion;
  final String notes;

  /// abi → 安装包地址（服务端只列已缓存且校验通过的 ABI）。
  final Map<String, String> apks;

  /// abi → sha256 十六进制。
  final Map<String, String> sha256;
}

enum UpdateDecision { none, optional, forced }

/// 当前版本低于 [minAppVersion] → 强制；低于 [latest] → 可选；
/// 任一版本号无法识别时按“不满足该条件”处理，不打扰用户。
UpdateDecision decideUpdate({
  required String current,
  required String? latest,
  required String? minAppVersion,
}) {
  if (minAppVersion != null && compareVersions(current, minAppVersion) == -1) {
    return UpdateDecision.forced;
  }
  if (latest != null && compareVersions(current, latest) == -1) {
    return UpdateDecision.optional;
  }
  return UpdateDecision.none;
}

/// 服务端版本低于 [minServer] 时需提示“请升级电脑端”；`dev` 等无法识别的版本跳过。
bool needsServerUpgrade(
  String serverVersion, {
  String minServer = minServerVersion,
}) => compareVersions(serverVersion, minServer) == -1;

/// 距上次检查满 24 小时（或从未检查）才再检查。
bool shouldCheckUpdate(DateTime? lastCheck, DateTime now) =>
    lastCheck == null || now.difference(lastCheck) >= const Duration(hours: 24);

/// 按设备 `supportedAbis`（偏好顺序）选安装包：设备支持 arm64-v8a 且有该包时优先，
/// 否则取设备列表中第一个有安装包的 ABI；都没有返回 null。
String? chooseAbi(List<String> supportedAbis, Map<String, String> apks) {
  const preferred = 'arm64-v8a';
  if (supportedAbis.contains(preferred) && apks.containsKey(preferred)) {
    return preferred;
  }
  for (final abi in supportedAbis) {
    if (apks.containsKey(abi)) return abi;
  }
  return null;
}

/// 文件 sha256 是否等于 [expectedHex]（忽略大小写）。
Future<bool> verifySha256(File file, String expectedHex) async {
  if (expectedHex.isEmpty) return false;
  final digest = await sha256.bind(file.openRead()).first;
  return digest.toString() == expectedHex.trim().toLowerCase();
}

/// 用户取消下载。
class DownloadCancelled implements Exception {
  const DownloadCancelled();
}

/// 可取消的 APK 下载：边下边写 [dest]，失败或取消时删除半截文件。
class ApkDownload {
  ApkDownload._(this._dest, this._client, this._ownsClient);

  /// 开始下载；[onProgress] 参数为已收字节与总字节（未知时为 null）。
  factory ApkDownload.start(
    Uri url,
    File dest, {
    Map<String, String> headers = const {},
    http.Client? httpClient,
    void Function(int received, int? total)? onProgress,
  }) {
    final d = ApkDownload._(
      dest,
      httpClient ?? http.Client(),
      httpClient == null,
    );
    d._run(url, headers, onProgress);
    return d;
  }

  final File _dest;
  final http.Client _client;
  final bool _ownsClient;
  final _done = Completer<File>();
  StreamSubscription<List<int>>? _sub;
  IOSink? _sink;
  Completer<void>? _finished;
  bool _cancelled = false;
  bool _failing = false;

  /// 下载完成得到文件；取消时以 [DownloadCancelled] 结束。
  Future<File> get done => _done.future;

  Future<void> _run(
    Uri url,
    Map<String, String> headers,
    void Function(int, int?)? onProgress,
  ) async {
    try {
      final req = http.Request('GET', url)..headers.addAll(headers);
      final res = await _client.send(req);
      if (res.statusCode != 200) {
        throw HttpException('下载失败（${res.statusCode}）', uri: url);
      }
      await _dest.parent.create(recursive: true);
      if (_cancelled) throw const DownloadCancelled();
      final sink = _sink = _dest.openWrite();
      var got = 0;
      final finished = _finished = Completer<void>();
      _sub = res.stream.listen(
        (chunk) {
          sink.add(chunk);
          got += chunk.length;
          onProgress?.call(got, res.contentLength);
        },
        onError: (Object e) {
          if (!finished.isCompleted) finished.completeError(e);
        },
        onDone: () {
          if (!finished.isCompleted) finished.complete();
        },
        cancelOnError: true,
      );
      await finished.future;
      await sink.close();
      _finish(_dest);
    } on Object catch (e) {
      await _fail(e);
    }
  }

  /// 取消下载（已完成时无效）。
  void cancel() {
    if (_done.isCompleted || _cancelled) return;
    _cancelled = true;
    _sub?.cancel();
    final f = _finished;
    if (f == null) {
      _fail(const DownloadCancelled()); // 尚未开始写文件：_run 稍后见 _cancelled 退出
    } else if (!f.isCompleted) {
      f.completeError(const DownloadCancelled()); // 由 _run 关闭文件并清理
    }
  }

  void _finish(File f) {
    if (_done.isCompleted) return;
    if (_ownsClient) _client.close();
    _done.complete(f);
  }

  Future<void> _fail(Object e) async {
    if (_done.isCompleted || _failing) return;
    _failing = true;
    if (_ownsClient) _client.close();
    try {
      await _sink?.close();
    } on Object {
      // 忽略
    }
    try {
      if (_dest.existsSync()) _dest.deleteSync();
    } on FileSystemException {
      // 忽略
    }
    if (!_done.isCompleted) {
      _done.completeError(_cancelled ? const DownloadCancelled() : e);
    }
  }
}
