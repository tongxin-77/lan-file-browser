import 'dart:async';
import 'dart:io';
import 'dart:ui' as ui;

import 'package:flutter/foundation.dart';
import 'package:flutter/painting.dart';
import 'package:flutter_cache_manager/flutter_cache_manager.dart';
import 'package:http/io_client.dart';

import 'fetch_queue.dart';

/// 图片磁盘缓存（键 → 字节）。
abstract class ImageStore {
  /// 已缓存的字节；没有返回 null。
  Future<Uint8List?> read(String key);

  /// 下载 [url] 并以 [key] 存盘（已有同键旧条目时带 If-None-Match，304 复用）。
  Future<Uint8List> download(Uri url, String key);

  Future<void> remove(String key);
}

class _CacheManagerStore implements ImageStore {
  _CacheManagerStore(this._create);

  final CacheManager Function() _create;
  late final CacheManager _m = _create(); // 首次读写时才建（建时即访问缓存目录）

  @override
  Future<Uint8List?> read(String key) async =>
      (await _m.getFileFromCache(key))?.file.readAsBytes();

  @override
  Future<Uint8List> download(Uri url, String key) async =>
      (await _m.downloadFile('$url', key: key)).file.readAsBytes();

  @override
  Future<void> remove(String key) => _m.removeFile(key);
}

/// 图片加载依赖：磁盘缓存、只进内存的直接下载、请求队列。测试可替换 [instance]。
class ImageStores {
  ImageStores({
    required this.thumbs,
    required this.previews,
    required this.fetch,
    FetchQueue? thumbQueue,
    FetchQueue? viewerQueue,
  }) : thumbQueue = thumbQueue ?? FetchQueue(maxConcurrent: 4),
       viewerQueue = viewerQueue ?? FetchQueue(maxConcurrent: 3);

  /// 列表缩略图（160/320/640）：约 6000 张 × 20–60 KB ≈ 200 MB。
  final ImageStore thumbs;

  /// 看图预览图（1280/1920/2560）：约 400 张 × 0.3–1 MB ≈ 250 MB。
  final ImageStore previews;

  /// 不落盘的下载（加密片库、原图）：只进 Flutter 内存图片缓存。
  final Future<Uint8List> Function(Uri url) fetch;

  /// 缩略图网格请求（滑出屏幕即取消排队中的请求）。
  final FetchQueue thumbQueue;

  /// 看图的预览图 / 原图请求（含前后预取）。
  final FetchQueue viewerQueue;

  static ImageStores? _instance;
  static ImageStores get instance => _instance ??= _create();
  static set instance(ImageStores v) => _instance = v;

  static ImageStores _create() {
    // 共用连接池：同一电脑最多 6 条连接（keep-alive 复用），超出的由队列排队
    final http = IOClient(
      HttpClient()
        ..maxConnectionsPerHost = 6
        ..connectionTimeout = const Duration(seconds: 10),
    );
    final service = HttpFileService(httpClient: http);
    // flutter_cache_manager 按条目数上限 + 最近访问时间（touched）淘汰，即 LRU
    CacheManager manager(String key, int count, int days) => CacheManager(
      Config(
        key,
        maxNrOfCacheObjects: count,
        stalePeriod: Duration(days: days),
        fileService: service,
      ),
    );
    return ImageStores(
      thumbs: _CacheManagerStore(() => manager('lfbThumbs', 6000, 60)),
      previews: _CacheManagerStore(() => manager('lfbPreviews', 400, 30)),
      fetch: (url) async {
        final res = await http.get(url);
        if (res.statusCode != 200) {
          throw HttpException('HTTP ${res.statusCode}', uri: url);
        }
        return res.bodyBytes;
      },
    );
  }
}

/// 服务端图片：先查磁盘缓存 [store]（命中直接用，键含 mtime / 版本，内容不会变），
/// 否则经 [queue] 排队下载；[store] 为 null 时只进内存（加密片库、原图）。
/// 相等性只看 [cacheKey]（不含 token），token 变化仍命中内存缓存。
class ServerImage extends ImageProvider<ServerImage> {
  const ServerImage(
    this.url, {
    required this.cacheKey,
    required this.queue,
    this.store,
  });

  final Uri url;
  final String cacheKey;
  final FetchQueue queue;
  final ImageStore? store;

  @override
  Future<ServerImage> obtainKey(ImageConfiguration configuration) =>
      SynchronousFuture(this);

  @override
  ImageStreamCompleter loadImage(
    ServerImage key,
    ImageDecoderCallback decode,
  ) => MultiFrameImageStreamCompleter(
    codec: _load(decode),
    scale: 1,
    debugLabel: cacheKey,
  );

  Future<ui.Codec> _load(ImageDecoderCallback decode) async {
    try {
      final bytes = await loadBytes();
      return await decode(await ui.ImmutableBuffer.fromUint8List(bytes));
    } catch (_) {
      // 失败 / 取消后移出内存缓存，下次显示时重新请求
      scheduleMicrotask(() => PaintingBinding.instance.imageCache.evict(this));
      rethrow;
    }
  }

  /// 取图片字节（磁盘命中 → 排队下载）。
  Future<Uint8List> loadBytes() async {
    final s = store;
    if (s == null) {
      return queue.run(cacheKey, () => ImageStores.instance.fetch(url));
    }
    final hit = await s.read(cacheKey);
    if (hit != null) return hit;
    return queue.run(cacheKey, () => s.download(url, cacheKey));
  }

  @override
  bool operator ==(Object other) =>
      other is ServerImage && other.cacheKey == cacheKey;

  @override
  int get hashCode => cacheKey.hashCode;

  @override
  String toString() => 'ServerImage($cacheKey, disk: ${store != null})';
}
