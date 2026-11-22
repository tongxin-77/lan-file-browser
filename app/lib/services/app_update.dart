import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:crypto/crypto.dart';
import 'package:http/http.dart' as http;

import 'semver.dart';

/// App 要求的最低电脑端版本（02 §5.3）：服务端接口发生本 App 依赖的新增/变更时提升。
const minServerVersion = '0.0.0';

/// App 直接从 GitHub 检查与下载更新（02 §5.2）。
const githubRepo = 'tongxin-77/lan-file-browser';

/// 全部 Release 列表页：检查失败时让用户用系统浏览器打开。
const releasesPageUrl = 'https://github.com/$githubRepo/releases';

const _userAgent = 'lfb-app';
const _apiHeaders = {
  'Accept': 'application/vnd.github+json',
  'User-Agent': _userAgent,
};

/// 下载资产（APK、checksums.txt）时的请求头。
const downloadHeaders = {'User-Agent': _userAgent};

/// 检查 / 下载更新失败，[message] 直接给用户看。
class UpdateException implements Exception {
  const UpdateException(this.message);
  final String message;
  @override
  String toString() => message;
}

const unreachableMessage = '无法访问 GitHub（可能需要代理）';

/// GitHub Release（只取用到的字段）。
class GithubRelease {
  const GithubRelease({
    required this.version,
    required this.notes,
    required this.htmlUrl,
    required this.draft,
    required this.assets,
  });

  factory GithubRelease.fromJson(Map<String, dynamic> j) => GithubRelease(
    version: j['tag_name'] as String? ?? '',
    notes: j['body'] as String? ?? '',
    htmlUrl: j['html_url'] as String? ?? releasesPageUrl,
    draft: j['draft'] as bool? ?? false,
    assets: {
      for (final a in (j['assets'] as List?) ?? const [])
        if (a is Map &&
            a['name'] is String &&
            a['browser_download_url'] is String)
          a['name'] as String: a['browser_download_url'] as String,
    },
  );

  /// `tag_name`，如 `v1.2.0`。
  final String version;
  final String notes;
  final String htmlUrl;
  final bool draft;

  /// 资产文件名 → browser_download_url。
  final Map<String, String> assets;

  /// abi → APK 下载地址（资产名 `lfb-app-<abi>.apk`）。
  Map<String, String> get apks => {
    for (final e in assets.entries)
      if (_apkName.firstMatch(e.key) case final m?) m[1]!: e.value,
  };

  String? get checksumsUrl => assets['checksums.txt'];
}

final _apkName = RegExp(r'^lfb-app-(.+)\.apk$');

/// 安装包资产名。
String apkAssetName(String abi) => 'lfb-app-$abi.apk';

/// 从 releases 列表中选更新目标：忽略草稿与无法识别的版本号，取 semver 最大者（含正式版）。
GithubRelease? pickRelease(Iterable<GithubRelease> releases) {
  GithubRelease? best;
  for (final r in releases) {
    if (r.draft || compareVersions(r.version, r.version) == null) continue;
    if (best == null || compareVersions(r.version, best.version) == 1) best = r;
  }
  return best;
}

/// 按 App 自身版本的通道查询 GitHub（与服务端 02 §5.1 一致）：
/// 预发布版（带 `-`）看最近 20 个 Release 取最大；正式版只看 `releases/latest`。
/// 没有可用 Release 返回 null；失败抛 [UpdateException]。
Future<GithubRelease?> fetchRelease(
  String currentVersion, {
  http.Client? client,
  Duration timeout = const Duration(seconds: 15),
}) async {
  final pre = currentVersion.contains('-');
  final url = Uri.parse(
    'https://api.github.com/repos/$githubRepo/releases'
    '${pre ? '?per_page=20' : '/latest'}',
  );
  final c = client ?? http.Client();
  final http.Response res;
  try {
    res = await c.get(url, headers: _apiHeaders).timeout(timeout);
  } on Object {
    throw const UpdateException(unreachableMessage);
  } finally {
    if (client == null) c.close();
  }
  switch (res.statusCode) {
    case 200:
      break;
    case 404 when !pre:
      return null; // 还没有正式版 Release
    case 403 || 429:
      throw const UpdateException('GitHub 请求过于频繁，请稍后再试');
    default:
      throw UpdateException('GitHub 返回异常（${res.statusCode}）');
  }
  try {
    final body = jsonDecode(utf8.decode(res.bodyBytes));
    if (!pre) {
      final r = GithubRelease.fromJson((body as Map).cast<String, dynamic>());
      return pickRelease([r]);
    }
    return pickRelease(
      (body as List).map(
        (e) => GithubRelease.fromJson((e as Map).cast<String, dynamic>()),
      ),
    );
  } on Object {
    throw const UpdateException('GitHub 返回的数据无法识别');
  }
}

/// 解析 sha256sum 格式（`<hex>  <文件名>`，文件名前可带 `*`）→ 文件名 → 小写 hex。
Map<String, String> parseChecksums(String text) => {
  for (final line in const LineSplitter().convert(text))
    if (RegExp(r'^([0-9a-fA-F]{64})\s+\*?(.+)$').firstMatch(line.trim())
        case final m?)
      m[2]!.trim(): m[1]!.toLowerCase(),
};

/// 下载 checksums.txt 并取出 [fileName] 的 sha256；缺失时抛 [UpdateException]。
Future<String> fetchChecksum(
  Uri checksumsUrl,
  String fileName, {
  http.Client? client,
  Duration timeout = const Duration(seconds: 30),
}) async {
  final c = client ?? http.Client();
  final http.Response res;
  try {
    res = await c.get(checksumsUrl, headers: downloadHeaders).timeout(timeout);
  } on Object {
    throw const UpdateException(unreachableMessage);
  } finally {
    if (client == null) c.close();
  }
  if (res.statusCode != 200) {
    throw UpdateException('下载校验文件失败（${res.statusCode}）');
  }
  final hex = parseChecksums(utf8.decode(res.bodyBytes))[fileName];
  if (hex == null) throw const UpdateException('校验文件中没有该安装包，无法校验');
  return hex;
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

/// 可取消的 APK 下载：边下边写 [dest]；连接中途断开时自动重试并用 HTTP Range 续传；
/// 给了 [sha256Hex] 时下载完校验。失败或取消时删除半截文件。
class ApkDownload {
  ApkDownload._(this._dest, this._client, this._ownsClient);

  /// 开始下载；[onProgress] 参数为已收字节与总字节（未知时为 null）。
  /// 连续 [maxRetries] 次没有任何进展才算失败；[timeout] 为连接与读数据的空闲超时。
  factory ApkDownload.start(
    Uri url,
    File dest, {
    Map<String, String> headers = const {},
    String? sha256Hex,
    http.Client? httpClient,
    void Function(int received, int? total)? onProgress,
    int maxRetries = 5,
    Duration retryDelay = const Duration(seconds: 2),
    Duration timeout = const Duration(seconds: 30),
  }) {
    final d = ApkDownload._(
      dest,
      httpClient ?? http.Client(),
      httpClient == null,
    );
    d._run(
      url,
      headers,
      sha256Hex,
      onProgress,
      maxRetries,
      retryDelay,
      timeout,
    );
    return d;
  }

  final File _dest;
  final http.Client _client;
  final bool _ownsClient;
  final _done = Completer<File>();
  final _cancelSignal = Completer<void>();
  StreamSubscription<List<int>>? _sub;
  Completer<void>? _finished;
  bool _cancelled = false;

  /// 下载完成得到文件；取消时以 [DownloadCancelled] 结束。
  Future<File> get done => _done.future;

  Future<void> _run(
    Uri url,
    Map<String, String> headers,
    String? sha256Hex,
    void Function(int, int?)? onProgress,
    int maxRetries,
    Duration retryDelay,
    Duration timeout,
  ) async {
    IOSink? sink;
    try {
      await _dest.parent.create(recursive: true);
      if (_dest.existsSync()) _dest.deleteSync();
      var failures = 0;
      var got = 0;
      int? total;
      while (true) {
        _checkCancelled();
        final have = got;
        try {
          final req = http.Request('GET', url)..headers.addAll(headers);
          if (have > 0) req.headers['Range'] = 'bytes=$have-';
          final res = await Future.any([
            _client.send(req).timeout(timeout),
            _cancelSignal.future.then<http.StreamedResponse>(
              (_) => throw const DownloadCancelled(),
            ),
          ]);
          _checkCancelled();
          final resumed = have > 0 && res.statusCode == 206;
          if (!resumed && res.statusCode != 200) {
            throw UpdateException('下载失败（${res.statusCode}）');
          }
          if (resumed) {
            total = _rangeTotal(res.headers['content-range']) ?? total;
          } else {
            got = 0; // 服务端不支持 Range：从头再下
            total = res.contentLength;
          }
          final s = sink = _dest.openWrite(
            mode: resumed ? FileMode.append : FileMode.write,
          );
          final finished = _finished = Completer<void>();
          _sub = res.stream
              .timeout(timeout)
              .listen(
                (chunk) {
                  s.add(chunk);
                  got += chunk.length;
                  onProgress?.call(got, total);
                },
                onError: (Object e) {
                  if (!finished.isCompleted) finished.completeError(e);
                },
                onDone: () {
                  if (!finished.isCompleted) finished.complete();
                },
                cancelOnError: true,
              );
          try {
            await finished.future;
          } finally {
            sink = null;
            await s.close();
          }
          if (total != null && got < total) {
            throw const HttpException('连接中断');
          }
          break;
        } on DownloadCancelled {
          rethrow;
        } on UpdateException {
          rethrow; // HTTP 状态码不对：不重试
        } on Object {
          if (_cancelled) throw const DownloadCancelled();
          if (got > have) failures = 0; // 有进展就重新计数
          if (++failures > maxRetries) rethrow;
          await Future.any([
            Future<void>.delayed(retryDelay),
            _cancelSignal.future,
          ]);
        }
      }
      _checkCancelled();
      if (sha256Hex != null && !await verifySha256(_dest, sha256Hex)) {
        throw const UpdateException('安装包校验失败，请重试');
      }
      _checkCancelled();
      if (_ownsClient) _client.close();
      _done.complete(_dest);
    } on Object catch (e) {
      if (_ownsClient) _client.close();
      try {
        await sink?.close();
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

  void _checkCancelled() {
    if (_cancelled) throw const DownloadCancelled();
  }

  /// 取消下载（已完成时无效）。
  void cancel() {
    if (_done.isCompleted || _cancelled) return;
    _cancelled = true;
    _cancelSignal.complete();
    _sub?.cancel();
    final f = _finished;
    if (f != null && !f.isCompleted) f.completeError(const DownloadCancelled());
  }
}

/// `Content-Range: bytes 100-999/1000` → 1000。
int? _rangeTotal(String? v) =>
    v == null ? null : int.tryParse(v.split('/').last.trim());
