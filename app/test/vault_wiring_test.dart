import 'dart:convert';

import 'package:cached_network_image/cached_network_image.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:lfb_app/api/lfb_client.dart';
import 'package:lfb_app/app.dart' show vaultController;
import 'package:lfb_app/models/server.dart';
import 'package:lfb_app/pages/home_page.dart';
import 'package:lfb_app/theme.dart';
import 'package:lfb_app/widgets/thumb.dart';
import 'package:material_ui/material_ui.dart';

http.Response jsonRes(Object body) => http.Response.bytes(
  utf8.encode(jsonEncode(body)),
  200,
  headers: {'content-type': 'application/json; charset=utf-8'},
);

/// 已解锁片库 v1 的客户端；k3 是普通共享。
LfbClient unlockedClient(List<http.Request> seen) => LfbClient(
  host: 'h',
  port: 1,
  token: 'tok',
  httpClient: MockClient((req) async {
    seen.add(req);
    return http.Response('', 200);
  }),
)..vault = const VaultAccess('sess', {'v1'});

void main() {
  group('媒体 URL 统一经 mediaUrl', () {
    test('加密片库的缩略图、大图、字幕直链带 ?v=，普通共享不带', () {
      final c = unlockedClient([]);
      for (final u in [
        c.thumbUrl('v1', 'a.jpg', 320),
        c.fileUrl('v1', 'a.jpg'),
        c.fileUrl('v1', 'a.srt'),
      ]) {
        expect(u.queryParameters['v'], 'sess', reason: '$u');
        expect(u.queryParameters['t'], 'tok', reason: '$u');
      }
      for (final u in [
        c.thumbUrl('k3', 'a.jpg', 320),
        c.fileUrl('k3', 'a.jpg'),
      ]) {
        expect(u.queryParameters.containsKey('v'), isFalse, reason: '$u');
        expect(u.queryParameters['t'], 'tok', reason: '$u');
      }
      expect(c.thumbUrl('v1', 'a.jpg', 320).queryParameters['w'], '320');
    });

    test('上锁后加密片库直链也不再带 ?v=', () {
      final c = unlockedClient([])..vault = null;
      expect(c.thumbUrl('v1', 'a.jpg', 160).queryParameters, {
        'share': 'v1',
        'path': 'a.jpg',
        'w': '160',
        't': 'tok',
      });
    });

    test('预取下一集在加密片库中带 X-Vault', () async {
      final seen = <http.Request>[];
      await unlockedClient(seen).prefetch('v1', 'E02.mkv', bytes: 16);
      expect(seen.single.headers['X-Vault'], 'sess');
      expect(seen.single.headers['Range'], 'bytes=0-15');
    });
  });

  group('加密片库图片不写磁盘缓存', () {
    final mtime = DateTime.fromMillisecondsSinceEpoch(0);

    test('加密片库用内存 NetworkImage，普通共享用 CachedNetworkImageProvider', () {
      final c = unlockedClient([]);
      final vault = serverImageProvider(
        c,
        'v1',
        c.thumbUrl('v1', 'a.jpg', 160),
        mtime,
      );
      expect(vault, isA<NetworkImage>());
      expect(vault, isNot(isA<CachedNetworkImageProvider>()));
      final normal = serverImageProvider(
        c,
        'k3',
        c.thumbUrl('k3', 'a.jpg', 160),
        mtime,
      );
      expect(normal, isA<CachedNetworkImageProvider>());
    });

    testWidgets('加密片库缩略图不经 CachedNetworkImage', (tester) async {
      await tester.pumpWidget(
        MaterialApp(
          home: ServerThumb(
            client: unlockedClient([]),
            share: 'v1',
            path: 'a.jpg',
            mtime: mtime,
            width: 112,
            placeholder: const SizedBox(),
          ),
        ),
      );
      expect(find.byType(CachedNetworkImage), findsNothing);
      final img = tester.widget<Image>(find.byType(Image));
      expect(img.image, isA<NetworkImage>());
      expect((img.image as NetworkImage).url, contains('v=sess'));
    });
  });

  testWidgets('上锁后首页立即移除加密片库的继续观看条目', (tester) async {
    var failContinue = false;
    final client = LfbClient(
      host: 'h',
      port: 1,
      token: 'tok',
      httpClient: MockClient((req) async {
        switch (req.url.path) {
          case '/api/vault/unlock':
            return jsonRes({
              'session': 'sess',
              'expiresIn': 600,
              'shares': [
                {
                  'id': 'v1',
                  'name': '私密',
                  'perms': {'download': true},
                  'status': 'ok',
                },
              ],
            });
          case '/api/continue':
            if (failContinue) throw http.ClientException('offline');
            return jsonRes({
              'items': [
                if (req.headers['X-Vault'] == 'sess')
                  {
                    'share': 'v1',
                    'path': '私密剧/E05.mkv',
                    'name': 'E05.mkv',
                    'position': 60,
                    'duration': 1200,
                    'watched': false,
                    'deviceName': '手机',
                  },
              ],
            });
        }
        return jsonRes({'items': []});
      }),
    );
    await tester.pumpWidget(
      MaterialApp(
        theme: buildNightTheme(),
        home: HomePage(
          server: const SavedServer(
            serverId: 'srv',
            name: '书房电脑',
            lastIp: '10.0.0.2',
            port: 8686,
          ),
          client: client,
        ),
      ),
    );
    await tester.pumpAndSettle();
    expect(find.text('私密剧'), findsNothing);

    expect(await vaultController.unlock(client, '0000'), isTrue);
    await tester.pumpAndSettle();
    expect(find.text('私密剧'), findsOneWidget);

    // 上锁后即使刷新失败（保留旧列表的路径），也不能再显示片库条目
    failContinue = true;
    vaultController.lock();
    await tester.pumpAndSettle();
    expect(find.text('私密剧'), findsNothing);
  });
}
