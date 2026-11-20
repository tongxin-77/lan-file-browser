import 'package:flutter/services.dart';
import 'package:material_ui/material_ui.dart';
import 'package:media_kit/media_kit.dart';

import 'app.dart';

void main() {
  WidgetsFlutterBinding.ensureInitialized();
  MediaKit.ensureInitialized();
  // 竖屏为主；播放页单独切横屏
  SystemChrome.setPreferredOrientations([DeviceOrientation.portraitUp]);
  runApp(const LfbApp());
}
