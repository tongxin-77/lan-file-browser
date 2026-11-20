import 'package:material_ui/material_ui.dart';

import '../services/playback.dart';
import '../theme.dart';

/// 片尾自动下一集卡片（设计稿 4b）：倒计时圆环 + “N 秒后播放 第 13 集 · 归途”，取消 / 立即播放。
class UpNextCard extends StatelessWidget {
  const UpNextCard({
    super.key,
    required this.fileName,
    required this.remaining,
    required this.total,
    required this.onCancel,
    required this.onPlayNow,
  });

  final String fileName;
  final int remaining;
  final int total;
  final VoidCallback onCancel;
  final VoidCallback onPlayNow;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: 340,
      padding: const EdgeInsets.fromLTRB(16, 16, 16, 12),
      decoration: BoxDecoration(
        color: NightColors.pillow.withValues(alpha: 0.94),
        borderRadius: BorderRadius.circular(20),
        border: Border.all(color: NightColors.seam),
        boxShadow: [
          BoxShadow(
            color: NightColors.wick.withValues(alpha: 0.18),
            blurRadius: 40,
          ),
        ],
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            children: [
              SizedBox(
                width: 52,
                height: 52,
                child: Stack(
                  alignment: Alignment.center,
                  children: [
                    SizedBox.expand(
                      child: CircularProgressIndicator(
                        value: total > 0 ? remaining / total : 0,
                        strokeWidth: 3,
                        backgroundColor: NightColors.seam,
                      ),
                    ),
                    Text(
                      '$remaining',
                      style: const TextStyle(
                        color: NightColors.wick,
                        fontSize: 20,
                        fontFeatures: [FontFeature.tabularFigures()],
                      ),
                    ),
                  ],
                ),
              ),
              const SizedBox(width: 14),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      '$remaining 秒后播放 ${episodeLabel(fileName)}',
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                      style: const TextStyle(
                        color: NightColors.moon,
                        fontSize: 16,
                      ),
                    ),
                    const SizedBox(height: 4),
                    Text(
                      fileName,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: const TextStyle(
                        color: NightColors.ash,
                        fontSize: 12,
                      ),
                    ),
                  ],
                ),
              ),
            ],
          ),
          const SizedBox(height: 12),
          Row(
            mainAxisAlignment: MainAxisAlignment.end,
            children: [
              TextButton(onPressed: onCancel, child: const Text('取消')),
              const SizedBox(width: 8),
              FilledButton(onPressed: onPlayNow, child: const Text('立即播放')),
            ],
          ),
        ],
      ),
    );
  }
}
