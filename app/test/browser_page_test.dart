import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:lfb_app/api/lfb_client.dart';
import 'package:lfb_app/models/share.dart';
import 'package:lfb_app/pages/browser_page.dart';
import 'package:lfb_app/theme.dart';
import 'package:material_ui/material_ui.dart';

Map<String, Object?> item(String name, String kind, {int size = 0}) => {
  'name': name,
  'kind': kind,
  'size': size,
  'mtime': 1700000000000,
  'hidden': false,
  'hasThumb': false,
  'progress': null,
};

void main() {
  testWidgets('浏览：列出条目、进入子目录、面包屑、非视频提示', (tester) async {
    final paths = <String>[];
    final client = LfbClient(
      host: 'h',
      port: 1,
      token: 't',
      httpClient: MockClient((req) async {
        final path = req.url.queryParameters['path']!;
        paths.add(path);
        final items = path.isEmpty
            ? [item('S01', 'dir'), item('说明.txt', 'other', size: 2048)]
            : [item('E01.mkv', 'video', size: 3 * 1024 * 1024)];
        return http.Response.bytes(
          utf8.encode(jsonEncode({'items': items, 'total': items.length})),
          200,
          headers: {'content-type': 'application/json; charset=utf-8'},
        );
      }),
    );

    await tester.pumpWidget(
      MaterialApp(
        theme: buildNightTheme(),
        home: BrowserPage(
          client: client,
          share: const Share(
            id: 'k3',
            name: '动漫',
            online: true,
            perms: SharePerms(),
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.text('S01'), findsOneWidget);
    expect(find.text('说明.txt'), findsOneWidget);
    expect(find.textContaining('2.0 KB'), findsOneWidget);

    await tester.tap(find.text('说明.txt'));
    await tester.pump();
    expect(find.text('暂不支持打开'), findsOneWidget);
    // 等提示消失，避免浮动 SnackBar 挡住底部面包屑
    tester
        .state<ScaffoldMessengerState>(find.byType(ScaffoldMessenger))
        .hideCurrentSnackBar();
    await tester.pumpAndSettle();

    await tester.tap(find.text('S01'));
    await tester.pumpAndSettle();
    expect(paths, ['', 'S01']);
    expect(find.text('E01.mkv'), findsOneWidget);
    expect(find.textContaining('3.0 MB'), findsOneWidget);

    // 面包屑点共享名回到根目录
    await tester.tap(find.widgetWithText(TextButton, '动漫'));
    await tester.pumpAndSettle();
    expect(paths.last, '');
    expect(find.text('S01'), findsOneWidget);
  });
}
