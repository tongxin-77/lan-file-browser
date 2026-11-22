import 'dart:convert';
import 'dart:io';

import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:lfb_app/app.dart';
import 'package:lfb_app/pages/connect_page.dart';
import 'package:lfb_app/pages/startup_page.dart';
import 'package:lfb_app/services/discovery.dart';
import 'package:material_ui/material_ui.dart';
import 'package:shared_preferences/shared_preferences.dart';

/// 连接另一台电脑失败：已保存的电脑、凭据与当前选中的电脑都不变，
/// 且可一键回到原来的电脑（01 §2、§6）。
void main() {
  const a = {
    'serverId': 's1',
    'name': '书房电脑',
    'lastIp': '10.0.0.1',
    'port': 8686,
    'ips': <String>[],
  };
  const b = {
    'serverId': 's2',
    'name': '客厅电脑',
    'lastIp': '10.0.0.2',
    'port': 8686,
    'ips': <String>[],
  };

  late List<DiscoveryReply> lan;

  setUp(() {
    SharedPreferences.setMockInitialValues({
      'servers': jsonEncode([a, b]),
      'lastServerId': 's1',
    });
    FlutterSecureStorage.setMockInitialValues({
      'token_s1': 'tok1',
      'token_s2': 'tok2',
    });
    lan = [];
    lanDiscovery.probe = (_, _) async => lan;
  });

  http.Response json(Object body, [int status = 200]) => http.Response.bytes(
    utf8.encode(jsonEncode(body)),
    status,
    headers: {'content-type': 'application/json; charset=utf-8'},
  );

  Future<void> settle(WidgetTester tester) async {
    for (var i = 0; i < 8; i++) {
      await tester.runAsync(
        () => Future<void>.delayed(const Duration(milliseconds: 10)),
      );
      await tester.pump();
    }
  }

  Future<void> openConnect(WidgetTester tester, MockClient mock) async {
    tester.view.physicalSize = const Size(1080, 4000); // 错误提示不被滚出视口
    addTearDown(tester.view.reset);
    await http.runWithClient(() async {
      await tester.pumpWidget(
        MaterialApp(navigatorKey: navigatorKey, home: const ConnectPage()),
      );
      await settle(tester);
    }, () => mock);
  }

  Future<void> tap(WidgetTester tester, MockClient mock, Finder f) async {
    // 新页面在 pump 时创建客户端，需一并放在 mock 的 zone 里
    await http.runWithClient(() async {
      await tester.tap(f);
      await settle(tester);
    }, () => mock);
  }

  /// 已保存的电脑、token、当前选中的电脑都与初始一致。
  Future<void> expectUnchanged() async {
    final prefs = await SharedPreferences.getInstance();
    expect(prefs.getString('lastServerId'), 's1');
    expect(jsonDecode(prefs.getString('servers')!), [a, b]);
    final paired = await serverStore.paired();
    expect(
      [for (final (s, t) in paired) (s.serverId, t)],
      [('s1', 'tok1'), ('s2', 'tok2')],
    );
  }

  Future<void> pairManually(WidgetTester tester, MockClient mock) async {
    await tester.enterText(find.byKey(const Key('addr')), '10.0.0.9');
    await tester.enterText(find.byKey(const Key('pin')), '123456');
    await tap(tester, mock, find.text('配对'));
  }

  testWidgets('手动配对 PIN 错误（401）：不清任何凭据，不提示被移除', (tester) async {
    final mock = MockClient((req) async {
      if (req.url.path == '/api/pair') {
        return json({
          'error': {'code': 'UNAUTHORIZED', 'message': 'bad code'},
        }, 401);
      }
      return json({'id': 's9', 'name': '新电脑', 'needPair': true});
    });
    await openConnect(tester, mock);
    await pairManually(tester, mock);
    expect(find.textContaining('配对码错误'), findsOneWidget);
    expect(find.textContaining('已将本机移除'), findsNothing);
    await expectUnchanged();
  });

  testWidgets('手动配对网络不通：已保存的电脑不变，仍可一键回到原来的电脑', (tester) async {
    final mock = MockClient(
      (_) async => throw const SocketException('unreachable'),
    );
    await openConnect(tester, mock);
    await pairManually(tester, mock);
    await expectUnchanged();
    expect(find.text('书房电脑'), findsOneWidget);
    await tap(tester, mock, find.text('书房电脑'));
    expect(find.byType(StartupPage), findsOneWidget);
    expect(find.textContaining('正在连接「书房电脑」'), findsOneWidget);
    await expectUnchanged();
    await tester.pumpWidget(const SizedBox()); // 释放自动重试计时器
  });

  testWidgets('点发现列表中已配对的另一台电脑但连不上：不改其地址，也不改当前选中的电脑', (tester) async {
    lan = [
      ('192.168.1.20', utf8.encode('{"id":"s2","name":"客厅电脑","port":8686}')),
    ];
    final mock = MockClient(
      (_) async => throw const SocketException('unreachable'),
    );
    await openConnect(tester, mock);
    await tap(tester, mock, find.textContaining('192.168.1.20'));
    expect(find.textContaining('正在连接「客厅电脑」'), findsOneWidget);
    // 发现到的新地址与原地址都尝试过，但都没有写入
    expect(find.textContaining('192.168.1.20、10.0.0.2'), findsOneWidget);
    await expectUnchanged();
    await tester.pumpWidget(const SizedBox());
  });
}
