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
