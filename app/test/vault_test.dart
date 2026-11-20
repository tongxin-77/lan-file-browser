import 'dart:convert';

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:lfb_app/api/api_exception.dart';
import 'package:lfb_app/api/lfb_client.dart';
import 'package:lfb_app/services/vault.dart';

http.Response jsonRes(Object body, [int status = 200]) => http.Response.bytes(
  utf8.encode(jsonEncode(body)),
  status,
  headers: {'content-type': 'application/json; charset=utf-8'},
);

Map<String, Object?> shareJson(String id, String name) => {
  'id': id,
  'name': name,
  'perms': {'download': true},
  'status': 'ok',
};

/// 模拟服务端：密码 0000 解锁 v1；记录所有请求。
class FakeServer {
  final requests = <http.Request>[];

  late final client = LfbClient(
    host: 'h',
    port: 1,
    token: 'tok',
    httpClient: MockClient((req) async {
      requests.add(req);
      switch (req.url.path) {
        case '/api/vault/unlock':
          final pw = (jsonDecode(req.body) as Map)['password'];
          return pw == '0000'
              ? jsonRes({
                  'session': 'sess',
                  'expiresIn': 600,
                  'shares': [shareJson('v1', '工作资料')],
                })
              : jsonRes({'session': null, 'expiresIn': 600, 'shares': []});
        case '/api/vault/lock':
          return jsonRes({});
        case '/api/list':
          return jsonRes({
            'error': {'code': 'NOT_FOUND', 'message': '不存在'},
          }, 404);
      }
      return jsonRes({'items': []});
    }),
  );

  Iterable<http.Request> at(String path) =>
      requests.where((r) => r.url.path == path);
}

void main() {
  group('LfbClient 会话注入', () {
    test('解锁后所有请求附 X-Vault，上锁后不再附', () async {
      final s = FakeServer();
      s.client.vault = const VaultAccess('sess', {'v1'});
      await s.client.shares();
      expect(s.requests.last.headers['X-Vault'], 'sess');
      s.client.vault = null;
      await s.client.shares();
      expect(s.requests.last.headers.containsKey('X-Vault'), isFalse);
    });

    test('?v= 只加在 file/download/thumb 直链上', () {
      final c = LfbClient(host: 'h', port: 1, token: 't')
        ..vault = const VaultAccess('sess', {'v1'});
      for (final p in ['/api/file', '/api/download', '/api/thumb']) {
        final u = c.mediaUrl(p, {'share': 'v1', 'path': 'a.mkv'});
        expect(u.queryParameters['v'], 'sess', reason: p);
        expect(u.queryParameters['t'], 't', reason: p);
      }
      for (final p in ['/api/list', '/api/app/apk/arm64-v8a', '/api/next']) {
        final u = c.mediaUrl(p, {'share': 'v1'});
        expect(u.queryParameters.containsKey('v'), isFalse, reason: p);
      }
      expect(c.fileUrl('v1', 'a.mkv').queryParameters['v'], 'sess');
      c.vault = null;
      expect(c.fileUrl('v1', 'a.mkv').queryParameters.containsKey('v'), false);
    });

    test('isVaultShare 只对已解锁的片库为真', () {
      final c = LfbClient(host: 'h', port: 1, token: 't');
      expect(c.isVaultShare('v1'), isFalse);
      c.vault = const VaultAccess('sess', {'v1'});
      expect(c.isVaultShare('v1'), isTrue);
      expect(c.isVaultShare('k3'), isFalse);
    });

    test('片库内请求 404 视为会话失效并回调', () async {
      final s = FakeServer();
      var lost = 0;
      s.client
        ..onVaultLost = (() => lost++)
        ..vault = const VaultAccess('sess', {'v1'});
      await expectLater(s.client.list('k3', ''), throwsA(isA<ApiException>()));
      expect(lost, 0, reason: '普通共享的 404 不算');
      await expectLater(s.client.list('v1', ''), throwsA(isA<ApiException>()));
      expect(lost, 1);
    });
  });

  group('VaultController', () {
    late FakeServer s;
    late DateTime now;
    late List<bool> secure;
    late int lockedCalls;
    late VaultController vault;

    setUp(() {
      s = FakeServer();
      now = DateTime(2026, 11, 20, 22);
      secure = [];
      lockedCalls = 0;
      vault = VaultController(
        now: () => now,
        setSecure: (on) async => secure.add(on),
        tick: null,
      )..onLocked = () => lockedCalls++;
    });

    test('错误密码：不解锁、不置防截屏', () async {
      expect(await vault.unlock(s.client, '1234'), isFalse);
      expect(vault.unlocked, isFalse);
      expect(s.client.vault, isNull);
      expect(secure, isEmpty);
    });

    test('正确密码：保存会话与片库、置防截屏；lock 通知服务端并清空', () async {
      expect(await vault.unlock(s.client, '0000'), isTrue);
      expect(vault.unlocked, isTrue);
      expect(vault.shares.single.name, '工作资料');
      expect(s.client.vault?.session, 'sess');
      expect(s.client.isVaultShare('v1'), isTrue);
      expect(secure, [true]);

      vault.lock();
      await Future<void>.delayed(Duration.zero);
      expect(vault.unlocked, isFalse);
      expect(vault.shares, isEmpty);
      expect(s.client.vault, isNull);
      expect(secure, [true, false]);
      expect(lockedCalls, 1);
      expect(s.at('/api/vault/lock').single.headers['X-Vault'], 'sess');

      vault.lock(); // 重复上锁无副作用
      expect(lockedCalls, 1);
    });

    test('空闲 10 分钟自动上锁；有操作则重新计时', () async {
      await vault.unlock(s.client, '0000');
      now = now.add(const Duration(minutes: 9));
      vault.checkIdle();
      expect(vault.unlocked, isTrue);

      vault.touch();
      now = now.add(const Duration(minutes: 9, seconds: 59));
      vault.checkIdle();
      expect(vault.unlocked, isTrue);

      now = now.add(const Duration(seconds: 1));
      vault.checkIdle();
      expect(vault.unlocked, isFalse);
      expect(lockedCalls, 1);
    });

    test('切后台（paused / hidden）立即上锁；inactive 不上锁', () async {
      for (final state in [
        AppLifecycleState.paused,
        AppLifecycleState.hidden,
      ]) {
        await vault.unlock(s.client, '0000');
        vault.didChangeAppLifecycleState(AppLifecycleState.inactive);
        expect(vault.unlocked, isTrue);
        vault.didChangeAppLifecycleState(state);
        expect(vault.unlocked, isFalse, reason: '$state');
      }
      expect(lockedCalls, 2);
    });

    test('片库内 404 → 上锁', () async {
      await vault.unlock(s.client, '0000');
      await expectLater(s.client.list('v1', ''), throwsA(isA<ApiException>()));
      expect(vault.unlocked, isFalse);
      expect(lockedCalls, 1);
    });
  });

  testWidgets('系统生命周期事件经 WidgetsBinding 触发上锁', (tester) async {
    final s = FakeServer();
    final vault = VaultController(setSecure: (_) async {}, tick: null);
    WidgetsBinding.instance.addObserver(vault);
    addTearDown(() => WidgetsBinding.instance.removeObserver(vault));
    await tester.runAsync(() => vault.unlock(s.client, '0000'));
    expect(vault.unlocked, isTrue);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
    expect(vault.unlocked, isFalse);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
  });
}
