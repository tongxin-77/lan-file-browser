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
  }) : _http = httpClient ?? http.Client();

  final String host;
  final int port;
  final String? token;

  /// 已配对服务器的 id；重连时 ping 结果需与之匹配（01 §6）。
  final String? serverId;
  final void Function()? onUnauthorized;
  final Duration timeout;
  final http.Client _http;

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
      _uri('/api/file', {'share': share, 'path': path, 't': ?token});

  /// 缩略图 URL（`?t=` 白名单接口）；[width] 为 160/320/640。
  Uri thumbUrl(String share, String path, int width) => _uri('/api/thumb', {
    'share': share,
    'path': path,
    'w': '$width',
    't': ?token,
  });

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
}
