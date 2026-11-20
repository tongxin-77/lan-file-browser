import 'package:cached_network_image/cached_network_image.dart';
import 'package:material_ui/material_ui.dart';

import '../api/lfb_client.dart';
import '../services/thumbs.dart';
import '../theme.dart';

/// 服务端图片的 [ImageProvider]：加密片库只进内存 imageCache（上锁时清空），
/// 不写 cached_network_image 的磁盘缓存（04 §8）；其他共享内存 + 磁盘缓存，缓存键去掉 token。
ImageProvider serverImageProvider(
  LfbClient client,
  String share,
  Uri url,
  DateTime mtime,
) => client.isVaultShare(share)
    ? NetworkImage(url.toString())
    : CachedNetworkImageProvider(
        url.toString(),
        cacheKey: imageCacheKey(url, mtime),
      );

/// 服务端缩略图（加密片库仅内存缓存，见 [serverImageProvider]）。加载中/失败显示 [placeholder]。
class ServerThumb extends StatelessWidget {
  const ServerThumb({
    super.key,
    required this.client,
    required this.share,
    required this.path,
    required this.mtime,
    required this.placeholder,
    this.width,
    this.height,
  });

  final LfbClient client;
  final String share;
  final String path;
  final DateTime mtime;
  final Widget placeholder;
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
        final url = client.thumbUrl(share, path, w);
        if (client.isVaultShare(share)) {
          return Image(
            image: serverImageProvider(client, share, url, mtime),
            width: width,
            height: height,
            fit: BoxFit.cover,
            frameBuilder: (_, child, frame, sync) =>
                frame == null && !sync ? placeholder : child,
            errorBuilder: (_, _, _) => placeholder,
          );
        }
        return CachedNetworkImage(
          imageUrl: url.toString(),
          cacheKey: imageCacheKey(url, mtime),
          width: width,
          height: height,
          fit: BoxFit.cover,
          fadeInDuration: const Duration(milliseconds: 150),
          placeholder: (_, _) => placeholder,
          errorWidget: (_, _, _) => placeholder,
        );
      },
    );
  }
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
