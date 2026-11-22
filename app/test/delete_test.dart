import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:lfb_app/api/lfb_client.dart';
import 'package:lfb_app/models/server.dart';
import 'package:lfb_app/models/share.dart';
import 'package:lfb_app/pages/browser_page.dart';
import 'package:lfb_app/pages/home_page.dart';
import 'package:lfb_app/theme.dart';
import 'package:material_ui/material_ui.dart';

http.Response jsonRes(Object body, [int status = 200]) => http.Response.bytes(
  utf8.encode(jsonEncode(body)),
  status,
  headers: {'content-type': 'application/json; charset=utf-8'},
);

http.Response errRes(int status, String code) => jsonRes({
  'error': {'code': code, 'message': code},
}, status);

Map<String, Object?> cont(String path) => {
  'share': 'k3',
  'path': path,
  'name': path.split('/').last,
  'position': 100,
  'duration': 1400,
  'watched': false,
  'deviceName': '',
};

Map<String, Object?> file(String name, String kind) => {
  'name': name,
  'kind': kind,
  'size': 10,
  'mtime': 1700000000000,
  'hidden': false,
  'hasThumb': false,
  'progress': null,
};

Share share({required bool modify}) => Share(
  id: 'k3',
  name: '动漫',
  online: true,
  perms: SharePerms(modify: modify),
);

void main() {
  test('perms 删除权限兼容 modify 与 delete 两种字段名', () {
    expect(SharePerms.fromJson({'modify': true}).modify, isTrue);
    expect(SharePerms.fromJson({'delete': true}).modify, isTrue);
    expect(SharePerms.fromJson({}).modify, isFalse);
  });

  test('deleteFiles：204 全部成功，200 返回失败条目', () async {
    final bodies = <Object?>[];
    var partial = false;
    final c = LfbClient(
      host: 'h',
      port: 1,
      token: 't',
      httpClient: MockClient((req) async {
        bodies.add(jsonDecode(req.body));
        return partial
            ? jsonRes({
                'failed': [
                  {'path': 'a.mkv', 'code': 'IN_USE', 'message': '占用'},
                ],
              })
            : http.Response('', 204);
      }),
    );
    expect(await c.deleteFiles('k3', ['a.mkv']), isEmpty);
    partial = true;
    final failed = await c.deleteFiles('k3', ['a.mkv', 'b.mkv']);
    expect(failed.single.code, 'IN_USE');
    expect(bodies.last, {
      'share': 'k3',
      'paths': ['a.mkv', 'b.mkv'],
    });
  });

  group('首页接着看：移除与清空', () {
    late List<http.Request> seen;
    late int deleteStatus;

    Future<void> pumpHome(WidgetTester tester) async {
      seen = [];
      final client = LfbClient(
        host: 'h',
        port: 1,
        token: 't',
        httpClient: MockClient((req) async {
          seen.add(req);
          if (req.method == 'DELETE') {
            return deleteStatus == 204
                ? http.Response('', 204)
                : errRes(deleteStatus, 'INTERNAL');
          }
          return switch (req.url.path) {
            '/api/continue' => jsonRes({
              'items': [cont('甲剧/E01.mkv'), cont('乙剧/E02.mkv')],
            }),
            _ => jsonRes({'items': []}),
          };
        }),
      );
      await tester.pumpWidget(
        MaterialApp(
          theme: buildNightTheme(),
          home: HomePage(
            server: const SavedServer(
              serverId: 's',
              name: '书房电脑',
              lastIp: 'h',
              port: 1,
            ),
            client: client,
          ),
        ),
      );
      await tester.pumpAndSettle();
    }

    testWidgets('长按 → 从列表移除，发 DELETE 带 share/path', (tester) async {
      deleteStatus = 204;
      await pumpHome(tester);
      expect(find.text('甲剧'), findsOneWidget);
      await tester.longPress(find.text('甲剧'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('从列表移除'));
      await tester.pumpAndSettle();
      expect(find.text('甲剧'), findsNothing);
      expect(find.text('乙剧'), findsOneWidget);
      final del = seen.singleWhere((r) => r.method == 'DELETE');
      expect(del.url.path, '/api/continue');
      expect(del.url.queryParameters, {'share': 'k3', 'path': '甲剧/E01.mkv'});
    });

    testWidgets('移除失败回滚并提示', (tester) async {
      deleteStatus = 500;
      await pumpHome(tester);
      await tester.longPress(find.text('甲剧'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('从列表移除'));
      await tester.pumpAndSettle();
      expect(find.text('甲剧'), findsOneWidget);
      expect(find.textContaining('移除失败'), findsOneWidget);
    });

    testWidgets('清空需确认，确认后发无参数 DELETE', (tester) async {
      deleteStatus = 204;
      await pumpHome(tester);
      await tester.tap(find.text('清空'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('取消'));
      await tester.pumpAndSettle();
      expect(seen.where((r) => r.method == 'DELETE'), isEmpty);
      expect(find.text('甲剧'), findsOneWidget);

      await tester.tap(find.text('清空'));
      await tester.pumpAndSettle();
      await tester.tap(find.widgetWithText(TextButton, '清空').last);
      await tester.pumpAndSettle();
      expect(find.text('接着看'), findsNothing);
      final del = seen.singleWhere((r) => r.method == 'DELETE');
      expect(del.url.queryParameters, isEmpty);
    });
  });

  group('文件列表删除', () {
    late List<http.Request> seen;
    late http.Response Function() deleteRes;

    Future<void> pumpBrowser(
      WidgetTester tester, {
      required bool modify,
    }) async {
      seen = [];
      final client = LfbClient(
        host: 'h',
        port: 1,
        token: 't',
        httpClient: MockClient((req) async {
          seen.add(req);
          if (req.url.path == '/api/delete') return deleteRes();
          return jsonRes({
            'items': [file('E01.mkv', 'other')],
            'total': 1,
          });
        }),
      );
      await tester.pumpWidget(
        MaterialApp(
          theme: buildNightTheme(),
          home: BrowserPage(
            client: client,
            share: share(modify: modify),
          ),
        ),
      );
      await tester.pumpAndSettle();
    }

    Future<void> deleteFirst(WidgetTester tester) async {
      await tester.longPress(find.text('E01.mkv'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('删除'));
      await tester.pumpAndSettle();
      expect(find.textContaining('电脑的回收站'), findsOneWidget);
      await tester.tap(find.widgetWithText(TextButton, '删除'));
      await tester.pumpAndSettle();
    }

    testWidgets('无删除权限：长按不出菜单', (tester) async {
      await pumpBrowser(tester, modify: false);
      await tester.longPress(find.text('E01.mkv'));
      await tester.pumpAndSettle();
      expect(find.text('删除'), findsNothing);
    });

    testWidgets('有权限：长按 → 删除 → 确认回收站 → 请求并刷新', (tester) async {
      deleteRes = () => http.Response('', 204);
      await pumpBrowser(tester, modify: true);
      final lists = seen.length;
      await deleteFirst(tester);
      final del = seen.singleWhere((r) => r.url.path == '/api/delete');
      expect(jsonDecode(del.body), {
        'share': 'k3',
        'paths': ['E01.mkv'],
      });
      expect(find.text('已移入电脑的回收站'), findsOneWidget);
      expect(seen.length, lists + 2, reason: '删除后重新列目录');
      expect(seen.last.url.path, '/api/list');
    });

    testWidgets('IN_USE 部分失败给中文提示', (tester) async {
      deleteRes = () => jsonRes({
        'failed': [
          {'path': 'E01.mkv', 'code': 'IN_USE', 'message': 'in use'},
        ],
      });
      await pumpBrowser(tester, modify: true);
      await deleteFirst(tester);
      expect(find.textContaining('正在播放或被电脑上的程序占用'), findsOneWidget);
    });

    testWidgets('FORBIDDEN 整体失败给中文提示', (tester) async {
      deleteRes = () => errRes(403, 'FORBIDDEN');
      await pumpBrowser(tester, modify: true);
      await deleteFirst(tester);
      expect(find.textContaining('没有删除'), findsOneWidget);
    });

    test('错误码文案', () {
      expect(deleteErrorText('NOT_FOUND', 'x'), '文件已不存在');
      expect(deleteErrorText('NO_RECYCLE_BIN', 'x'), contains('没有回收站'));
      expect(deleteErrorText('OTHER', '盘没有回收站'), '删除失败：盘没有回收站');
    });
  });
}
