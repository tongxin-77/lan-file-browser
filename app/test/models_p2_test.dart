import 'package:flutter_test/flutter_test.dart';
import 'package:lfb_app/models/continue_item.dart';
import 'package:lfb_app/models/file_item.dart';
import 'package:lfb_app/models/subtitle.dart';
import 'package:lfb_app/services/thumbs.dart';

void main() {
  test('list 条目解析 hasThumb 与 progress', () {
    final a = FileItem.fromJson({
      'name': 'E12.mkv',
      'kind': 'video',
      'size': 1,
      'mtime': 0,
      'hidden': false,
      'hasThumb': true,
      'progress': {'position': 1008, 'duration': 1440, 'watched': false},
    });
    expect(a.hasThumb, isTrue);
    expect(a.progress!.position, 1008);
    expect(a.progress!.fraction, closeTo(0.7, 1e-9));
    final b = FileItem.fromJson({'name': 'x', 'kind': 'dir', 'progress': null});
    expect(b.hasThumb, isFalse);
    expect(b.progress, isNull);
  });

  group('继续观看解析', () {
    final watchedJson = {
      'share': 'k3',
      'path': '星海旅人/第一季/S01E12 灯塔.mkv',
      'name': 'S01E12 灯塔.mkv',
      'position': 1400.5,
      'duration': 1450,
      'watched': true,
      'updatedAt': '2025-06-12T18:30:00+08:00',
      'deviceName': '客厅电视',
      'next': {
        'name': 'S01E13 归途.mkv',
        'kind': 'video',
        'size': 100,
        'mtime': 1700000000000,
        'hidden': false,
        'hasThumb': true,
        'progress': null,
        'path': '星海旅人/第一季/S01E13 归途.mkv',
      },
    };

    test('看完带 next：播放下一集，从头开始', () {
      final c = ContinueItem.fromJson(watchedJson);
      expect(c.deviceName, '客厅电视');
      expect(c.updatedAt, DateTime.utc(2025, 6, 12, 10, 30));
      expect(c.playsNext, isTrue);
      final t = c.target;
      expect(t.path, '星海旅人/第一季/S01E13 归途.mkv');
      expect(t.name, 'S01E13 归途.mkv');
      expect(t.progress, isNull);
    });

    test('未看完：播放本集，带上次位置', () {
      final c = ContinueItem.fromJson({
        'share': 'm',
        'path': '雾港往事.mkv',
        'name': '雾港往事.mkv',
        'position': 4360,
        'duration': 7510,
        'watched': false,
        'updatedAt': '2025-06-12T18:30:00Z',
        'deviceName': 'Pixel 8',
      });
      expect(c.next, isNull);
      expect(c.playsNext, isFalse);
      expect(c.fraction, closeTo(0.58, 0.01));
      expect(c.target.path, '雾港往事.mkv');
      expect(c.target.progress!.position, 4360);
    });
  });

  test('字幕优先级 exact > episode > season', () {
    SubtitleInfo s(String name, String match) =>
        SubtitleInfo(name: name, path: name, match: match);
    expect(pickSubtitle([]), isNull);
    expect(
      pickSubtitle([
        s('Show.S01.chs.srt', 'season'),
        s('[组] Show [12].ass', 'episode'),
        s('E12.chs.srt', 'exact'),
        s('E12.ass', 'exact'),
      ])!.name,
      'E12.chs.srt',
    );
    expect(
      pickSubtitle([
        s('Show.S01.chs.srt', 'season'),
        s('[组] Show [12].ass', 'episode'),
      ])!.name,
      '[组] Show [12].ass',
    );
    expect(pickSubtitle([s('a.srt', 'other')]), isNull);
    expect(
      subtitleLabel(
        const SubtitleInfo(name: 'a.chs.srt', path: '', lang: 'chs'),
      ),
      '简体中文',
    );
    expect(subtitleLabel(const SubtitleInfo(name: 'a.srt', path: '')), 'a.srt');
  });

  group('缩略图', () {
    test('按显示尺寸选档位', () {
      expect(thumbWidthFor(48, 2), 160);
      expect(thumbWidthFor(80, 2), 160);
      expect(thumbWidthFor(120, 2.75), 640);
      expect(thumbWidthFor(150, 2), 320);
      expect(thumbWidthFor(400, 3), 640);
    });

    test('缓存键去掉 token、带 mtime', () {
      final mtime = DateTime.fromMillisecondsSinceEpoch(1700000000000);
      final a = Uri.parse(
        'http://192.168.1.5:8686/api/thumb?share=k3&path=a%2Fb.jpg&w=320&t=tok1',
      );
      final b = a.replace(queryParameters: {...a.queryParameters, 't': 'tok2'});
      final key = imageCacheKey(a, mtime);
      expect(key, imageCacheKey(b, mtime));
      expect(key, isNot(contains('tok')));
      expect(key, contains('w=320'));
      expect(key, endsWith('#1700000000000'));
      expect(
        imageCacheKey(a, mtime.add(const Duration(seconds: 1))),
        isNot(key),
      );
    });
  });
}
