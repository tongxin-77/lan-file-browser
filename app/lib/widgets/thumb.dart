import 'package:material_ui/material_ui.dart';

import '../api/lfb_client.dart';
import '../services/fetch_queue.dart';
import '../services/server_image.dart';
import '../services/thumbs.dart';
import '../theme.dart';

/// 服务端图片的 [ImageProvider]（03 §4.5）：缓存键去掉 token、带 mtime（及 URL 中的版本参数）。
/// 普通共享的缩略图 / 预览图进内存 + 磁盘缓存；加密片库（04 §8）与原图 `/api/file` 只进内存
/// （上锁时清空）。缩略图走 [ImageStores.thumbQueue]，预览图与原图走 [ImageStores.viewerQueue]。
ServerImage serverImageProvider(
  LfbClient client,
  String share,
  Uri url,
  DateTime mtime,
) {
  final stores = ImageStores.instance;
  final w = int.tryParse(url.queryParameters['w'] ?? '') ?? 0;
  final isThumb = url.path == '/api/thumb' && w < previewWidths.first;
  final ImageStore? store =
      client.isVaultShare(share) || url.path != '/api/thumb'
      ? null
      : isThumb
      ? stores.thumbs
      : stores.previews;
  return ServerImage(
    url,
    cacheKey: imageCacheKey(url, mtime),
    queue: isThumb ? stores.thumbQueue : stores.viewerQueue,
    store: store,
  );
}

/// 内存或磁盘里已有的列表缩略图（大档优先），用作看图的底图；都没有返回 null。
Future<ServerImage?> cachedThumb(
  LfbClient client,
  String share,
  String path,
  DateTime mtime, {
  String? ver,
}) async {
  final all = [
    for (final w in thumbWidths.reversed)
      serverImageProvider(
        client,
        share,
        client.thumbUrl(share, path, w, ver: ver),
        mtime,
      ),
  ];
  final memory = PaintingBinding.instance.imageCache;
  for (final p in all) {
    if (memory.containsKey(p)) return p;
  }
  for (final p in all) {
    if (await p.store?.read(p.cacheKey) != null) return p;
  }
  return null;
}

/// 文件被删除后清掉本机各档缩略图 / 预览图缓存（普通共享含磁盘缓存；片库只在内存）。失败静默。
void evictServerThumbs(
  LfbClient client,
  String share,
  String path,
  DateTime mtime, {
  String? ver,
}) {
  for (final w in [...thumbWidths, ...previewWidths]) {
    final p = serverImageProvider(
      client,
      share,
      client.thumbUrl(share, path, w, ver: ver),
      mtime,
    );
    p.evict().ignore();
    p.store?.remove(p.cacheKey).ignore();
  }
}

/// 服务端缩略图（加密片库仅内存缓存，见 [serverImageProvider]）。加载中/失败显示 [placeholder]。
/// 显示期间在队列里优先；滑出屏幕时取消尚未开始的请求。
class ServerThumb extends StatelessWidget {
  const ServerThumb({
    super.key,
    required this.client,
    required this.share,
    required this.path,
    required this.mtime,
    required this.placeholder,
    this.ver,
    this.width,
    this.height,
  });

  final LfbClient client;
  final String share;
  final String path;
  final DateTime mtime;
  final Widget placeholder;

  /// 列表条目的缩略图缓存版本（[FileItem.thumbVer]）。
  final String? ver;
  final double? width;
  final double? height;

  @override
  Widget build(BuildContext context) {
    return LayoutBuilder(
      builder: (context, c) {
        final logical = width ?? (c.hasBoundedWidth ? c.maxWidth : 160);
        final w = thumbWidthFor(
          logical,
          MediaQuery.devicePixelRatioOf(context),
        );
        final image = serverImageProvider(
          client,
          share,
          client.thumbUrl(share, path, w, ver: ver),
          mtime,
        );
        return QueueRetain(
          queue: image.queue,
          id: image.cacheKey,
          child: Image(
            image: image,
            width: width,
            height: height,
            fit: BoxFit.cover,
            gaplessPlayback: true,
            frameBuilder: (_, child, frame, sync) => sync
                ? child
                : Stack(
                    fit: StackFit.expand,
                    children: [
                      placeholder,
                      AnimatedOpacity(
                        opacity: frame == null ? 0 : 1,
                        duration: const Duration(milliseconds: 150),
                        child: child,
                      ),
                    ],
                  ),
            errorBuilder: (_, _, _) => placeholder,
          ),
        );
      },
    );
  }
}

/// 在显示期间对 [queue] 中的 [id] 计引用（[FetchQueue.retain]），移除时释放。
class QueueRetain extends StatefulWidget {
  const QueueRetain({
    super.key,
    required this.queue,
    required this.id,
    required this.child,
  });

  final FetchQueue queue;
  final Object id;
  final Widget child;

  @override
  State<QueueRetain> createState() => _QueueRetainState();
}

class _QueueRetainState extends State<QueueRetain> {
  @override
  void initState() {
    super.initState();
    widget.queue.retain(widget.id);
  }

  @override
  void didUpdateWidget(QueueRetain old) {
    super.didUpdateWidget(old);
    if (old.queue != widget.queue || old.id != widget.id) {
      widget.queue.retain(widget.id);
      old.queue.release(old.id);
    }
  }

  @override
  void dispose() {
    widget.queue.release(widget.id);
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => widget.child;
}

/// 无缩略图时的占位：枕套色块 + 类别图标。
class ThumbPlaceholder extends StatelessWidget {
  const ThumbPlaceholder({super.key, required this.icon, this.color});

  final IconData icon;
  final Color? color;

  @override
  Widget build(BuildContext context) => ColoredBox(
    color: NightColors.pillow,
    child: Center(child: Icon(icon, color: color ?? NightColors.ash)),
  );
}
