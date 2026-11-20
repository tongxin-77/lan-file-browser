import 'package:flutter_test/flutter_test.dart';
import 'package:lfb_app/models/file_item.dart';
import 'package:lfb_app/services/playback.dart';

PathItem ep(String name) => PathItem(
  FileItem(name: name, kind: FileKind.video, size: 0, mtime: DateTime(2025)),
  'S01/$name',
);

void main() {
  group('进度上报节流', () {
    late DateTime now;
    late List<(int, int)> sent;
    late ProgressReporter r;

    setUp(() {
      now = DateTime(2025);
      sent = [];
      r = ProgressReporter(
        (p, d) => sent.add((p.inSeconds, d.inSeconds)),
        clock: () => now,
      );
    });

    void at(int sec, {int dur = 1440}) {
      now = DateTime(2025).add(Duration(seconds: sec));
      r.onPosition(Duration(seconds: 100 + sec), Duration(seconds: dur));
    }

    test('播放中每 10 秒最多上报一次', () {
      for (var s = 0; s <= 25; s++) {
        at(s);
      }
      expect(sent, [(110, 1440), (120, 1440)]);
    });

    test('暂停 flush 立即上报，位置未变时退出不重复', () {
      at(0);
      at(3);
      r.flush(const Duration(seconds: 103), const Duration(seconds: 1440));
      r.flush(const Duration(seconds: 103), const Duration(seconds: 1440));
      expect(sent, [(103, 1440)]);
      // flush 后重新计时
      at(12);
      expect(sent.length, 1);
      at(13);
      expect(sent.last, (113, 1440));
    });

    test('时长未知或位置为 0 不上报', () {
      r.flush(Duration.zero, const Duration(seconds: 100));
      r.flush(const Duration(seconds: 5), Duration.zero);
      expect(sent, isEmpty);
    });

    test('reset 后新文件从头计时', () {
      at(0);
      at(10);
      r.reset();
      at(11);
      at(15);
      expect(sent, [(110, 1440)]);
    });
  });

  group('续播位置判定', () {
    WatchProgress p(double pos, double dur, {bool watched = false}) =>
        WatchProgress(position: pos, duration: dur, watched: watched);

    test('无进度从头', () => expect(resumePosition(null), Duration.zero));
    test('中途恢复到上次位置', () {
      expect(
        resumePosition(p(1008.5, 1440)),
        const Duration(milliseconds: 1008500),
      );
    });
    test('已看完从头', () {
      expect(resumePosition(p(700, 1440, watched: true)), Duration.zero);
    });
    test('接近片尾（剩余 < 2 分钟）从头', () {
      expect(resumePosition(p(1330, 1440)), Duration.zero);
    });
    test('短视频 ≥ 90% 从头，不足 90% 恢复', () {
      expect(resumePosition(p(275, 300)), Duration.zero);
      expect(resumePosition(p(200, 300)), const Duration(seconds: 200));
    });
    test('刚开头（< 5 秒）从头', () {
      expect(resumePosition(p(3, 1440)), Duration.zero);
    });
  });

  test('预取与片尾卡片触发时机', () {
    const dur = Duration(minutes: 24);
    expect(shouldPrefetchNext(const Duration(minutes: 21), dur), isFalse);
    expect(shouldPrefetchNext(const Duration(minutes: 22), dur), isTrue);
    expect(
      shouldPrefetchNext(Duration.zero, const Duration(seconds: 90)),
      isFalse,
    );
    expect(shouldShowUpNext(dur - const Duration(seconds: 6), dur), isFalse);
    expect(shouldShowUpNext(dur - const Duration(seconds: 5), dur), isTrue);
    expect(shouldShowUpNext(Duration.zero, Duration.zero), isFalse);
  });

  group('下一集倒计时状态机', () {
    test('倒数 5 秒后触发', () {
      final c = UpNextCountdown();
      expect(c.start(ep('E13.mkv')), isTrue);
      expect(c.phase, UpNextPhase.counting);
      expect(c.remaining, 5);
      for (var i = 0; i < 4; i++) {
        expect(c.tick(), isFalse);
      }
      expect(c.remaining, 1);
      expect(c.tick(), isTrue);
      expect(c.phase, UpNextPhase.fired);
      expect(c.tick(), isFalse);
    });

    test('取消后不再触发、本集内不再开始', () {
      final c = UpNextCountdown()..start(ep('E13.mkv'));
      c.tick();
      c.cancel();
      expect(c.phase, UpNextPhase.cancelled);
      for (var i = 0; i < 10; i++) {
        expect(c.tick(), isFalse);
      }
      expect(c.start(ep('E13.mkv')), isFalse);
      expect(c.playNow(), isFalse);
    });

    test('立即播放', () {
      final c = UpNextCountdown()..start(ep('E13.mkv'));
      expect(c.playNow(), isTrue);
      expect(c.phase, UpNextPhase.fired);
      expect(c.playNow(), isFalse);
    });

    test('切集后 reset 可再次开始', () {
      final c = UpNextCountdown()..start(ep('E13.mkv'));
      c.playNow();
      c.reset();
      expect(c.phase, UpNextPhase.idle);
      expect(c.next, isNull);
      expect(c.start(ep('E14.mkv')), isTrue);
      expect(c.next!.item.name, 'E14.mkv');
    });
  });

  test('片名展示', () {
    expect(episodeLabel('星海旅人 S01E13 归途.mkv'), '第 13 集 · 归途');
    expect(episodeLabel('Show.S01E12.1080p.mkv'), '第 12 集');
    expect(episodeLabel('第12集 灯塔.mp4'), '第 12 集 · 灯塔');
    expect(episodeLabel('[Sub][07][1080P].mkv'), '第 7 集');
    expect(episodeLabel('EP03.mkv'), '第 3 集');
    expect(episodeLabel('雾港往事.mkv'), '雾港往事');
  });
}
