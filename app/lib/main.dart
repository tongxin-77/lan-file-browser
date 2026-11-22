import 'package:flutter/services.dart';
import 'package:flutter_cache_manager/flutter_cache_manager.dart';
import 'package:material_ui/material_ui.dart';
import 'package:media_kit/media_kit.dart';

import 'app.dart';

void main() {
  WidgetsFlutterBinding.ensureInitialized();
  WidgetsBinding.instance.addObserver(vaultController); // 切后台 / 锁屏即上锁
  MediaKit.ensureInitialized();
  // 旧版图片磁盘缓存（cached_network_image 默认缓存，含原图）已不再使用，清掉释放空间
  DefaultCacheManager().emptyCache().ignore();
  // 竖屏为主；播放页单独切横屏
  SystemChrome.setPreferredOrientations([DeviceOrientation.portraitUp]);
  runApp(const LfbApp());
}
