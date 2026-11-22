import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:http/http.dart' as http;

import '../models/continue_item.dart';
import '../models/file_item.dart';
import '../models/server.dart';
import '../models/share.dart';
import '../models/subtitle.dart';
import '../services/app_update.dart';
import 'api_exception.dart';

/// 服务端 HTTP 客户端：Bearer 鉴权、错误 JSON → [ApiException]、
/// 网络错误 → [NetworkException]；带 token 的请求收到 401 时回调 [onUnauthorized]。
class LfbClient {
  LfbClient({
    required this.host,
    required this.port,
    this.token,
    this.serverId,
    http.Client? httpClient,
    this.onUnauthorized,
    this.timeout = const Duration(seconds: 10),
    this.fallbackHosts = const [],
    this.onHostChanged,
    this.rediscover,
  }) : _http = httpClient ?? http.Client();

  /// 当前使用的地址与端口；[reconnect] 成功换到其他地址时改变。
  String host;
  int port;

  /// 除 [host] 外的已知地址（01 §6），[reconnect] 时一并尝试。
  final List<String> fallbackHosts;

  /// [reconnect] 换了地址后回调（用于保存新的 lastIp / port）。
  final void Function(String host, int port)? onHostChanged;

  /// 已知地址都不通时按 serverId 在局域网查找的新地址（01 §2.2、§6）；找不到返回 null。
  final Future<({List<String> hosts, int port})?> Function()? rediscover;
  final String? token;

  /// 已配对服务器的 id；重连时 ping 结果需与之匹配（01 §6）。
  final String? serverId;
  final void Function()? onUnauthorized;
  final Duration timeout;
  final http.Client _http;

  /// 加密片库解锁会话（04 §5，仅内存）：非空时所有请求附 `X-Vault`，
  /// 白名单直链附 `?v=`（见 [mediaUrl]）。由 VaultController 维护。
  VaultAccess? vault;

  /// 已解锁片库内的请求收到 404：会话已失效（04 §8），由 VaultController 上锁回首页。
  void Function()? onVaultLost;

  Uri _uri(String path, [Map<String, String>? query]) => Uri(
    scheme: 'http',
    host: host,
    port: port,
    path: path,
    queryParameters: query,
  );

  /// `GET /api/ping`（免鉴权；带 token 时 needPair 反映 token 是否有效）。
  Future<PingInfo> ping({Duration? timeout}) async {
    final res = await _send('GET', '/api/ping', timeout: timeout);
    return PingInfo.fromJson(_json(res));
  }

  /// ping 并校验：id 与 [serverId] 不符视为 IP 已被其他电脑占用（01 §6）；
  /// 带 token 而 needPair=true 表示 token 已失效，触发 [onUnauthorized]。
  Future<PingInfo> verify({Duration? timeout}) async {
    final p = await ping(timeout: timeout);
    if (serverId != null && p.id != serverId) {
      throw const NetworkException('该地址上已不是原来的电脑');
    }
    if (token != null && p.needPair) {
      onUnauthorized?.call();
      throw const ApiException(401, 'UNAUTHORIZED', '授权已失效，请重新配对');
    }
    return p;
  }

  /// 断线重连：并发 [verify] [host] 与 [fallbackHosts]，第一个成功的地址设为 [host]；
  /// 都不通时用 [rediscover] 在局域网按 serverId 查找新地址再试（01 §6）。
  /// 只有 id 匹配的电脑明确表示 token 无效时才触发 [onUnauthorized] 并抛 401；
  /// 其余失败（超时、不可达、id 不符、5xx）一律抛 [NetworkException]。
  Future<PingInfo> reconnect({
    Duration timeout = const Duration(seconds: 3),
  }) async {
    var r = await _tryHosts({host, ...fallbackHosts}.toList(), port, timeout);
    if (r.ok == null && !r.unauthorized) {
      final found = await rediscover?.call();
      if (found != null) r = await _tryHosts(found.hosts, found.port, timeout);
    }
    if (r.ok case (final h, final pt, final p)) {
      if (h != host || pt != port) {
        host = h;
        port = pt;
        onHostChanged?.call(h, pt);
      }
      return p;
    }
    if (r.unauthorized) {
      onUnauthorized?.call();
      throw const ApiException(401, 'UNAUTHORIZED', '授权已失效，请重新配对');
    }
    throw const NetworkException();
  }

  /// 并发 verify：[ok] 为第一个成功的 (地址, 端口, ping)；都失败时
  /// [unauthorized] 表示是否有 id 匹配的电脑说 token 无效。
  Future<({(String, int, PingInfo)? ok, bool unauthorized})> _tryHosts(
    List<String> hosts,
    int port,
    Duration timeout,
  ) async {
    if (hosts.isEmpty) return (ok: null, unauthorized: false);
    final done =
        Completer<({(String, int, PingInfo)? ok, bool unauthorized})>();
    var pending = hosts.length;
    var unauthorized = false;
    for (final h in hosts) {
      LfbClient(
            host: h,
            port: port,
            token: token,
            serverId: serverId,
            httpClient: _http,
          )
          .verify(timeout: timeout)
          .then((p) {
            if (!done.isCompleted) {
              done.complete((ok: (h, port, p), unauthorized: false));
            }
          })
          .catchError((Object e) {
            if (e is ApiException && e.isUnauthorized) unauthorized = true;
          })
          .whenComplete(() {
            if (--pending == 0 && !done.isCompleted) {
              done.complete((ok: null, unauthorized: unauthorized));
            }
          });
    }
    return done.future;
  }

  /// `POST /api/pair`：code 为 6 位 PIN 或二维码一次性码。
  Future<PairResult> pair({
    required String code,
    required String deviceName,
    required String deviceId,
  }) async {
    final res = await _send(
      'POST',
      '/api/pair',
      body: {'code': code, 'deviceName': deviceName, 'deviceId': deviceId},
    );
    return PairResult.fromJson(_json(res));
  }

  /// `GET /api/shares`
  Future<List<Share>> shares() async {
    final res = await _send('GET', '/api/shares');
    final items = (_json(res)['items'] as List?) ?? const [];
    return items
        .map((e) => Share.fromJson((e as Map).cast<String, dynamic>()))
        .toList();
  }

  /// `GET /api/list`；传 [etag] 且服务端返回 304 时结果为 null（列表未变）。
  Future<ListPage?> list(
    String share,
    String path, {
    int offset = 0,
    int limit = 200,
    String? etag,
  }) async {
    final res = await _send(
      'GET',
      '/api/list',
      query: {
        'share': share,
        'path': path,
        'offset': '$offset',
        'limit': '$limit',
      },
      headers: {'If-None-Match': ?etag},
    );
    if (res.statusCode == 304) return null;
    return ListPage.fromJson(_json(res), etag: res.headers['etag']);
  }

  /// 播放器直接使用的文件 URL（`?t=` 仅 /api/file 等白名单接口有效，01 §3.3）。
  Uri fileUrl(String share, String path) =>
      mediaUrl('/api/file', {'share': share, 'path': path});

  /// 缩略图 / 预览图 URL（`?t=` 白名单接口）；[width] 为 160/320/640（预览 1280/1920/2560）。
  /// [ver] 为缓存版本（[FileItem.thumbVer]，03 §4.5），null 时不带。
  Uri thumbUrl(String share, String path, int width, {String? ver}) => mediaUrl(
    '/api/thumb',
    {'share': share, 'path': path, 'w': '$width', 'ver': ?ver},
  );

  /// `GET /api/next`：下一集，无则 null（03 §4.6）。
  Future<PathItem?> next(String share, String path) async {
    final res = await _send(
      'GET',
      '/api/next',
      query: {'share': share, 'path': path},
    );
    final item = _json(res)['item'];
    return item is Map ? PathItem.fromJson(item.cast<String, dynamic>()) : null;
  }

  /// `GET /api/subtitles`：同目录外挂字幕，按 exact/episode/season 排列（03 §4.7）。
  Future<List<SubtitleInfo>> subtitles(String share, String path) async {
    final res = await _send(
      'GET',
      '/api/subtitles',
      query: {'share': share, 'path': path},
    );
    final items = (_json(res)['items'] as List?) ?? const [];
    return items
        .map((e) => SubtitleInfo.fromJson((e as Map).cast<String, dynamic>()))
        .toList();
  }

  /// `POST /api/progress`（秒）。
  Future<void> reportProgress(
    String share,
    String path, {
    required Duration position,
    required Duration duration,
  }) async {
    await _send(
      'POST',
      '/api/progress',
      body: {
        'share': share,
        'path': path,
        'position': position.inMilliseconds / 1000,
        'duration': duration.inMilliseconds / 1000,
      },
    );
  }

  /// `GET /api/continue`：继续观看（家庭共享进度，03 §4.8）。
  Future<List<ContinueItem>> continueWatching() async {
    final res = await _send('GET', '/api/continue');
    final items = (_json(res)['items'] as List?) ?? const [];
    return items
        .map((e) => ContinueItem.fromJson((e as Map).cast<String, dynamic>()))
        .toList();
  }

  /// `DELETE /api/continue?share=&path=`：从本设备继续观看列表移除一条。
  Future<void> removeContinue(String share, String path) async {
    await _send(
      'DELETE',
      '/api/continue',
      query: {'share': share, 'path': path},
    );
  }

  /// `DELETE /api/continue`：清空本设备继续观看列表。
  Future<void> clearContinue() async {
    await _send('DELETE', '/api/continue');
  }

  /// `POST /api/delete`：移入电脑回收站（需 modify 权限，03 §4.10）。
  /// 全部成功返回空列表；部分失败返回失败条目。
  Future<List<({String path, String code, String message})>> deleteFiles(
    String share,
    List<String> paths,
  ) async {
    final res = await _send(
      'POST',
      '/api/delete',
      body: {'share': share, 'paths': paths},
    );
    if (res.statusCode == 204 || res.bodyBytes.isEmpty) return const [];
    final failed = (_json(res)['failed'] as List?) ?? const [];
    return [
      for (final f in failed.cast<Map>())
        (
          path: f['path'] as String? ?? '',
          code: f['code'] as String? ?? '',
          message: f['message'] as String? ?? '删除失败',
        ),
    ];
  }

  /// `GET /api/download` 整文件读入内存（用于保存图片；需共享的下载权限）。
  Future<Uint8List> download(String share, String path) async {
    final res = await _send(
      'GET',
      '/api/download',
      query: {'share': share, 'path': path},
      timeout: const Duration(seconds: 60),
    );
    return res.bodyBytes;
  }

  /// 预取文件开头 [bytes] 字节并丢弃（05 §4.4：唤醒电脑硬盘、进系统缓存）。失败静默。
  Future<void> prefetch(
    String share,
    String path, {
    int bytes = 8 * 1024 * 1024,
  }) async {
    final req = http.Request(
      'GET',
      _uri('/api/file', {'share': share, 'path': path}),
    );
    if (token != null) req.headers['Authorization'] = 'Bearer $token';
    final v = vault;
    if (v != null) req.headers['X-Vault'] = v.session;
    req.headers['Range'] = 'bytes=0-${bytes - 1}';
    try {
      final res = await _http.send(req).timeout(timeout);
      await res.stream.drain<void>().timeout(const Duration(seconds: 60));
    } on Object {
      // 预取只是优化
    }
  }

  Future<http.Response> _send(
    String method,
    String path, {
    Map<String, String>? query,
    Map<String, String>? headers,
    Object? body,
    Duration? timeout,
  }) async {
    final req = http.Request(method, _uri(path, query));
    if (token != null) req.headers['Authorization'] = 'Bearer $token';
    final v = vault;
    if (v != null) req.headers['X-Vault'] = v.session;
    if (headers != null) req.headers.addAll(headers);
    if (body != null) {
      req.headers['Content-Type'] = 'application/json';
      req.body = jsonEncode(body);
    }
    final http.Response res;
    try {
      final streamed = await _http.send(req).timeout(timeout ?? this.timeout);
      res = await http.Response.fromStream(streamed)
          .timeout(timeout ?? this.timeout);
    } on TimeoutException {
      throw const NetworkException('连接超时，请确认电脑已开机且在同一网络');
    } on SocketException {
      throw const NetworkException();
    } on http.ClientException {
      throw const NetworkException();
    }
    if (res.statusCode >= 400) {
      final e = parseError(res);
      if (e.isUnauthorized && token != null) onUnauthorized?.call();
      if (res.statusCode == 404 && isVaultShare(query?['share'])) {
        onVaultLost?.call();
      }
      throw e;
    }
    return res;
  }

  static Map<String, dynamic> _json(http.Response res) {
    try {
      return (jsonDecode(utf8.decode(res.bodyBytes)) as Map)
          .cast<String, dynamic>();
    } on Object {
      throw ApiException(res.statusCode, 'BAD_RESPONSE', '服务器返回了无法识别的数据');
    }
  }

  /// 把错误响应转为 [ApiException]；非统一格式时用通用文案。
  static ApiException parseError(http.Response res) {
    try {
      final err = (jsonDecode(utf8.decode(res.bodyBytes)) as Map)['error'];
      if (err is Map && err['code'] is String) {
        return ApiException(
          res.statusCode,
          err['code'] as String,
          (err['message'] as String?) ?? '请求失败（${res.statusCode}）',
        );
      }
    } on Object {
      // 落到下方通用错误
    }
    return ApiException(
      res.statusCode,
      'HTTP_${res.statusCode}',
      '请求失败（${res.statusCode}）',
    );
  }

  void close() => _http.close();

  /// 需直链的媒体 URL：附 `?t=`；仅对已解锁片库内的 file/download/thumb 附 `?v=`（04 §5），
  /// 普通共享的直链不带会话。
  Uri mediaUrl(String apiPath, Map<String, String> query) => _uri(apiPath, {
    ...query,
    't': ?token,
    if (vaultUrlPaths.contains(apiPath) && isVaultShare(query['share']))
      'v': ?vault?.session,
  });

  /// 允许 `?v=` 的接口（与服务端白名单一致，不含 APK 下载）。
  static const vaultUrlPaths = {'/api/file', '/api/download', '/api/thumb'};

  /// [shareId] 是否为当前已解锁的加密片库：其内容不得写入任何本地持久缓存（04 §8）。
  bool isVaultShare(String? shareId) =>
      shareId != null && (vault?.shareIds.contains(shareId) ?? false);

  /// `POST /api/vault/unlock`：始终 200；密码不对时 session 为 null。
  Future<({String? session, List<Share> shares})> unlockVault(
    String password,
  ) async {
    final res = await _send(
      'POST',
      '/api/vault/unlock',
      body: {'password': password},
    );
    final j = _json(res);
    final items = (j['shares'] as List?) ?? const [];
    return (
      session: j['session'] as String?,
      shares: items
          .map((e) => Share.fromJson((e as Map).cast<String, dynamic>()))
          .toList(),
    );
  }

  /// `POST /api/vault/lock`：销毁服务端会话。
  Future<void> lockVault(String session) async {
    await _send('POST', '/api/vault/lock', headers: {'X-Vault': session});
  }

  /// `GET /api/app/latest`；服务端尚无版本信息（404）时返回 null。
  Future<AppRelease?> appLatest() async {
    try {
      return AppRelease.fromJson(_json(await _send('GET', '/api/app/latest')));
    } on ApiException catch (e) {
      if (e.statusCode == 404) return null;
      rethrow;
    }
  }
}

/// 加密片库解锁会话与其解锁的 shareId。
class VaultAccess {
  const VaultAccess(this.session, this.shareIds);
  final String session;
  final Set<String> shareIds;
}
