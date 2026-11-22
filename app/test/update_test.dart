import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:crypto/crypto.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:lfb_app/services/app_update.dart';
import 'package:lfb_app/services/semver.dart';

void main() {
  group('语义化版本比较', () {
    test('数字逐段比较，不按字符串', () {
      expect(compareVersions('1.10.0', '1.9.0'), 1);
      expect(compareVersions('1.2.3', '1.2.3'), 0);
      expect(compareVersions('0.9.9', '1.0.0'), -1);
      expect(compareVersions('2.0.0', '10.0.0'), -1);
    });

    test('v 前缀与 +build 后缀忽略', () {
      expect(compareVersions('v1.2.0', '1.2.0'), 0);
      expect(compareVersions('1.2.0+7', '1.2.0+1'), 0);
    });

    test('预发布版本低于正式版，按标识逐段比较', () {
      expect(compareVersions('1.0.0-rc.1', '1.0.0'), -1);
      expect(compareVersions('1.0.0-alpha', '1.0.0-beta'), -1);
      expect(compareVersions('1.0.0-rc.2', '1.0.0-rc.10'), -1);
      expect(compareVersions('1.0.0-rc.1', '1.0.0-rc.1.1'), -1);
      expect(compareVersions('1.0.0-1', '1.0.0-alpha'), -1);
    });

    test('无效版本返回 null', () {
      expect(compareVersions('dev', '1.0.0'), isNull);
      expect(compareVersions('1.0', '1.0.0'), isNull);
      expect(compareVersions('1.0.0', ''), isNull);
      expect(compareVersions('1.x.0', '1.0.0'), isNull);
    });
  });

  group('ABI 选择', () {
    const apks = {
      'arm64-v8a': 'http://h/api/app/apk/arm64-v8a',
      'armeabi-v7a': 'http://h/api/app/apk/armeabi-v7a',
      'x86_64': 'http://h/api/app/apk/x86_64',
    };

    test('设备支持 arm64-v8a 时优先，即便它不是第一个', () {
      expect(chooseAbi(['armeabi-v7a', 'arm64-v8a'], apks), 'arm64-v8a');
    });

    test('否则按设备支持顺序取第一个有安装包的', () {
      expect(chooseAbi(['armeabi-v7a', 'armeabi'], apks), 'armeabi-v7a');
      expect(chooseAbi(['x86', 'x86_64'], apks), 'x86_64');
    });

    test('设备支持 arm64 但服务端没有该包时退回其他 ABI', () {
      expect(
        chooseAbi(['arm64-v8a', 'armeabi-v7a'], {'armeabi-v7a': 'u'}),
        'armeabi-v7a',
      );
    });

    test('无可用安装包返回 null', () {
      expect(chooseAbi(['mips'], apks), isNull);
      expect(chooseAbi(['arm64-v8a'], const {}), isNull);
    });
  });

  group('更新决策', () {
    test('无新版本', () {
      expect(
        decideUpdate(current: '1.2.0', latest: '1.2.0', minAppVersion: '0.0.0'),
        UpdateDecision.none,
      );
      expect(
        decideUpdate(current: '1.2.0', latest: '1.1.0', minAppVersion: null),
        UpdateDecision.none,
      );
      expect(
        decideUpdate(current: '1.2.0', latest: null, minAppVersion: '1.0.0'),
        UpdateDecision.none,
      );
    });

    test('有新版本：可选', () {
      expect(
        decideUpdate(current: '1.2.0', latest: '1.3.0', minAppVersion: '1.0.0'),
        UpdateDecision.optional,
      );
      expect(
        decideUpdate(current: '1.2.0', latest: 'v1.2.1', minAppVersion: null),
        UpdateDecision.optional,
      );
    });

    test('低于 minAppVersion：强制', () {
      expect(
        decideUpdate(current: '1.0.0', latest: '1.3.0', minAppVersion: '1.1.0'),
        UpdateDecision.forced,
      );
      // 服务端尚无版本信息也要强提示（不能下载，但不可跳过）
      expect(
        decideUpdate(current: '1.0.0', latest: null, minAppVersion: '1.1.0'),
        UpdateDecision.forced,
      );
    });

    test('版本号无效时不打扰', () {
      expect(
        decideUpdate(current: '1.0.0', latest: 'dev', minAppVersion: 'x'),
        UpdateDecision.none,
      );
      expect(
        decideUpdate(current: '', latest: '9.0.0', minAppVersion: '9.0.0'),
        UpdateDecision.none,
      );
    });
  });

  group('电脑端版本兼容', () {
    test('低于 minServerVersion 提示升级电脑端', () {
      expect(needsServerUpgrade('1.0.0', minServer: '1.2.0'), isTrue);
      expect(needsServerUpgrade('1.2.0', minServer: '1.2.0'), isFalse);
      expect(needsServerUpgrade('2.0.0', minServer: '1.2.0'), isFalse);
    });

    test('dev 开发版与无法识别的版本跳过', () {
      expect(needsServerUpgrade('dev', minServer: '9.9.9'), isFalse);
      expect(needsServerUpgrade('', minServer: '9.9.9'), isFalse);
    });
  });

  group('每天检查一次', () {
    final t0 = DateTime(2026, 11, 20, 8);
    test('从未检查或已过 24 小时才检查', () {
      expect(shouldCheckUpdate(null, t0), isTrue);
      expect(shouldCheckUpdate(t0, t0.add(const Duration(hours: 23))), isFalse);
      expect(shouldCheckUpdate(t0, t0.add(const Duration(hours: 24))), isTrue);
    });
  });

  group('GitHub Release 与通道', () {
    Map<String, dynamic> rel(
      String tag, {
      bool draft = false,
      List<String> assets = const [],
    }) => {
      'tag_name': tag,
      'body': '说明 $tag',
      'html_url': 'https://github.com/$githubRepo/releases/tag/$tag',
      'draft': draft,
      'prerelease': tag.contains('-'),
      'assets': [
        for (final a in assets)
          {'name': a, 'browser_download_url': 'https://dl/$tag/$a'},
      ],
    };

    test('pickRelease 忽略草稿与无效版本号，取 semver 最大（含正式版）', () {
      final list = [
        rel('v0.1.0-beta.9'),
        rel('v0.1.0-beta.10'),
        rel('v0.2.0-beta.1', draft: true),
        rel('nightly'),
      ].map(GithubRelease.fromJson);
      expect(pickRelease(list)!.version, 'v0.1.0-beta.10');
      expect(
        pickRelease([...list, GithubRelease.fromJson(rel('v0.1.0'))])!.version,
        'v0.1.0',
      );
      expect(
        pickRelease([GithubRelease.fromJson(rel('v1.0.0', draft: true))]),
        isNull,
      );
    });

    test('资产：按 lfb-app-<abi>.apk 识别 APK，取 checksums.txt', () {
      final r = GithubRelease.fromJson(
        rel(
          'v1.0.0',
          assets: [
            'lfb-app-arm64-v8a.apk',
            'lfb-app-armeabi-v7a.apk',
            'lfb-server-setup-1.0.0.exe',
            'checksums.txt',
          ],
        ),
      );
      expect(r.apks, {
        'arm64-v8a': 'https://dl/v1.0.0/lfb-app-arm64-v8a.apk',
        'armeabi-v7a': 'https://dl/v1.0.0/lfb-app-armeabi-v7a.apk',
      });
      expect(r.checksumsUrl, 'https://dl/v1.0.0/checksums.txt');
      expect(r.htmlUrl, endsWith('/releases/tag/v1.0.0'));
      expect(chooseAbi(['arm64-v8a', 'armeabi-v7a'], r.apks), 'arm64-v8a');
      expect(apkAssetName('arm64-v8a'), 'lfb-app-arm64-v8a.apk');
    });

    test('没有匹配本机 ABI 的 APK 时 chooseAbi 为 null', () {
      final r = GithubRelease.fromJson(
        rel('v1.0.0', assets: ['lfb-app-x86_64.apk', 'checksums.txt']),
      );
      expect(chooseAbi(['arm64-v8a', 'armeabi-v7a'], r.apks), isNull);
    });

    test('正式版 App 只请求 releases/latest，带 Accept 与 User-Agent', () async {
      late http.Request seen;
      final client = MockClient((req) async {
        seen = req;
        return http.Response.bytes(utf8.encode(jsonEncode(rel('v1.2.0'))), 200);
      });
      final r = await fetchRelease('1.1.0', client: client);
      expect(
        seen.url.toString(),
        'https://api.github.com/repos/$githubRepo/releases/latest',
      );
      expect(seen.headers['Accept'], 'application/vnd.github+json');
      expect(seen.headers['User-Agent'], 'lfb-app');
      expect(r!.version, 'v1.2.0');
    });

    test('正式版：还没有任何正式 Release（404）返回 null', () async {
      final client = MockClient((_) async => http.Response('{}', 404));
      expect(await fetchRelease('1.0.0', client: client), isNull);
    });

    test('预发布 App 请求列表，过滤草稿取最大（可升到正式版）', () async {
      late Uri seen;
      final client = MockClient((req) async {
        seen = req.url;
        return http.Response.bytes(
          utf8.encode(
            jsonEncode([
              rel('v0.2.0-beta.1', draft: true),
              rel('v0.1.0-beta.2'),
              rel('v0.1.0'),
            ]),
          ),
          200,
        );
      });
      final r = await fetchRelease('0.1.0-beta.1', client: client);
      expect(
        seen.toString(),
        'https://api.github.com/repos/$githubRepo/releases?per_page=20',
      );
      expect(r!.version, 'v0.1.0');
    });

    test('403 / 429 限流：提示稍后再试', () async {
      for (final code in [403, 429]) {
        final client = MockClient((_) async => http.Response('{}', code));
        await expectLater(
          fetchRelease('1.0.0', client: client),
          throwsA(
            isA<UpdateException>().having(
              (e) => e.message,
              'message',
              contains('稍后再试'),
            ),
          ),
        );
      }
    });

    test('网络不通：提示无法访问 GitHub（可能需要代理）', () async {
      final client = MockClient(
        (_) async => throw http.ClientException('Connection refused'),
      );
      await expectLater(
        fetchRelease('1.0.0', client: client),
        throwsA(
          isA<UpdateException>().having(
            (e) => e.message,
            'message',
            unreachableMessage,
          ),
        ),
      );
    });
  });

  group('checksums.txt', () {
    final a = 'a' * 64, b = 'B' * 64;
    test('解析 sha256sum 格式（含 * 二进制标记、空行）', () {
      final m = parseChecksums(
        '$a  lfb-app-arm64-v8a.apk\n\n$b *lfb-server-setup-1.0.0.exe\r\nbad line\n',
      );
      expect(m, {
        'lfb-app-arm64-v8a.apk': a,
        'lfb-server-setup-1.0.0.exe': b.toLowerCase(),
      });
    });

    test('fetchChecksum 取出指定文件；没有该文件报错', () async {
      final client = MockClient(
        (_) async => http.Response('$a  lfb-app-arm64-v8a.apk\n', 200),
      );
      final url = Uri.parse('https://dl/checksums.txt');
      expect(
        await fetchChecksum(url, 'lfb-app-arm64-v8a.apk', client: client),
        a,
      );
      await expectLater(
        fetchChecksum(url, 'lfb-app-x86_64.apk', client: client),
        throwsA(isA<UpdateException>()),
      );
    });
  });

  group('下载与 sha256 校验', () {
    late Directory dir;
    setUp(() => dir = Directory.systemTemp.createTempSync('lfb_update_test'));
    tearDown(() => dir.deleteSync(recursive: true));

    test('sha256 匹配（大小写不敏感）', () async {
      final f = File('${dir.path}/a.apk')..writeAsBytesSync([1, 2, 3]);
      final hex = sha256.convert([1, 2, 3]).toString();
      expect(await verifySha256(f, hex), isTrue);
      expect(await verifySha256(f, hex.toUpperCase()), isTrue);
      expect(await verifySha256(f, '00$hex'.substring(0, 64)), isFalse);
      expect(await verifySha256(f, ''), isFalse);
    });

    test('下载报告进度、带请求头、写入文件并校验通过', () async {
      late http.BaseRequest seen;
      final body = List<int>.generate(1000, (i) => i % 256);
      final client = MockClient.streaming((req, _) async {
        seen = req;
        return http.StreamedResponse(
          Stream.fromIterable([body.sublist(0, 400), body.sublist(400)]),
          200,
          contentLength: body.length,
        );
      });
      final progress = <(int, int?)>[];
      final dest = File('${dir.path}/b.apk');
      final dl = ApkDownload.start(
        Uri.parse('https://dl/lfb-app-arm64-v8a.apk'),
        dest,
        headers: downloadHeaders,
        sha256Hex: sha256.convert(body).toString(),
        httpClient: client,
        onProgress: (got, total) => progress.add((got, total)),
      );
      final f = await dl.done;
      expect(seen.headers['User-Agent'], 'lfb-app');
      expect(seen.headers.containsKey('Range'), isFalse);
      expect(f.readAsBytesSync(), body);
      expect(progress, [(400, 1000), (1000, 1000)]);
    });

    test('中途断开后自动重试，带 Range 续传（206）', () async {
      final body = List<int>.generate(1000, (i) => (i * 7) % 256);
      final requests = <http.BaseRequest>[];
      final client = MockClient.streaming((req, _) async {
        requests.add(req);
        if (requests.length == 1) {
          // 第一次：发出 300 字节后连接断开
          final ctrl = StreamController<List<int>>();
          ctrl.add(body.sublist(0, 300));
          scheduleMicrotask(() {
            ctrl.addError(http.ClientException('Connection closed'));
            ctrl.close();
          });
          return http.StreamedResponse(
            ctrl.stream,
            200,
            contentLength: body.length,
          );
        }
        return http.StreamedResponse(
          Stream.value(body.sublist(300)),
          206,
          contentLength: 700,
          headers: {'content-range': 'bytes 300-999/1000'},
        );
      });
      final progress = <(int, int?)>[];
      final dl = ApkDownload.start(
        Uri.parse('https://dl/x.apk'),
        File('${dir.path}/r.apk'),
        sha256Hex: sha256.convert(body).toString(),
        httpClient: client,
        retryDelay: Duration.zero,
        onProgress: (got, total) => progress.add((got, total)),
      );
      final f = await dl.done;
      expect(requests, hasLength(2));
      expect(requests[1].headers['Range'], 'bytes=300-');
      expect(f.readAsBytesSync(), body);
      expect(progress.last, (1000, 1000));
    });

    test('连接提前正常结束（少于 Content-Length）也续传', () async {
      final body = List<int>.generate(500, (i) => i % 256);
      var n = 0;
      final client = MockClient.streaming((req, _) async {
        if (++n == 1) {
          return http.StreamedResponse(
            Stream.value(body.sublist(0, 200)),
            200,
            contentLength: 500,
          );
        }
        expect(req.headers['Range'], 'bytes=200-');
        return http.StreamedResponse(
          Stream.value(body.sublist(200)),
          206,
          headers: {'content-range': 'bytes 200-499/500'},
        );
      });
      final dl = ApkDownload.start(
        Uri.parse('https://dl/x.apk'),
        File('${dir.path}/s.apk'),
        httpClient: client,
        retryDelay: Duration.zero,
      );
      expect((await dl.done).readAsBytesSync(), body);
    });

    test('多次重试无进展后失败，不留文件', () async {
      var n = 0;
      final client = MockClient.streaming((req, _) async {
        n++;
        throw http.ClientException('reset');
      });
      final dest = File('${dir.path}/t.apk');
      final dl = ApkDownload.start(
        Uri.parse('https://dl/x.apk'),
        dest,
        httpClient: client,
        maxRetries: 2,
        retryDelay: Duration.zero,
      );
      await expectLater(dl.done, throwsA(isA<http.ClientException>()));
      expect(n, 3);
      expect(dest.existsSync(), isFalse);
    });

    test('sha256 不符：删除文件并报校验失败', () async {
      final client = MockClient.streaming(
        (req, _) async => http.StreamedResponse(
          Stream.value([1, 2, 3]),
          200,
          contentLength: 3,
        ),
      );
      final dest = File('${dir.path}/v.apk');
      final dl = ApkDownload.start(
        Uri.parse('https://dl/x.apk'),
        dest,
        sha256Hex: sha256.convert([9]).toString(),
        httpClient: client,
      );
      await expectLater(
        dl.done,
        throwsA(
          isA<UpdateException>().having(
            (e) => e.message,
            'message',
            contains('校验失败'),
          ),
        ),
      );
      expect(dest.existsSync(), isFalse);
    });

    test('取消后删除半截文件并以 DownloadCancelled 结束', () async {
      final ctrl = StreamController<List<int>>();
      final client = MockClient.streaming(
        (req, _) async => http.StreamedResponse(ctrl.stream, 200),
      );
      final dest = File('${dir.path}/c.apk');
      final started = Completer<void>();
      final dl = ApkDownload.start(
        Uri.parse('http://h/x'),
        dest,
        httpClient: client,
        onProgress: (_, _) {
          if (!started.isCompleted) started.complete();
        },
      );
      ctrl.add([1, 2, 3]);
      await started.future; // 已开始写文件后再取消
      dl.cancel();
      await expectLater(dl.done, throwsA(isA<DownloadCancelled>()));
      expect(dest.existsSync(), isFalse);
      await ctrl.close();
    });

    test('尚未收到响应时取消也立即结束，且之后不再写文件', () async {
      final res = Completer<http.StreamedResponse>();
      final client = MockClient.streaming((req, _) => res.future);
      final dest = File('${dir.path}/e.apk');
      final dl = ApkDownload.start(
        Uri.parse('http://h/x'),
        dest,
        httpClient: client,
      );
      dl.cancel();
      await expectLater(dl.done, throwsA(isA<DownloadCancelled>()));
      res.complete(http.StreamedResponse(Stream.value([1]), 200));
      await Future<void>.delayed(const Duration(milliseconds: 50));
      expect(dest.existsSync(), isFalse);
    });

    test('HTTP 错误状态不重试，以异常结束且不留文件', () async {
      var n = 0;
      final client = MockClient((req) async {
        n++;
        return http.Response('', 404);
      });
      final dest = File('${dir.path}/d.apk');
      final dl = ApkDownload.start(
        Uri.parse('http://h/x'),
        dest,
        httpClient: client,
      );
      await expectLater(dl.done, throwsA(isA<UpdateException>()));
      expect(n, 1);
      expect(dest.existsSync(), isFalse);
    });
  });
}
