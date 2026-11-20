import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:lfb_app/api/api_exception.dart';
import 'package:lfb_app/api/lfb_client.dart';
import 'package:lfb_app/api/probe.dart';
import 'package:lfb_app/models/file_item.dart';

http.Response jsonRes(
  Object body, [
  int status = 200,
  Map<String, String>? h,
]) => http.Response.bytes(
  utf8.encode(jsonEncode(body)),
  status,
  headers: {'content-type': 'application/json; charset=utf-8', ...?h},
);

void main() {
  group('鉴权与请求', () {
    test('带 Bearer 头并解析 shares', () async {
      late http.Request seen;
      final client = LfbClient(
        host: '192.168.1.5',
        port: 8686,
        token: 'tok',
        httpClient: MockClient((req) async {
          seen = req;
          return jsonRes({
            'items': [
              {
                'id': 'k3f9q2ma',
                'name': '动漫',
                'perms': {
                  'download': true,
                  'upload': false,
                  'modify': false,
                  'showHidden': true,
                },
                'status': 'ok',
              },
              {'id': 'b', 'name': '移动硬盘', 'perms': {}, 'status': 'offline'},
            ],
          });
        }),
      );
      final shares = await client.shares();
      expect(seen.url.toString(), 'http://192.168.1.5:8686/api/shares');
      expect(seen.headers['Authorization'], 'Bearer tok');
      expect(shares.map((s) => s.name), ['动漫', '移动硬盘']);
      expect(shares[0].online, isTrue);
      expect(shares[0].perms.download, isTrue);
      expect(shares[0].perms.showHidden, isTrue);
      expect(shares[1].online, isFalse);
    });

    test('list 解析条目、分页参数与 ETag', () async {
      late http.Request seen;
      final client = LfbClient(
        host: 'h',
        port: 1,
        token: 't',
        httpClient: MockClient((req) async {
          seen = req;
          return jsonRes(
            {
              'items': [
                {
                  'name': 'S01',
                  'kind': 'dir',
                  'size': 0,
                  'mtime': 1700000000000,
                  'hidden': false,
                  'hasThumb': false,
                  'progress': null,
                },
                {
                  'name': 'E12.mkv',
                  'kind': 'video',
                  'size': 1536,
                  'mtime': 1700000000000,
                  'hidden': true,
                  'hasThumb': true,
                  'progress': null,
                },
                {'name': 'x.bin', 'kind': 'weird', 'size': 1, 'mtime': 0},
              ],
              'total': 503,
            },
            200,
            {'etag': '"abc"'},
          );
        }),
      );
      final page = (await client.list('k3', '进击/S01', offset: 200))!;
      expect(seen.url.path, '/api/list');
      expect(seen.url.queryParameters, {
        'share': 'k3',
        'path': '进击/S01',
        'offset': '200',
        'limit': '200',
      });
      expect(seen.headers.containsKey('If-None-Match'), isFalse);
      expect(page.total, 503);
      expect(page.etag, '"abc"');
      expect(page.items.map((i) => i.kind), [
        FileKind.dir,
        FileKind.video,
        FileKind.other,
      ]);
      expect(page.items[1].size, 1536);
      expect(page.items[1].hidden, isTrue);
      expect(page.items[1].mtime.millisecondsSinceEpoch, 1700000000000);
    });

    test('If-None-Match 命中 304 返回 null', () async {
      final client = LfbClient(
        host: 'h',
        port: 1,
        token: 't',
        httpClient: MockClient((req) async {
          expect(req.headers['If-None-Match'], '"abc"');
          return http.Response('', 304);
        }),
      );
      expect(await client.list('s', '', etag: '"abc"'), isNull);
    });

    test('fileUrl 带 ?t= 且正确编码', () {
      final client = LfbClient(host: '10.0.0.3', port: 8686, token: 'a+b/c');
      final u = client.fileUrl('k3', '动漫/第 1 集.mkv');
      expect(u.path, '/api/file');
      expect(u.queryParameters, {
        'share': 'k3',
        'path': '动漫/第 1 集.mkv',
        't': 'a+b/c',
      });
    });

    test('pair 发送 JSON 并解析 token', () async {
      final client = LfbClient(
        host: 'h',
        port: 1,
        httpClient: MockClient((req) async {
          expect(req.method, 'POST');
          expect(req.headers.containsKey('Authorization'), isFalse);
          expect(jsonDecode(req.body), {
            'code': '123456',
            'deviceName': 'Pixel',
            'deviceId': 'dev-1',
          });
          return jsonRes({'token': 'T', 'serverId': 'S', 'name': 'PC'});
        }),
      );
      final r = await client.pair(
        code: '123456',
        deviceName: 'Pixel',
        deviceId: 'dev-1',
      );
      expect((r.token, r.serverId, r.name), ('T', 'S', 'PC'));
    });
  });

  group('错误处理', () {
    LfbClient failing(
      http.Response res, {
      String? token = 't',
      void Function()? on401,
    }) => LfbClient(
      host: 'h',
      port: 1,
      token: token,
      onUnauthorized: on401,
      httpClient: MockClient((_) async => res),
    );

    test('统一错误 JSON → ApiException', () async {
      final c = failing(
        jsonRes({
          'error': {'code': 'SHARE_OFFLINE', 'message': '共享不可用'},
        }, 503),
      );
      await expectLater(
        c.list('s', ''),
        throwsA(
          isA<ApiException>()
              .having((e) => e.code, 'code', 'SHARE_OFFLINE')
              .having((e) => e.statusCode, 'status', 503)
              .having((e) => e.message, 'message', '共享不可用'),
        ),
      );
    });

    test('非 JSON 错误体 → HTTP_<状态码>', () async {
      final c = failing(http.Response('Range Not Satisfiable', 416));
      await expectLater(
        c.shares(),
        throwsA(isA<ApiException>().having((e) => e.code, 'code', 'HTTP_416')),
      );
    });

    test('带 token 的 401 触发 onUnauthorized', () async {
      var called = 0;
      final c = failing(
        jsonRes({
          'error': {'code': 'UNAUTHORIZED', 'message': 'x'},
        }, 401),
        on401: () => called++,
      );
      await expectLater(
        c.shares(),
        throwsA(
          isA<ApiException>().having((e) => e.isUnauthorized, '401', true),
        ),
      );
      expect(called, 1);
    });

    test('配对（无 token）的 401 不触发 onUnauthorized', () async {
      var called = 0;
      final c = failing(
        jsonRes({
          'error': {'code': 'UNAUTHORIZED', 'message': '配对码错误或已过期'},
        }, 401),
        token: null,
        on401: () => called++,
      );
      await expectLater(
        c.pair(code: '000000', deviceName: 'd', deviceId: 'i'),
        throwsA(isA<ApiException>()),
      );
      expect(called, 0);
    });

    test('网络错误 → NetworkException', () async {
      final c = LfbClient(
        host: 'h',
        port: 1,
        httpClient: MockClient(
          (_) async => throw const SocketException('unreachable'),
        ),
      );
      await expectLater(c.ping(), throwsA(isA<NetworkException>()));
    });

    test('超时 → NetworkException', () async {
      final c = LfbClient(
        host: 'h',
        port: 1,
        httpClient: MockClient((_) async {
          await Future<void>.delayed(const Duration(milliseconds: 200));
          return jsonRes({});
        }),
      );
      await expectLater(
        c.ping(timeout: const Duration(milliseconds: 20)),
        throwsA(isA<NetworkException>()),
      );
    });

    test('verify：id 不符视为其他电脑，needPair 触发重新配对', () async {
      LfbClient pinging(Map<String, Object> body, void Function() on401) =>
          LfbClient(
            host: 'h',
            port: 1,
            token: 't',
            serverId: 'S',
            onUnauthorized: on401,
            httpClient: MockClient((_) async => jsonRes(body)),
          );
      var called = 0;
      await expectLater(
        pinging({
          'id': 'OTHER',
          'name': 'x',
          'version': '1',
          'needPair': false,
        }, () => called++).verify(),
        throwsA(isA<NetworkException>()),
      );
      await expectLater(
        pinging({
          'id': 'S',
          'name': 'x',
          'version': '1',
          'needPair': true,
        }, () => called++).verify(),
        throwsA(isA<ApiException>()),
      );
      expect(called, 1);
      final ok = await pinging({
        'id': 'S',
        'name': 'PC',
        'version': '1',
        'needPair': false,
      }, () => called++).verify();
      expect(ok.name, 'PC');
    });
  });

  group('probeFirst', () {
    test('返回第一个成功且 id 匹配的地址', () async {
      final mock = MockClient((req) async {
        switch (req.url.host) {
          case '10.0.0.1': // 不通
            throw const SocketException('down');
          case '10.0.0.2': // 其他电脑
            return jsonRes({'id': 'OTHER', 'needPair': true});
          case '10.0.0.3': // 慢但正确
            await Future<void>.delayed(const Duration(milliseconds: 50));
            return jsonRes({'id': 'S', 'needPair': true});
        }
        return jsonRes({'id': 'S', 'needPair': true});
      });
      expect(
        await probeFirst(
          ['10.0.0.1', '10.0.0.2', '10.0.0.3'],
          8686,
          'S',
          httpClient: mock,
        ),
        '10.0.0.3',
      );
    });

    test('全部失败返回 null', () async {
      final mock = MockClient((_) async => throw const SocketException('x'));
      expect(await probeFirst(['a', 'b'], 1, 'S', httpClient: mock), isNull);
      expect(await probeFirst([], 1, 'S', httpClient: mock), isNull);
    });
  });
}
