import 'package:material_ui/material_ui.dart';

import '../models/file_item.dart';
import '../theme.dart';

/// 文件/目录行：kind 图标、名称、大小与修改时间。
class FileTile extends StatelessWidget {
  const FileTile({
    super.key,
    required this.item,
    required this.onTap,
    this.onLongPress,
    this.thumbnail,
  });

  final FileItem item;
  final VoidCallback onTap;

  /// 长按菜单（如删除）；null 时长按无反应。
  final VoidCallback? onLongPress;

  /// 有缩略图时替代类别图标（约 64×40）。
  final Widget? thumbnail;

  @override
  Widget build(BuildContext context) {
    final time = formatTime(item.mtime);
    final p = item.progress;
    final watched = p?.watched ?? false;
    final info = Text(
      item.isDir ? time : '${formatSize(item.size)} · $time',
      style: const TextStyle(
        color: NightColors.ash,
        fontFeatures: [FontFeature.tabularFigures()],
      ),
    );
    return Opacity(
      // 看完的集自动变暗（设计稿 3）
      opacity: watched ? 0.6 : 1,
      child: ListTile(
        leading: thumbnail != null
            ? ClipRRect(
                borderRadius: BorderRadius.circular(6),
                child: SizedBox(width: 64, height: 40, child: thumbnail),
              )
            : Icon(
                kindIcon(item.kind),
                color: item.isDir || item.kind == FileKind.video
                    ? NightColors.wick
                    : NightColors.ash,
              ),
        title: Text(item.name, maxLines: 2, overflow: TextOverflow.ellipsis),
        subtitle: p == null
            ? info
            : Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  info,
                  const SizedBox(height: 6),
                  WatchBar(progress: p),
                ],
              ),
        trailing: watched
            ? const Icon(
                Icons.check,
                color: NightColors.moss,
                semanticLabel: '已看完',
              )
            : null,
        onTap: onTap,
        onLongPress: onLongPress,
      ),
    );
  }
}

/// 网格格子：缩略图 + 名称 + 观看进度。
class FileGridTile extends StatelessWidget {
  const FileGridTile({
    super.key,
    required this.item,
    required this.onTap,
    this.onLongPress,
    this.thumbnail,
  });

  final FileItem item;
  final VoidCallback onTap;
  final VoidCallback? onLongPress;
  final Widget? thumbnail;

  @override
  Widget build(BuildContext context) {
    final p = item.progress;
    final watched = p?.watched ?? false;
    return Opacity(
      opacity: watched ? 0.6 : 1,
      child: InkWell(
        onTap: onTap,
        onLongPress: onLongPress,
        borderRadius: BorderRadius.circular(10),
        child: Padding(
          padding: const EdgeInsets.all(4),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Expanded(
                child: ClipRRect(
                  borderRadius: BorderRadius.circular(8),
                  child: Stack(
                    fit: StackFit.expand,
                    children: [
                      thumbnail ??
                          ColoredBox(
                            color: NightColors.pillow,
                            child: Icon(
                              kindIcon(item.kind),
                              size: 32,
                              color: item.isDir || item.kind == FileKind.video
                                  ? NightColors.wick
                                  : NightColors.ash,
                            ),
                          ),
                      if (watched)
                        const Positioned(
                          right: 4,
                          top: 4,
                          child: Icon(
                            Icons.check_circle,
                            size: 18,
                            color: NightColors.moss,
                            semanticLabel: '已看完',
                          ),
                        ),
                      if (p != null && !watched)
                        Positioned(
                          left: 0,
                          right: 0,
                          bottom: 0,
                          child: WatchBar(progress: p, height: 3),
                        ),
                    ],
                  ),
                ),
              ),
              const SizedBox(height: 6),
              Text(
                item.name,
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
                style: const TextStyle(fontSize: 12, color: NightColors.moon),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

/// 琥珀色观看进度条；看完为苔绿。
class WatchBar extends StatelessWidget {
  const WatchBar({super.key, required this.progress, this.height = 2});

  final WatchProgress progress;
  final double height;

  @override
  Widget build(BuildContext context) => ClipRRect(
    borderRadius: BorderRadius.circular(height),
    child: LinearProgressIndicator(
      value: progress.watched ? 1 : progress.fraction,
      minHeight: height,
      color: progress.watched ? NightColors.moss : NightColors.wick,
      backgroundColor: NightColors.seam,
    ),
  );
}

IconData kindIcon(FileKind kind) => switch (kind) {
  FileKind.dir => Icons.folder_outlined,
  FileKind.video => Icons.movie_outlined,
  FileKind.image => Icons.image_outlined,
  FileKind.audio => Icons.music_note_outlined,
  FileKind.subtitle => Icons.subtitles_outlined,
  FileKind.other => Icons.insert_drive_file_outlined,
};

/// 1536 → “1.5 KB”；按 1024 进位。
String formatSize(int bytes) {
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  var v = bytes.toDouble();
  var i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return i == 0 ? '$bytes B' : '${v.toStringAsFixed(1)} ${units[i]}';
}

/// 本地时间 `yyyy-MM-dd HH:mm`。
String formatTime(DateTime t) {
  String two(int n) => n.toString().padLeft(2, '0');
  final l = t.toLocal();
  return '${l.year}-${two(l.month)}-${two(l.day)} ${two(l.hour)}:${two(l.minute)}';
}

/// 播放时间 `H:MM:SS` / `MM:SS`。
String formatDuration(Duration d) {
  String two(int n) => n.toString().padLeft(2, '0');
  final h = d.inHours, m = d.inMinutes % 60, s = d.inSeconds % 60;
  return h > 0 ? '$h:${two(m)}:${two(s)}' : '${two(m)}:${two(s)}';
}
