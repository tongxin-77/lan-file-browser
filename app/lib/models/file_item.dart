/// 条目类别（按扩展名，服务端判定）。
enum FileKind {
  dir,
  video,
  image,
  audio,
  subtitle,
  other;

  static FileKind parse(String? s) =>
      FileKind.values.firstWhere((k) => k.name == s, orElse: () => other);
}

/// `GET /api/list` 的 `items[]` 条目（docs/03-file-api.md 4.2）。
class FileItem {
  const FileItem({
    required this.name,
    required this.kind,
    required this.size,
    required this.mtime,
    this.hidden = false,
    this.hasThumb = false,
    this.progress,
  });

  factory FileItem.fromJson(Map<String, dynamic> j) => FileItem(
    name: j['name'] as String,
    kind: FileKind.parse(j['kind'] as String?),
    size: (j['size'] as num?)?.toInt() ?? 0,
    mtime: DateTime.fromMillisecondsSinceEpoch(
      (j['mtime'] as num?)?.toInt() ?? 0,
    ),
    hidden: j['hidden'] as bool? ?? false,
    hasThumb: j['hasThumb'] as bool? ?? false,
    progress: j['progress'] is Map
        ? WatchProgress.fromJson((j['progress'] as Map).cast<String, dynamic>())
        : null,
  );

  final String name;
  final FileKind kind;
  final int size;
  final DateTime mtime;
  final bool hidden;

  /// 服务端可生成缩略图（03 §4.5），不表示已缓存。
  final bool hasThumb;

  /// 仅视频：本设备观看进度（03 §4.2），无则 null。
  final WatchProgress? progress;

  bool get isDir => kind == FileKind.dir;
}

/// 观看进度（秒）：`/api/list` 条目的 `progress` 字段。
class WatchProgress {
  const WatchProgress({
    required this.position,
    required this.duration,
    this.watched = false,
  });

  factory WatchProgress.fromJson(Map<String, dynamic> j) => WatchProgress(
    position: (j['position'] as num?)?.toDouble() ?? 0,
    duration: (j['duration'] as num?)?.toDouble() ?? 0,
    watched: j['watched'] as bool? ?? false,
  );

  final double position;
  final double duration;
  final bool watched;

  /// 0–1，用于进度条。
  double get fraction =>
      duration > 0 ? (position / duration).clamp(0.0, 1.0) : 0.0;
}

/// 带共享内路径的条目：`/api/next` 的 `item`、`/api/continue` 的 `next`。
class PathItem {
  const PathItem(this.item, this.path);

  factory PathItem.fromJson(Map<String, dynamic> j) =>
      PathItem(FileItem.fromJson(j), j['path'] as String);

  final FileItem item;
  final String path;
}

/// 一页列表结果；[etag] 用于下拉刷新时 `If-None-Match`。
class ListPage {
  const ListPage({required this.items, required this.total, this.etag});

  factory ListPage.fromJson(Map<String, dynamic> j, {String? etag}) => ListPage(
    items: ((j['items'] as List?) ?? const [])
        .map((e) => FileItem.fromJson((e as Map).cast<String, dynamic>()))
        .toList(),
    total: (j['total'] as num?)?.toInt() ?? 0,
    etag: etag,
  );

  final List<FileItem> items;
  final int total;
  final String? etag;
}

/// 拼接共享内相对路径（`/` 分隔，不以 `/` 开头）。
String joinPath(String dir, String name) => dir.isEmpty ? name : '$dir/$name';

/// 父目录；共享根返回空串。
String parentPath(String path) {
  final i = path.lastIndexOf('/');
  return i < 0 ? '' : path.substring(0, i);
}
