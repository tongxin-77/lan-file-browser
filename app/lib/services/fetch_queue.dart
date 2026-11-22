import 'dart:async';

/// 排队中的请求被取消（已不再需要）。
class FetchCancelled implements Exception {
  const FetchCancelled();

  @override
  String toString() => 'FetchCancelled';
}

/// 有并发上限、同键去重、可取消的请求队列（缩略图网格、看图预览/原图）。
///
/// [retain] / [release] 为界面上正在显示的条目计数：被引用的键优先出队，且不会被 [cancel]；
/// 引用归零时若仍在排队则自动取消（滑出屏幕、翻页离开）。已开始的请求不中断。
class FetchQueue {
  FetchQueue({this.maxConcurrent = 3});

  final int maxConcurrent;
  final _pending = <Object, _Job>{}; // 按加入顺序
  final _running = <Object, Future<Object?>>{};
  final _refs = <Object, int>{};

  int get running => _running.length;
  Iterable<Object> get pendingKeys => _pending.keys;

  /// 执行 [task]；同一 [key] 已在排队或进行中时复用其结果。被取消时以 [FetchCancelled] 失败。
  Future<T> run<T>(Object key, Future<T> Function() task) {
    final existing = _running[key] ?? _pending[key]?.done.future;
    if (existing != null) return existing.then((v) => v as T);
    final job = _Job(task);
    _pending[key] = job;
    _pump();
    return job.done.future.then((v) => v as T);
  }

  /// 取消排队中的 [key]（被 [retain] 引用或已开始的不取消）。
  void cancel(Object key) {
    if (_refs.containsKey(key)) return;
    _pending.remove(key)?.done.completeError(const FetchCancelled());
  }

  void retain(Object key) => _refs[key] = (_refs[key] ?? 0) + 1;

  void release(Object key) {
    final n = (_refs[key] ?? 0) - 1;
    if (n > 0) {
      _refs[key] = n;
      return;
    }
    _refs.remove(key);
    cancel(key);
  }

  void _pump() {
    while (_running.length < maxConcurrent && _pending.isNotEmpty) {
      final key = _pending.keys.firstWhere(
        _refs.containsKey,
        orElse: () => _pending.keys.first,
      );
      final job = _pending.remove(key)!;
      _running[key] = job.done.future;
      Future.sync(job.task)
          .then(job.done.complete, onError: job.done.completeError)
          .whenComplete(() {
            _running.remove(key);
            _pump();
          });
    }
  }
}

class _Job {
  _Job(this.task);

  final Future<Object?> Function() task;
  final done = Completer<Object?>();
}
