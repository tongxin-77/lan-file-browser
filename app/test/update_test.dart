import 'dart:async';
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

  group('AppRelease 解析', () {
    test('解析 /api/app/latest', () {
      final r = AppRelease.fromJson({
        'version': '1.3.0',
        'minAppVersion': '1.1.0',
        'notes': '修复',
        'apks': {'arm64-v8a': 'http://h/api/app/apk/arm64-v8a'},
        'sha256': {'arm64-v8a': 'ab'},
      });
      expect(r.version, '1.3.0');
      expect(r.minAppVersion, '1.1.0');
      expect(r.notes, '修复');
      expect(r.apks['arm64-v8a'], 'http://h/api/app/apk/arm64-v8a');
      expect(r.sha256['arm64-v8a'], 'ab');
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

    test('下载报告进度、带 Bearer 头、写入文件', () async {
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
        Uri.parse('http://h/api/app/apk/arm64-v8a'),
        dest,
        headers: {'Authorization': 'Bearer t'},
        httpClient: client,
        onProgress: (got, total) => progress.add((got, total)),
      );
      final f = await dl.done;
      expect(seen.headers['Authorization'], 'Bearer t');
      expect(f.readAsBytesSync(), body);
      expect(progress, [(400, 1000), (1000, 1000)]);
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

    test('HTTP 错误以异常结束且不留文件', () async {
      final client = MockClient((req) async => http.Response('', 404));
      final dest = File('${dir.path}/d.apk');
      final dl = ApkDownload.start(
        Uri.parse('http://h/x'),
        dest,
        httpClient: client,
      );
      await expectLater(dl.done, throwsA(isA<HttpException>()));
      expect(dest.existsSync(), isFalse);
    });
  });
}
