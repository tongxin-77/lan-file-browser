import 'package:material_ui/material_ui.dart';

/// 设计稿 A4「床头夜读」色板（docs/desgin/app/a4-night.html 开头注释）。
abstract final class NightColors {
  static const quilt = Color(0xFF201719); // 被褥：主背景
  static const pillow = Color(0xFF2A1F22); // 枕套：抬起的表面
  static const seam = Color(0xFF3A2D30); // 褶缝：分隔、描边
  static const wick = Color(0xFFE2A35E); // 灯芯：唯一强调色
  static const moon = Color(0xFFDCCFBB); // 月光纸：主文字
  static const ash = Color(0xFF9F8F89); // 烛灰：次要文字
  static const moss = Color(0xFF8E9A7C); // 晨苔：完成/正常状态
}

ThemeData buildNightTheme() {
  const scheme = ColorScheme.dark(
    primary: NightColors.wick,
    onPrimary: NightColors.quilt,
    secondary: NightColors.moss,
    onSecondary: NightColors.quilt,
    surface: NightColors.quilt,
    onSurface: NightColors.moon,
    onSurfaceVariant: NightColors.ash,
    surfaceContainer: NightColors.pillow,
    surfaceContainerHigh: NightColors.pillow,
    outline: NightColors.seam,
    outlineVariant: NightColors.seam,
    error: Color(0xFFD98A7A),
  );
  return ThemeData(
    colorScheme: scheme,
    scaffoldBackgroundColor: NightColors.quilt,
    dividerColor: NightColors.seam,
    appBarTheme: const AppBarTheme(
      backgroundColor: NightColors.quilt,
      foregroundColor: NightColors.moon,
      elevation: 0,
      scrolledUnderElevation: 0,
    ),
    // 界面不发光：标题用细体，正文用月光纸色
    textTheme: const TextTheme(
      headlineMedium: TextStyle(fontWeight: FontWeight.w300),
      titleLarge: TextStyle(fontWeight: FontWeight.w300),
    ).apply(bodyColor: NightColors.moon, displayColor: NightColors.moon),
    listTileTheme: const ListTileThemeData(
      minTileHeight: 64, // 行高 64dp
      iconColor: NightColors.ash,
      textColor: NightColors.moon,
    ),
    filledButtonTheme: FilledButtonThemeData(
      style: FilledButton.styleFrom(minimumSize: const Size(48, 52)),
    ),
    inputDecorationTheme: const InputDecorationTheme(
      filled: true,
      fillColor: NightColors.pillow,
      border: OutlineInputBorder(borderSide: BorderSide.none),
    ),
    snackBarTheme: const SnackBarThemeData(
      backgroundColor: NightColors.pillow,
      contentTextStyle: TextStyle(color: NightColors.moon),
      behavior: SnackBarBehavior.floating,
    ),
    progressIndicatorTheme: const ProgressIndicatorThemeData(
      color: NightColors.wick,
    ),
  );
}
