import 'dart:typed_data';

import 'package:gal/gal.dart';

import '../models/share.dart';

/// 保存到手机的相册目录（Android 上为 Pictures/LanFileBrowser）。
const saveAlbum = 'LanFileBrowser';

/// 仅共享允许下载时才显示“保存到手机”（03 §3）。
bool canSaveToPhone(SharePerms perms) => perms.download;

enum SaveResult { saved, forbidden, denied, failed }

/// 系统相册访问（默认实现为 gal：Android 10+ 走 MediaStore 无需权限，
/// Android 9 及以下申请 WRITE_EXTERNAL_STORAGE；测试中替换）。
class GalleryAccess {
  const GalleryAccess();

  Future<bool> hasAccess() => Gal.hasPermission(toAlbum: true);
  Future<bool> requestAccess() => Gal.requestPermission(toAlbum: true);
  Future<void> putImageBytes(Uint8List bytes, String name) =>
      Gal.putImageBytes(bytes, album: saveAlbum, name: name);
}

/// 保存图片：无下载权限 → forbidden；系统权限未授予且申请被拒 → denied；
/// 下载或写入失败 → failed。[download] 只在确认可保存后才调用。
Future<SaveResult> saveImageToPhone({
  required SharePerms perms,
  required String fileName,
  required Future<Uint8List> Function() download,
  GalleryAccess gallery = const GalleryAccess(),
}) async {
  if (!canSaveToPhone(perms)) return SaveResult.forbidden;
  try {
    if (!await gallery.hasAccess() && !await gallery.requestAccess()) {
      return SaveResult.denied;
    }
    final bytes = await download();
    final dot = fileName.lastIndexOf('.');
    await gallery.putImageBytes(
      bytes,
      dot > 0 ? fileName.substring(0, dot) : fileName,
    );
    return SaveResult.saved;
  } on Exception {
    return SaveResult.failed;
  }
}
