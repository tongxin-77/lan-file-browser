import 'dart:async';

import 'package:flutter/foundation.dart';

import 'package:flutter_test/flutter_test.dart';
import 'package:lfb_app/api/lfb_client.dart';
import 'package:lfb_app/models/file_item.dart';
import 'package:lfb_app/models/share.dart';
import 'package:lfb_app/pages/image_viewer_page.dart';
import 'package:lfb_app/services/fetch_queue.dart';
import 'package:lfb_app/services/server_image.dart';
import 'package:lfb_app/services/thumbs.dart';
import 'package:lfb_app/widgets/progressive_image.dart';
import 'package:lfb_app/widgets/thumb.dart';
import 'package:material_ui/material_ui.dart';
import 'package:photo_view/photo_view.dart';

/// 内存里的假磁盘缓存：记录读写；[gated] 时下载等 [gates] 中对应的 Completer。
class FakeStore implements ImageStore {
  final data = <String, Uint8List>{};
  final downloads = <String>[];
  final reads = <String>[];
  final gates = <String, Completer<Uint8List>>{};
  bool gated = false;

  @override
  Future<Uint8List?> read(String key) async {
    reads.add(key);
    return data[key];
  }

  @override
  Future<Uint8List> download(Uri url, String key) async {
    downloads.add(key);
    if (gated) return (gates[key] = Completer<Uint8List>()).future;
    return data[key] = Uint8List.fromList([1, 2, 3]);
  }

  @override
  Future<void> remove(String key) async => data.remove(key);
}

late FakeStore thumbs, previews;
late List<Uri> fetched;

void installFakeStores({bool hangFetch = false}) {
  thumbs = FakeStore();
  previews = FakeStore();
  fetched = [];
  ImageStores.instance = ImageStores(
    thumbs: thumbs,
    previews: previews,
    fetch: (url) {
      fetched.add(url);
      return hangFetch
          ? Completer<Uint8List>().future
          : Future.value(Uint8List.fromList([9]));
    },
  );
}

LfbClient client({String token = 'tok'}) =>
    LfbClient(host: 'h', port: 1, token: token)
      ..vault = const VaultAccess('sess', {'v1'});

/// 可控的测试图片：[done] 完成后才出图。
class TestImage extends ImageProvider<TestImage> {
  TestImage(this.name);

  final String name;
  final done = Completer<ImageInfo>();
  int loads = 0;

  @override
  Future<TestImage> obtainKey(ImageConfiguration configuration) =>
      SynchronousFuture(this);

  @override
  ImageStreamCompleter loadImage(TestImage key, ImageDecoderCallback decode) {
    loads++;
    return OneFrameImageStreamCompleter(done.future);
  }

  @override
  String toString() => 'TestImage($name)';
}

Future<void> deliver(WidgetTester tester, TestImage img, int w, int h) =>
    tester.runAsync(() async {
      img.done.complete(
        ImageInfo(
          image: await createTestImage(width: w, height: h),
        ),
      );
    });

void main() {
  group('档位选择', () {
    test('预览图按屏幕物理长边向上取档', () {
      expect(previewWidthFor(const Size(720, 1280)), 1280);
      expect(previewWidthFor(const Size(1080, 1920)), 1920);
      expect(previewWidthFor(const Size(1080, 2400)), 2560);
      expect(previewWidthFor(const Size(1440, 3200)), 2560);
      expect(previewWidthFor(const Size(2400, 1080)), 2560); // 横屏同长边
      expect(previewWidthFor(Size.zero), 1280);
    });

    test('预览图像素不够时才需要原图', () {
      bool need(double scale, {Size preview = const Size(1920, 1440)}) =>
          needsOriginal(
            displayedLogicalWidth: 400,
            scale: scale,
            devicePixelRatio: 3,
            previewPixels: preview,
            requestedWidth: 1920,
          );
      expect(need(1), isFalse); // 1200 px 显示，预览 1920 足够
      expect(need(1.9), isFalse); // 2280 < 1920 × 1.2
      expect(need(2), isTrue); // 2400 > 2304
      // 预览图未达请求档位：原图本就不大，不再加载
      expect(need(4, preview: const Size(1600, 1200)), isFalse);
    });
  });

  group('FetchQueue', () {
    test('并发上限、同键去重、按加入顺序出队', () async {
      final q = FetchQueue(maxConcurrent: 3);
      final gates = <String, Completer<String>>{};
      final started = <String>[];
      Future<String> task(String k) {
        started.add(k);
        return (gates[k] = Completer<String>()).future;
      }

      final futures = {
        for (final k in ['a', 'b', 'c', 'd', 'e']) k: q.run(k, () => task(k)),
      };
      expect(identical(q.run('a', () => task('x')), futures['a']), isFalse);
      expect(started, ['a', 'b', 'c']);
      expect(q.running, 3);
      expect(q.pendingKeys, ['d', 'e']);

      gates['b']!.complete('B');
      expect(await futures['b'], 'B');
      await Future<void>.delayed(Duration.zero);
      expect(started, ['a', 'b', 'c', 'd']);
      expect(q.running, 3);
    });

    test('取消排队中的请求；进行中、被引用的不取消；引用归零自动取消', () async {
      final q = FetchQueue(maxConcurrent: 1);
      final gate = Completer<int>();
      final started = <String>[];
      final a = q.run('a', () {
        started.add('a');
        return gate.future;
      });
      final b = q.run('b', () async {
        started.add('b');
        return 2;
      });
      final c = q.run('c', () async {
        started.add('c');
        return 3;
      });

      q.cancel('a'); // 已开始：不取消
      q.cancel('b');
      await expectLater(b, throwsA(isA<FetchCancelled>()));

      q.retain('c');
      q.cancel('c'); // 被引用：不取消
      expect(q.pendingKeys, ['c']);
      q.release('c'); // 引用归零：取消
      await expectLater(c, throwsA(isA<FetchCancelled>()));

      gate.complete(1);
      expect(await a, 1);
      expect(started, ['a']);
    });

    test('被引用（正在显示）的键优先出队', () async {
      final q = FetchQueue(maxConcurrent: 1);
      final gate = Completer<void>();
      final started = <String>[];
      Future<void> task(String k) async {
        started.add(k);
        if (k == 'busy') await gate.future;
      }

      q.run('busy', () => task('busy'));
      for (final k in ['p1', 'p2', 'shown']) {
        q.run(k, () => task(k));
      }
      q.retain('shown');
      gate.complete();
      await Future<void>.delayed(Duration.zero);
      await Future<void>.delayed(Duration.zero);
      expect(started.take(2), ['busy', 'shown']);
    });
  });

  group('缓存键与磁盘缓存', () {
    final mtime = DateTime.fromMillisecondsSinceEpoch(1700000000000);
    setUp(installFakeStores);

    test('缩略图、预览图进对应磁盘缓存，缓存键不含 token', () {
      final c = client();
      final thumb = serverImageProvider(
        c,
        'k3',
        c.thumbUrl('k3', 'a.jpg', 320),
        mtime,
      );
      expect(thumb.store, same(thumbs));
      expect(thumb.queue, same(ImageStores.instance.thumbQueue));
      expect(thumb.cacheKey, isNot(contains('tok')));
      final preview = serverImageProvider(
        c,
        'k3',
        c.thumbUrl('k3', 'a.jpg', 1920),
        mtime,
      );
      expect(preview.store, same(previews));
      expect(preview.queue, same(ImageStores.instance.viewerQueue));
      // 原图只进内存
      final original = serverImageProvider(
        c,
        'k3',
        c.fileUrl('k3', 'a.jpg'),
        mtime,
      );
      expect(original.store, isNull);
    });

    test('token 变化后仍命中磁盘缓存，不再下载', () async {
      final c1 = client(token: 'tok1'), c2 = client(token: 'tok2');
      final p1 = serverImageProvider(
        c1,
        'k3',
        c1.thumbUrl('k3', 'a.jpg', 320),
        mtime,
      );
      await p1.loadBytes();
      expect(thumbs.downloads, [p1.cacheKey]);
      final p2 = serverImageProvider(
        c2,
        'k3',
        c2.thumbUrl('k3', 'a.jpg', 320),
        mtime,
      );
      expect(p2, p1); // 内存缓存同键
      await p2.loadBytes();
      expect(thumbs.downloads, hasLength(1));
      expect(thumbs.data.keys.single, isNot(contains('tok')));
    });

    test('带缓存版本时版本进缓存键，缺失时不带参数', () {
      final item = FileItem(
        name: 'a.jpg',
        kind: FileKind.image,
        size: 2048,
        mtime: mtime,
      );
      expect(item.thumbVer, '1700000000000_2048');
      final c = client();
      expect(
        c.thumbUrl('k3', 'a.jpg', 320).queryParameters.containsKey('ver'),
        isFalse,
      );
      final a = imageCacheKey(c.thumbUrl('k3', 'a.jpg', 320, ver: 'v1'), mtime);
      final b = imageCacheKey(c.thumbUrl('k3', 'a.jpg', 320, ver: 'v2'), mtime);
      expect(a, contains('ver=v1'));
      expect(a, isNot(b));
    });

    test('加密片库的缩略图与预览图只进内存，不读写磁盘缓存', () async {
      final c = client();
      for (final w in [160, 320, 640, 1280, 1920, 2560]) {
        final p = serverImageProvider(
          c,
          'v1',
          c.thumbUrl('v1', 'a.jpg', w),
          mtime,
        );
        expect(p.store, isNull, reason: 'w=$w');
        await p.loadBytes();
      }
      await serverImageProvider(
        c,
        'v1',
        c.fileUrl('v1', 'a.jpg'),
        mtime,
      ).loadBytes();
      expect(fetched, hasLength(7));
      expect(fetched.every((u) => u.queryParameters['v'] == 'sess'), isTrue);
      for (final s in [thumbs, previews]) {
        expect(s.reads, isEmpty);
        expect(s.downloads, isEmpty);
        expect(s.data, isEmpty);
      }
      expect(await cachedThumb(c, 'v1', 'a.jpg', mtime), isNull);
      expect(thumbs.reads, isEmpty);
    });

    test('看图底图：取已缓存的最大档缩略图', () async {
      final c = client();
      final p320 = serverImageProvider(
        c,
        'k3',
        c.thumbUrl('k3', 'a.jpg', 320),
        mtime,
      );
      expect(await cachedThumb(c, 'k3', 'a.jpg', mtime), isNull);
      await p320.loadBytes();
      expect(await cachedThumb(c, 'k3', 'a.jpg', mtime), p320);
    });
  });

  group('渐进加载', () {
    testWidgets('缩略图底图 → 预览图淡入 → 放大超过预览图分辨率才加载原图', (tester) async {
      tester.view.physicalSize = const Size(400, 800);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.reset);
      final base = TestImage('base'),
          preview = TestImage('preview'),
          original = TestImage('original');
      var settled = 0;
      await deliver(tester, base, 160, 120);
      await tester.pumpWidget(
        MaterialApp(
          home: ProgressiveImage(
            base: Future.value(base),
            preview: preview,
            previewWidth: 1280,
            original: original,
            onSettled: () => settled++,
          ),
        ),
      );
      await tester.pump();
      List<ImageProvider> layers() => [
        for (final i in tester.widgetList<Image>(find.byType(Image))) i.image,
      ];
      expect(layers(), [base, preview]);
      expect(find.byType(LinearProgressIndicator), findsOneWidget);
      expect(settled, 0);

      await deliver(tester, preview, 1280, 960);
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 300));
      expect(settled, 1);
      expect(find.byType(LinearProgressIndicator), findsNothing);
      expect(original.loads, 0);

      // 400 逻辑像素宽 × 3 = 1200 < 1280 × 1.2：预览图足够
      final controller = tester
          .widget<PhotoView>(find.byType(PhotoView))
          .controller!;
      controller.scale = 3;
      await tester.pump();
      expect(original.loads, 0);

      controller.scale = 5; // 2000 > 1536：需要原图
      await tester.pump();
      expect(original.loads, 1);
      expect(layers(), [base, preview, original]);
      expect(find.byType(LinearProgressIndicator), findsOneWidget);

      await deliver(tester, original, 4000, 3000);
      await tester.pump();
      expect(find.byType(LinearProgressIndicator), findsNothing);
    });

    testWidgets('预览图已在缓存（预取过）：立即显示，无进度条', (tester) async {
      tester.view.physicalSize = const Size(400, 800);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.reset);
      final preview = TestImage('preview'), original = TestImage('original');
      var settled = 0;
      await deliver(tester, preview, 1280, 960);
      await tester.pumpWidget(const MaterialApp(home: SizedBox()));
      await tester.runAsync(
        () => precacheImage(preview, tester.element(find.byType(SizedBox))),
      );
      await tester.pumpWidget(
        MaterialApp(
          home: ProgressiveImage(
            preview: preview,
            previewWidth: 1280,
            original: original,
            onSettled: () => settled++,
          ),
        ),
      );
      await tester.pump();
      expect(tester.takeException(), isNull);
      expect(settled, 1);
      expect(find.byType(LinearProgressIndicator), findsNothing);
      expect(find.byType(CircularProgressIndicator), findsNothing);
      expect(original.loads, 0);
    });

    testWidgets('预览图失败时改加载原图', (tester) async {
      final preview = TestImage('preview'), original = TestImage('original');
      await tester.pumpWidget(
        MaterialApp(
          home: ProgressiveImage(
            preview: preview,
            previewWidth: 1280,
            original: original,
          ),
        ),
      );
      expect(find.byType(CircularProgressIndicator), findsOneWidget);
      preview.done.completeError(Exception('404'));
      await tester.pump();
      await tester.pump();
      expect(original.loads, 1);
    });
  });

  group('看图预取', () {
    FileItem img(String n) => FileItem(
      name: n,
      kind: FileKind.image,
      size: 1,
      mtime: DateTime.fromMillisecondsSinceEpoch(0),
      hasThumb: true,
    );

    testWidgets('当前图有结果后预取前后各 2 张（后 1 前 1 优先），并发 ≤ 3，翻页取消不再需要的', (
      tester,
    ) async {
      tester.view.physicalSize = const Size(1080, 1920);
      tester.view.devicePixelRatio = 3;
      addTearDown(tester.view.reset);
      installFakeStores(hangFetch: true);
      previews.gated = true;
      final q = ImageStores.instance.viewerQueue;
      final c = client();
      final images = [for (var i = 0; i < 8; i++) img('$i.jpg')];
      String key(int i) => serverImageProvider(
        c,
        'k3',
        c.thumbUrl('k3', '$i.jpg', 1920, ver: images[i].thumbVer),
        images[i].mtime,
      ).cacheKey;

      await tester.pumpWidget(
        MaterialApp(
          home: ImageViewerPage(
            client: c,
            share: const Share(
              id: 'k3',
              name: '相册',
              online: true,
              perms: SharePerms(),
            ),
            dir: '',
            images: images,
            initialIndex: 3,
          ),
        ),
      );
      await tester.pump();
      expect(previews.downloads, [key(3)]); // 当前图有结果前不预取

      // 当前预览图失败也算“有结果”：改取原图（只进内存），并开始预取
      previews.gates[key(3)]!.completeError(Exception('boom'));
      for (var i = 0; i < 5; i++) {
        await tester.pump();
      }
      expect(fetched.single.path, '/api/file');
      expect(previews.downloads, [key(3), key(4), key(2)]);
      expect(q.running, 3); // 原图 + 2 张预取
      expect(q.pendingKeys, [key(5), key(1)]);

      // 翻到下一张：排队中的旧预取被取消，当前图的预览已在下载
      await tester.drag(find.byType(PageView), const Offset(-500, 0));
      for (var i = 0; i < 10; i++) {
        await tester.pump(const Duration(milliseconds: 100));
      }
      expect(find.text('5 / 8'), findsOneWidget);
      expect(q.pendingKeys, isNot(contains(key(1))));
      expect(q.pendingKeys, isNot(contains(key(5))));
      expect(q.running, lessThanOrEqualTo(3));
    });
  });
}
