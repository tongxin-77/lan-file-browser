import 'package:material_ui/material_ui.dart';

import '../models/file_item.dart';
import '../theme.dart';

/// 文件/目录行：kind 图标、名称、大小与修改时间。
class FileTile extends StatelessWidget {
  const FileTile({super.key, required this.item, required this.onTap});

  final FileItem item;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final time = formatTime(item.mtime);
    return ListTile(
      leading: Icon(
        kindIcon(item.kind),
        color: item.isDir || item.kind == FileKind.video
            ? NightColors.wick
            : NightColors.ash,
      ),
      title: Text(item.name, maxLines: 2, overflow: TextOverflow.ellipsis),
      subtitle: Text(
        item.isDir ? time : '${formatSize(item.size)} · $time',
        style: const TextStyle(
          color: NightColors.ash,
          fontFeatures: [FontFeature.tabularFigures()],
        ),
      ),
      onTap: onTap,
    );
  }
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
