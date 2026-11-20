/// `GET /api/subtitles` 的条目（03 §4.7）。
class SubtitleInfo {
  const SubtitleInfo({
    required this.name,
    required this.path,
    this.format = '',
    this.lang = '',
    this.match = '',
  });

  factory SubtitleInfo.fromJson(Map<String, dynamic> j) => SubtitleInfo(
    name: j['name'] as String,
    path: j['path'] as String,
    format: j['format'] as String? ?? '',
    lang: j['lang'] as String? ?? '',
    match: j['match'] as String? ?? '',
  );

  final String name;
  final String path;
  final String format;
  final String lang;

  /// `exact` / `episode` / `season`。
  final String match;
}

const _matchRank = {'exact': 0, 'episode': 1, 'season': 2};

/// 自动加载的字幕：按 exact > episode > season 取第一条（同级保持服务端顺序）；无则 null。
SubtitleInfo? pickSubtitle(List<SubtitleInfo> subs) {
  SubtitleInfo? best;
  var bestRank = 1 << 30;
  for (final s in subs) {
    final r = _matchRank[s.match];
    if (r != null && r < bestRank) {
      best = s;
      bestRank = r;
    }
  }
  return best;
}

/// 字幕菜单里的显示名：有语言后缀时显示语言，否则显示文件名。
String subtitleLabel(SubtitleInfo s) => switch (s.lang.toLowerCase()) {
  'chs' || 'sc' || 'zh' || 'zh-cn' || 'zh-hans' || 'gb' => '简体中文',
  'cht' || 'tc' || 'zh-tw' || 'zh-hk' || 'zh-hant' || 'big5' => '繁体中文',
  'en' || 'eng' => '英文',
  'ja' || 'jp' || 'jpn' => '日文',
  '' => s.name,
  _ => s.lang,
};
