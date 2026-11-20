import 'package:material_ui/material_ui.dart';

import '../models/continue_item.dart';
import '../models/file_item.dart';
import '../services/playback.dart';
import '../theme.dart';
import 'file_tile.dart';

/// 首页“接着看”条目（设计稿 2）：封面、剧名、集数与剩余时间、进度条、上报设备名。
class ContinueTile extends StatelessWidget {
  const ContinueTile({
    super.key,
    required this.item,
    required this.onTap,
    this.art,
  });

  final ContinueItem item;
  final VoidCallback onTap;

  /// 封面（缩略图）；null 时用占位色块。
  final Widget? art;

  @override
  Widget build(BuildContext context) {
    final folder = parentPath(item.path).split('/').last;
    final title = folder.isNotEmpty ? folder : episodeLabel(item.name);
    final episode = item.playsNext
        ? '下一集 · ${episodeLabel(item.next!.item.name)}'
        : folder.isNotEmpty
        ? episodeLabel(item.name)
        : null;
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(14),
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
        child: Row(
          children: [
            ClipRRect(
              borderRadius: BorderRadius.circular(10),
              child: SizedBox(
                width: 112,
                height: 64,
                child: Stack(
                  fit: StackFit.expand,
                  children: [
                    art ??
                        const ColoredBox(
                          color: NightColors.pillow,
                          child: Icon(
                            Icons.movie_outlined,
                            color: NightColors.wick,
                          ),
                        ),
                    const Center(
                      child: Icon(
                        Icons.play_circle_fill,
                        size: 28,
                        color: Color(0xCCDCCFBB),
                      ),
                    ),
                  ],
                ),
              ),
            ),
            const SizedBox(width: 14),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    title,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: const TextStyle(
                      color: NightColors.moon,
                      fontSize: 16,
                    ),
                  ),
                  if (episode != null)
                    Text(
                      episode,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: const TextStyle(color: NightColors.moon),
                    ),
                  const SizedBox(height: 2),
                  Text(
                    _status(),
                    style: const TextStyle(
                      color: NightColors.ash,
                      fontSize: 12,
                      fontFeatures: [FontFeature.tabularFigures()],
                    ),
                  ),
                  const SizedBox(height: 6),
                  if (!item.playsNext)
                    WatchBar(
                      progress: WatchProgress(
                        position: item.position,
                        duration: item.duration,
                      ),
                    ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }

  String _status() {
    final device = item.deviceName.isEmpty ? '' : ' · ${item.deviceName}';
    if (item.playsNext) return '上一集已看完$device';
    final left = (item.duration - item.position).clamp(0, double.infinity);
    final text = left < 1800
        ? '还剩 ${(left / 60).ceil()} 分钟'
        : '看到 ${formatDuration(_sec(item.position))} / ${formatDuration(_sec(item.duration))}';
    return '$text$device';
  }

  static Duration _sec(double s) => Duration(milliseconds: (s * 1000).round());
}
