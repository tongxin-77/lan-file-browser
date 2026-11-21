import 'dart:async';

import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:lfb_app/api/lfb_client.dart';
import 'package:lfb_app/models/file_item.dart';
import 'package:lfb_app/models/share.dart';
import 'package:lfb_app/pages/image_viewer_page.dart';
import 'package:lfb_app/theme.dart';
import 'package:lfb_app/widgets/player_bottom_bar.dart';
import 'package:lfb_app/widgets/settings_sheet.dart';
import 'package:material_ui/material_ui.dart';

const portrait = Size(360, 780);
const landscape = Size(780, 360);

/// 设置逻辑尺寸（devicePixelRatio 1），测试结束复原。
void useSize(WidgetTester tester, Size size) {
  tester.view.physicalSize = size;
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
}

/// 记录 SystemChrome.setPreferredOrientations 的调用。
List<List<String>> recordOrientations(WidgetTester tester) {
  final calls = <List<String>>[];
  tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(
    SystemChannels.platform,
    (call) async {
      if (call.method == 'SystemChrome.setPreferredOrientations') {
        calls.add((call.arguments as List).cast<String>());
      }
      return null;
    },
  );
  addTearDown(
    () => tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(
      SystemChannels.platform,
      null,
    ),
  );
  return calls;
}

Widget app(Widget home) => MaterialApp(theme: buildNightTheme(), home: home);

void main() {
  for (final (name, size) in [('竖屏', portrait), ('横屏', landscape)]) {
    testWidgets('播放器底部控制栏$name不溢出', (tester) async {
      useSize(tester, size);
      await tester.pumpWidget(
        app(
          Scaffold(
            backgroundColor: Colors.black,
            body: Align(
              alignment: Alignment.bottomCenter,
              child: PlayerBottomBar(
                playing: true,
                value: 3600,
                max: 7200,
                duration: const Duration(hours: 2),
                onPlayPause: () {},
                onDrag: (_) {},
                onDragEnd: (_) {},
                onEpisodes: () {},
                onNext: () {},
              ),
            ),
          ),
        ),
      );
      expect(tester.takeException(), isNull);
      // 竖屏只留图标（带提示），横屏带文字
      expect(
        find.byTooltip('选集'),
        size == portrait ? findsOneWidget : findsNothing,
      );
      expect(
        find.text('下一集'),
        size == portrait ? findsNothing : findsOneWidget,
      );
      expect(find.byType(Slider), findsOneWidget);
    });

    testWidgets('设置面板$name不溢出', (tester) async {
      useSize(tester, size);
      await tester.pumpWidget(
        app(
          Scaffold(
            body: SettingsSheet(
              version: Future.value('0.1.0-beta.1'),
              onCheck: () async => '已是最新版本',
            ),
          ),
        ),
      );
      await tester.pump();
      expect(tester.takeException(), isNull);
    });
  }

  testWidgets('设置面板：显示版本，检查更新给出结果', (tester) async {
    final done = Completer<String?>();
    await tester.pumpWidget(
      app(
        Scaffold(
          body: SettingsSheet(
            version: Future.value('0.1.0-beta.1'),
            onCheck: () => done.future,
          ),
        ),
      ),
    );
    await tester.pump();
    expect(find.text('0.1.0-beta.1'), findsOneWidget);

    await tester.tap(find.text('检查更新'));
    await tester.pump();
    expect(find.byType(CircularProgressIndicator), findsOneWidget);

    done.complete('检查失败：无法连接到电脑');
    await tester.pump();
    expect(find.byType(CircularProgressIndicator), findsNothing);
    expect(find.text('检查失败：无法连接到电脑'), findsOneWidget);
  });

  testWidgets('看图：旋转按钮切横/竖屏，横屏不溢出，离开恢复竖屏', (tester) async {
    final calls = recordOrientations(tester);
    useSize(tester, portrait);
    final client = LfbClient(host: 'h', port: 1, token: 't');
    final images = [
      for (final n in ['a.jpg', 'b.jpg'])
        FileItem.fromJson({
          'name': n,
          'kind': 'image',
          'size': 1024,
          'mtime': 1700000000000,
          'hidden': false,
          'hasThumb': false,
          'progress': null,
        }),
    ];
    await tester.pumpWidget(
      app(
        ImageViewerPage(
          client: client,
          share: const Share(
            id: 'k3',
            name: '相册',
            online: true,
            perms: SharePerms(download: true),
          ),
          dir: '',
          images: images,
          initialIndex: 0,
        ),
      ),
    );
    await tester.pump();

    await tester.tap(find.byTooltip('旋转屏幕'));
    await tester.pump();
    expect(calls.last, [
      'DeviceOrientation.landscapeLeft',
      'DeviceOrientation.landscapeRight',
    ]);

    tester.view.physicalSize = landscape; // 系统按要求转成横屏
    await tester.pump();
    expect(tester.takeException(), isNull);
    expect(find.text('保存到手机'), findsOneWidget);

    await tester.pumpWidget(app(const SizedBox())); // 离开看图
    expect(calls.last, ['DeviceOrientation.portraitUp']);
  });
}
