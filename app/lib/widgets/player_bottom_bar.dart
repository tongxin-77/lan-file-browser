import 'package:material_ui/material_ui.dart';

import 'file_tile.dart';
import '../theme.dart';

/// 播放器底部控制栏：播放键在左下角（双手拇指）、进度条、选集、下一集。
/// 屏幕较窄（竖屏）时“选集”“下一集”只显示图标，避免挤掉进度条。
class PlayerBottomBar extends StatelessWidget {
  const PlayerBottomBar({
    super.key,
    required this.playing,
    required this.value,
    required this.max,
    required this.duration,
    required this.onPlayPause,
    required this.onDrag,
    required this.onDragEnd,
    required this.onEpisodes,
    required this.onNext,
  });

  final bool playing;

  /// 当前进度与总长（秒）。
  final double value;
  final double max;
  final Duration duration;
  final VoidCallback onPlayPause;
  final ValueChanged<double> onDrag;
  final ValueChanged<double> onDragEnd;
  final VoidCallback onEpisodes;
  final VoidCallback onNext;

  @override
  Widget build(BuildContext context) {
    const numStyle = TextStyle(
      color: NightColors.moon,
      fontFeatures: [FontFeature.tabularFigures()],
    );
    final compact = MediaQuery.sizeOf(context).width < 600;
    final buttonStyle = TextButton.styleFrom(
      foregroundColor: NightColors.moon,
      minimumSize: const Size(48, 48),
    );
    Widget action(IconData icon, String label, VoidCallback onPressed) =>
        compact
        ? IconButton(
            tooltip: label,
            color: NightColors.moon,
            icon: Icon(icon),
            onPressed: onPressed,
          )
        : TextButton.icon(
            onPressed: onPressed,
            style: buttonStyle,
            icon: Icon(icon),
            label: Text(label),
          );
    return Row(
      children: [
        IconButton(
          iconSize: 36,
          color: NightColors.wick,
          icon: Icon(playing ? Icons.pause : Icons.play_arrow),
          onPressed: onPlayPause,
        ),
        Text(
          formatDuration(Duration(milliseconds: (value * 1000).round())),
          style: numStyle,
        ),
        Expanded(
          child: Slider(
            value: value,
            max: max > 0 ? max : 1,
            onChanged: max > 0 ? onDrag : null,
            // 松手才跳转，落点精确
            onChangeEnd: max > 0 ? onDragEnd : null,
          ),
        ),
        Text(formatDuration(duration), style: numStyle),
        const SizedBox(width: 8),
        action(Icons.grid_view, '选集', onEpisodes),
        action(Icons.skip_next, '下一集', onNext),
      ],
    );
  }
}
