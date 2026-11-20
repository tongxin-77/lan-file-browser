import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:lfb_app/models/share.dart';
import 'package:lfb_app/services/image_saver.dart';

class FakeGallery extends GalleryAccess {
  FakeGallery({this.granted = false, this.grantOnRequest = false});

  bool granted;
  final bool grantOnRequest;
  int requests = 0;
  final saved = <String>[];

  @override
  Future<bool> hasAccess() async => granted;

  @override
  Future<bool> requestAccess() async {
    requests++;
    return granted = grantOnRequest;
  }

  @override
  Future<void> putImageBytes(Uint8List bytes, String name) async =>
      saved.add(name);
}

void main() {
  const allow = SharePerms(download: true);
  var downloads = 0;
  Future<Uint8List> download() async {
    downloads++;
    return Uint8List(3);
  }

  setUp(() => downloads = 0);

  test('仅共享允许下载时显示保存按钮', () {
    expect(canSaveToPhone(allow), isTrue);
    expect(canSaveToPhone(const SharePerms()), isFalse);
  });

  test('无下载权限：不请求系统权限、不下载', () async {
    final g = FakeGallery(granted: true);
    final r = await saveImageToPhone(
      perms: const SharePerms(),
      fileName: 'a.jpg',
      download: download,
      gallery: g,
    );
    expect(r, SaveResult.forbidden);
    expect(downloads, 0);
    expect(g.saved, isEmpty);
  });

  test('已有访问权限（Android 10+）：直接保存，去掉扩展名', () async {
    final g = FakeGallery(granted: true);
    final r = await saveImageToPhone(
      perms: allow,
      fileName: 'IMG_20250612_183015.jpg',
      download: download,
      gallery: g,
    );
    expect(r, SaveResult.saved);
    expect(g.requests, 0);
    expect(g.saved, ['IMG_20250612_183015']);
  });

  test('旧版 Android 需申请：同意后保存，拒绝则不下载', () async {
    final ok = FakeGallery(grantOnRequest: true);
    expect(
      await saveImageToPhone(
        perms: allow,
        fileName: 'a.png',
        download: download,
        gallery: ok,
      ),
      SaveResult.saved,
    );
    expect(ok.requests, 1);

    final no = FakeGallery();
    expect(
      await saveImageToPhone(
        perms: allow,
        fileName: 'a.png',
        download: download,
        gallery: no,
      ),
      SaveResult.denied,
    );
    expect(downloads, 1);
    expect(no.saved, isEmpty);
  });

  test('下载失败返回 failed', () async {
    final r = await saveImageToPhone(
      perms: allow,
      fileName: 'a.png',
      download: () async => throw Exception('net'),
      gallery: FakeGallery(granted: true),
    );
    expect(r, SaveResult.failed);
  });
}
