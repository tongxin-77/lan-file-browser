import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:lfb_app/api/lfb_client.dart';

http.Response jsonRes(Object body) => http.Response.bytes(
  utf8.encode(jsonEncode(body)),
  200,
  headers: {'content-type': 'application/json; charset=utf-8'},
);

void main() {
  late List<http.Request> seen;
  late http.Response Function(http.Request) reply;
  late LfbClient client;

  setUp(() {
    seen = [];
    client = LfbClient(
      host: 'h',
      port: 1,
      token: 'tok',
      httpClient: MockClient((req) async {
        seen.add(req);
        return reply(req);
      }),
    );
  });

  test('next 解析 item 与 null', () async {
    reply = (_) => jsonRes({
      'item': {
        'name': 'E13.mkv',
        'kind': 'video',
        'size': 1,
        'mtime': 0,
        'hasThumb': false,
        'progress': null,
        'path': 'S01/E13.mkv',
      },
    });
    final n = await client.next('k3', 'S01/E12.mkv');
    expect(seen.last.url.path, '/api/next');
    expect(seen.last.url.queryParameters, {
      'share': 'k3',
      'path': 'S01/E12.mkv',
    });
    expect(n!.path, 'S01/E13.mkv');
    expect(n.item.name, 'E13.mkv');

    reply = (_) => jsonRes({'item': null});
    expect(await client.next('k3', 'S01/E13.mkv'), isNull);
  });

  test('subtitles 解析', () async {
    reply = (_) => jsonRes({
      'items': [
        {
          'name': 'E12.chs.srt',
          'path': 'S01/E12.chs.srt',
          'format': 'srt',
          'lang': 'chs',
          'match': 'exact',
        },
      ],
    });
    final subs = await client.subtitles('k3', 'S01/E12.mkv');
    expect(seen.last.url.path, '/api/subtitles');
    expect(subs.single.lang, 'chs');
    expect(subs.single.match, 'exact');
  });

  test('reportProgress 以秒 POST JSON', () async {
    reply = (_) => http.Response('', 204);
    await client.reportProgress(
      'k3',
      'S01/E12.mkv',
      position: const Duration(milliseconds: 1008500),
      duration: const Duration(seconds: 1440),
    );
    final req = seen.last;
    expect(req.method, 'POST');
    expect(req.url.path, '/api/progress');
    expect(req.headers['Authorization'], 'Bearer tok');
    expect(jsonDecode(req.body), {
      'share': 'k3',
      'path': 'S01/E12.mkv',
      'position': 1008.5,
      'duration': 1440,
    });
  });

  test('continueWatching 解析列表', () async {
    reply = (_) => jsonRes({
      'items': [
        {
          'share': 'k3',
          'path': 'a.mkv',
          'name': 'a.mkv',
          'position': 10,
          'duration': 100,
          'watched': false,
          'updatedAt': '2025-01-01T00:00:00Z',
          'deviceName': 'TV',
        },
      ],
    });
    final items = await client.continueWatching();
    expect(seen.last.url.path, '/api/continue');
    expect(items.single.deviceName, 'TV');
  });

  test('thumbUrl 带 w 与 t', () {
    final u = client.thumbUrl('k3', 'a/b.jpg', 320);
    expect(u.path, '/api/thumb');
    expect(u.queryParameters, {
      'share': 'k3',
      'path': 'a/b.jpg',
      'w': '320',
      't': 'tok',
    });
  });

  test('prefetch 只取开头 8 MB，失败静默', () async {
    reply = (_) => http.Response('x', 206);
    await client.prefetch('k3', 'S01/E13.mkv');
    expect(seen.last.headers['Range'], 'bytes=0-8388607');
    expect(seen.last.headers['Authorization'], 'Bearer tok');
    expect(seen.last.url.queryParameters.containsKey('t'), isFalse);

    reply = (_) => throw http.ClientException('down');
    await client.prefetch('k3', 'S01/E13.mkv');
  });

  test('download 带 Bearer 取字节', () async {
    reply = (_) => http.Response.bytes([1, 2, 3], 200);
    final b = await client.download('k3', 'p/a.jpg');
    expect(seen.last.url.path, '/api/download');
    expect(b, [1, 2, 3]);
  });
}
