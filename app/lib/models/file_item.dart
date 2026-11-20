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
  });

  factory FileItem.fromJson(Map<String, dynamic> j) => FileItem(
    name: j['name'] as String,
    kind: FileKind.parse(j['kind'] as String?),
    size: (j['size'] as num?)?.toInt() ?? 0,
    mtime: DateTime.fromMillisecondsSinceEpoch(
      (j['mtime'] as num?)?.toInt() ?? 0,
    ),
    hidden: j['hidden'] as bool? ?? false,
  );

  final String name;
  final FileKind kind;
  final int size;
  final DateTime mtime;
  final bool hidden;

  bool get isDir => kind == FileKind.dir;
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
