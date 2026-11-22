import 'dart:ui' show Size;

/// 服务端缩略图档位（03 §4.5）。
const thumbWidths = [160, 320, 640];

/// 按显示宽度（逻辑像素 × 像素比）取不小于它的最小档位，超过 640 取 640。
int thumbWidthFor(double logicalWidth, double devicePixelRatio) {
  final px = logicalWidth * devicePixelRatio;
  for (final w in thumbWidths) {
    if (w >= px) return w;
  }
  return thumbWidths.last;
}

/// 图片缓存键：去掉 URL 中的 `t`（token 变化不应使缓存失效，也不把 token 写入磁盘缓存索引），
/// 附上文件修改时间（文件变化后缓存自然失效）。
String imageCacheKey(Uri url, DateTime mtime) {
  final q = Map<String, String>.of(url.queryParameters)..remove('t');
  final clean = url.replace(queryParameters: q);
  return '$clean#${mtime.millisecondsSinceEpoch}';
}

/// 预览图档位（03 §4.5，w ≥ 1280 仅图片、JPEG、按 EXIF 转正、不放大）。
const previewWidths = [1280, 1920, 2560];

/// 看图预览档位：屏幕物理像素长边向上取档，超过 2560 取 2560。
int previewWidthFor(Size physicalScreen) {
  final px = physicalScreen.longestSide;
  for (final w in previewWidths) {
    if (w >= px) return w;
  }
  return previewWidths.last;
}

/// 放大后是否需要原图：图片当前显示宽度（[displayedLogicalWidth] × 缩放 × 像素比，物理像素）
/// 超过预览图宽度 [previewPixels].width 的 1.2 倍，且预览图确实被缩小过（长边达到请求档位
/// [requestedWidth]；不足说明原图本就不比预览图大，换原图不会更清楚）。
bool needsOriginal({
  required double displayedLogicalWidth,
  required double scale,
  required double devicePixelRatio,
  required Size previewPixels,
  required int requestedWidth,
}) {
  if (previewPixels.longestSide < requestedWidth) return false;
  return displayedLogicalWidth * scale * devicePixelRatio >
      previewPixels.width * 1.2;
}
