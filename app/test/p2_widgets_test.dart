import 'package:flutter_test/flutter_test.dart';
import 'package:lfb_app/models/continue_item.dart';
import 'package:lfb_app/theme.dart';
import 'package:lfb_app/widgets/continue_tile.dart';
import 'package:lfb_app/widgets/up_next_card.dart';
import 'package:material_ui/material_ui.dart';

Widget wrap(Widget child) => MaterialApp(
  theme: buildNightTheme(),
  home: Scaffold(body: Center(child: child)),
);

void main() {
  testWidgets('倒计时卡片：文案、取消、立即播放', (tester) async {
    var cancelled = 0, now = 0;
    await tester.pumpWidget(
      wrap(
        UpNextCard(
          fileName: '星海旅人 S01E13 归途.mkv',
          remaining: 5,
          total: 5,
          onCancel: () => cancelled++,
          onPlayNow: () => now++,
        ),
      ),
    );
    expect(find.text('5 秒后播放 第 13 集 · 归途'), findsOneWidget);
    expect(find.text('星海旅人 S01E13 归途.mkv'), findsOneWidget);
    await tester.tap(find.text('取消'));
    await tester.tap(find.text('立即播放'));
    expect((cancelled, now), (1, 1));

    await tester.pumpWidget(
      wrap(
        UpNextCard(
          fileName: '星海旅人 S01E13 归途.mkv',
          remaining: 2,
          total: 5,
          onCancel: () {},
          onPlayNow: () {},
        ),
      ),
    );
    expect(find.text('2 秒后播放 第 13 集 · 归途'), findsOneWidget);
    final ring = tester.widget<CircularProgressIndicator>(
      find.byType(CircularProgressIndicator),
    );
    expect(ring.value, closeTo(0.4, 1e-9));
  });

  testWidgets('继续观看条目：未看完显示剩余与设备名，看完显示下一集', (tester) async {
    var taps = 0;
    final partial = ContinueItem.fromJson({
      'share': 'k3',
      'path': '星海旅人/S01E12 灯塔.mkv',
      'name': 'S01E12 灯塔.mkv',
      'position': 1090,
      'duration': 1450,
      'watched': false,
      'updatedAt': '2025-06-12T18:30:00Z',
      'deviceName': '客厅电视',
    });
    await tester.pumpWidget(
      wrap(ContinueTile(item: partial, onTap: () => taps++)),
    );
    expect(find.text('星海旅人'), findsOneWidget);
    expect(find.text('第 12 集 · 灯塔'), findsOneWidget);
    expect(find.text('还剩 6 分钟 · 客厅电视'), findsOneWidget);
    expect(find.byType(LinearProgressIndicator), findsOneWidget);
    await tester.tap(find.text('星海旅人'));
    expect(taps, 1);

    final long = ContinueItem.fromJson({
      'share': 'm',
      'path': '雾港往事.mkv',
      'name': '雾港往事.mkv',
      'position': 4360,
      'duration': 7510,
      'watched': false,
      'deviceName': 'Pixel 8',
    });
    await tester.pumpWidget(wrap(ContinueTile(item: long, onTap: () {})));
    expect(find.text('雾港往事'), findsOneWidget);
    expect(find.text('看到 1:12:40 / 2:05:10 · Pixel 8'), findsOneWidget);

    final done = ContinueItem.fromJson({
      'share': 'k3',
      'path': '星海旅人/S01E12 灯塔.mkv',
      'name': 'S01E12 灯塔.mkv',
      'position': 1440,
      'duration': 1450,
      'watched': true,
      'deviceName': '',
      'next': {
        'name': 'S01E13 归途.mkv',
        'kind': 'video',
        'path': '星海旅人/S01E13 归途.mkv',
      },
    });
    await tester.pumpWidget(wrap(ContinueTile(item: done, onTap: () {})));
    expect(find.text('下一集 · 第 13 集 · 归途'), findsOneWidget);
    expect(find.text('上一集已看完'), findsOneWidget);
    expect(find.byType(LinearProgressIndicator), findsNothing);
  });
}
