import 'dart:convert';
import 'dart:io';

import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:lfb_app/api/api_exception.dart';
import 'package:lfb_app/api/lfb_client.dart';
import 'package:lfb_app/app.dart';
import 'package:lfb_app/models/server.dart';
import 'package:lfb_app/pages/connect_page.dart';
import 'package:lfb_app/pages/startup_page.dart';
import 'package:shared_preferences/shared_preferences.dart';

/// 已保存凭据只在服务端明确 token 无效时清除（01 §3.3、§6）。
void main() {
  const server = {
    'serverId': 's1',
    'name': '书房电脑',
    'lastIp': '10.0.0.1',
    'port': 8686,
  };

  setUp(() {
    SharedPreferences.setMockInitialValues({
      'servers': jsonEncode([server]),
      'lastServerId': 's1',
    });
    FlutterSecureStorage.setMockInitialValues({'token_s1': 'tok'});
  });

  http.Response ping(String id, {bool needPair = false}) => http.Response(
    jsonEncode({
      'id': id,
      'name': 'x',
      'version': '0.1.0',
      'needPair': needPair,
    }),
    200,
  );

  Future<void> start(WidgetTester tester, MockClient mock) async {
    await http.runWithClient(
      () => tester.pumpWidget(const LfbApp()),
      () => mock,
    );
    for (var i = 0; i < 5; i++) {
      await tester.runAsync(() => Future<void>.delayed(Duration.zero));
      await tester.pump();
    }
  }

  testWidgets('启动时网络不可达：保留凭据、不回连接页、显示重试与切换入口', (tester) async {
    await start(
      tester,
      MockClient((_) async => throw const SocketException('unreachable')),
    );
    expect(find.byType(ConnectPage), findsNothing);
    expect(find.textContaining('书房电脑'), findsWidgets);
    expect(find.text('重试'), findsOneWidget);
    expect(find.text('切换或添加电脑'), findsOneWidget);
    expect(await serverStore.lastServer(), isNotNull);
    await tester.pumpWidget(const SizedBox()); // 释放自动重试计时器
  });

  testWidgets('启动时服务端 5xx：同样保留凭据', (tester) async {
    await start(tester, MockClient((_) async => http.Response('oops', 500)));
    expect(find.byType(ConnectPage), findsNothing);
    expect(await serverStore.lastServer(), isNotNull);
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('启动时服务端表示 token 无效：清凭据并提示已被移除', (tester) async {
    await start(tester, MockClient((_) async => ping('s1', needPair: true)));
    expect(find.byType(ConnectPage), findsOneWidget);
    expect(find.textContaining('已将本机移除，请重新配对'), findsOneWidget);
    expect(await serverStore.lastServer(), isNull);
  });

  group('LfbClient.reconnect', () {
    test('上次地址超时、二维码里的其他地址可用：换到该地址并回调', () async {
      String? changed;
      var unauthorized = false;
      final client = LfbClient(
        host: '10.0.0.1',
        port: 8686,
        token: 'tok',
        serverId: 's1',
        fallbackHosts: const ['10.0.0.2', '10.0.0.3'],
        onHostChanged: (h) => changed = h,
        onUnauthorized: () => unauthorized = true,
        httpClient: MockClient((req) async {
          if (req.url.host == '10.0.0.1') {
            await Future<void>.delayed(const Duration(seconds: 5));
          }
          if (req.url.host == '10.0.0.2') return ping('other'); // 别的电脑
          return ping('s1');
        }),
      );
      await client.reconnect(timeout: const Duration(milliseconds: 100));
      expect(client.host, '10.0.0.3');
      expect(changed, '10.0.0.3');
      expect(unauthorized, isFalse);
    });

    test('全部超时 / 不可达 / 5xx：抛 NetworkException，不触发 onUnauthorized', () async {
      var unauthorized = false;
      final client = LfbClient(
        host: '10.0.0.1',
        port: 8686,
        token: 'tok',
        serverId: 's1',
        fallbackHosts: const ['10.0.0.2', '10.0.0.3'],
        onUnauthorized: () => unauthorized = true,
        httpClient: MockClient((req) async {
          switch (req.url.host) {
            case '10.0.0.1':
              await Future<void>.delayed(const Duration(seconds: 5));
              return ping('s1');
            case '10.0.0.2':
              throw const SocketException('unreachable');
            default:
              return http.Response('oops', 503);
          }
        }),
      );
      await expectLater(
        client.reconnect(timeout: const Duration(milliseconds: 100)),
        throwsA(isA<NetworkException>()),
      );
      expect(unauthorized, isFalse);
      expect(client.host, '10.0.0.1');
    });

    test('同一台电脑表示 token 无效：触发 onUnauthorized 并抛 401', () async {
      var unauthorized = 0;
      final client = LfbClient(
        host: '10.0.0.1',
        port: 8686,
        token: 'tok',
        serverId: 's1',
        fallbackHosts: const ['10.0.0.2'],
        onUnauthorized: () => unauthorized++,
        httpClient: MockClient(
          (req) async => req.url.host == '10.0.0.1'
              ? ping('other', needPair: true) // 别的电脑说 needPair 不算
              : ping('s1', needPair: true),
        ),
      );
      await expectLater(
        client.reconnect(),
        throwsA(isA<ApiException>().having((e) => e.statusCode, 'status', 401)),
      );
      expect(unauthorized, 1);
    });

    test('只有别的电脑说 needPair：不算授权失效', () async {
      var unauthorized = false;
      final client = LfbClient(
        host: '10.0.0.1',
        port: 8686,
        token: 'tok',
        serverId: 's1',
        onUnauthorized: () => unauthorized = true,
        httpClient: MockClient((_) async => ping('other', needPair: true)),
      );
      await expectLater(client.reconnect(), throwsA(isA<NetworkException>()));
      expect(unauthorized, isFalse);
    });
  });

  group('ServerStore 多地址', () {
    test('旧数据无 ips 字段可读；候选以 lastIp 开头', () async {
      final (s, token) = (await serverStore.lastServer())!;
      expect(token, 'tok');
      expect(s.candidates, ['10.0.0.1']);
    });

    test('保存时合并已知地址，切换电脑不删除其他已保存的', () async {
      await serverStore.save(
        const SavedServer(
          serverId: 's1',
          name: '书房电脑',
          lastIp: '10.0.0.1',
          port: 8686,
          ips: ['10.0.0.1', '192.168.1.5'],
        ),
      );
      await serverStore.save(
        const SavedServer(
          serverId: 's2',
          name: '客厅电脑',
          lastIp: '10.0.0.9',
          port: 8686,
        ),
        token: 'tok2',
      );
      await serverStore.setLast('s1');
      final (s, _) = (await serverStore.lastServer())!;
      expect(s.candidates, ['10.0.0.1', '192.168.1.5']);
      await serverStore.save(s.withAddress('192.168.1.7'));
      final (moved, _) = (await serverStore.lastServer())!;
      expect(moved.candidates, ['192.168.1.7', '10.0.0.1', '192.168.1.5']);
      expect((await serverStore.paired()).length, 2);
    });
  });

  group('修改地址 moveServer', () {
    const saved = SavedServer(
      serverId: 's1',
      name: '书房电脑',
      lastIp: '10.0.0.1',
      port: 8686,
    );

    test('新地址 serverId 一致：用旧 token 校验通过后保存新地址', () async {
      late http.Request seen;
      final updated = await http.runWithClient(
        () => moveServer(saved, 'tok', '192.168.1.7', 9000),
        () => MockClient((req) async {
          seen = req;
          return ping('s1');
        }),
      );
      expect(seen.url.host, '192.168.1.7');
      expect(seen.headers['Authorization'], 'Bearer tok');
      expect(updated.lastIp, '192.168.1.7');
      final (s, token) = (await serverStore.lastServer())!;
      expect((s.lastIp, s.port, token), ('192.168.1.7', 9000, 'tok'));
      expect(s.candidates, contains('10.0.0.1'));
    });

    test('新地址是另一台电脑：拒绝且不保存', () async {
      await expectLater(
        http.runWithClient(
          () => moveServer(saved, 'tok', '192.168.1.7', 8686),
          () => MockClient((_) async => ping('other')),
        ),
        throwsA(isA<NetworkException>()),
      );
      final (s, token) = (await serverStore.lastServer())!;
      expect((s.lastIp, token), ('10.0.0.1', 'tok'));
    });
  });
}
