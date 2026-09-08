import 'dart:async';

import 'package:dio/dio.dart';
import 'package:flutter/foundation.dart';

import 'auth.dart';
import 'chat_models.dart';
import 'chat_stream.dart';
import 'server.dart';

class PersonalChatApi {
  PersonalChatApi(this.session)
    : owner = session.account?.subject ?? '',
      generation = session.generation,
      family = session.metadata?.sessionId ?? '';
  final MobileSession session;
  final String owner, family;
  final int generation;
  final pendingTurns = <String, ChatTurn>{};
  bool get live =>
      session.canChat &&
      session.generation == generation &&
      session.account?.subject == owner &&
      session.metadata?.sessionId == family;
  void guard() {
    if (!live) {
      throw const VerificationFailure(
        'Personal conversations require an administrator account.',
      );
    }
  }

  String path(String id) => 'api/v1/conversations/${chatId(id)}';
  Future<Object?> request(
    String path, {
    String method = 'GET',
    Map<String, dynamic>? data,
    Map<String, String>? query,
    int status = 200,
    int maxBytes = 262144,
    CancelToken? cancel,
  }) async {
    guard();
    final result = await session.authenticatedRequest(
      path,
      method: method,
      data: data,
      query: query,
      expectedStatus: status,
      maxBytes: maxBytes,
      cancel: cancel,
    );
    guard();
    return result;
  }

  Future<List<ChatModel>> models({CancelToken? cancel}) async {
    final value = await request('api/v1/conversations/models', cancel: cancel);
    if (value is! List) throw invalidChat;
    final models = value.map(ChatModel.parse).toList();
    final keys = models.map((m) => '${m.provider}\x00${m.model}').toSet();
    if (keys.length != models.length) throw invalidChat;
    return models;
  }

  Future<ChatPage<Conversation>> conversations({
    String? before,
    CancelToken? cancel,
  }) async => ChatPage.parse(
    await request(
      'api/v1/conversations',
      query: {'limit': '10', if (before != null) 'before': chatId(before)},
      maxBytes: 2 * 1024 * 1024,
      cancel: cancel,
    ),
    (v) => Conversation.parse(v, owner),
    limit: 10,
    before: before,
  );
  Future<Conversation> conversation(String id, {CancelToken? cancel}) async {
    final result = Conversation.parse(
      await request(path(id), cancel: cancel),
      owner,
    );
    if (result.id != id) throw invalidChat;
    return result;
  }

  Future<Conversation> create(
    String title,
    ChatModel model,
    String prompt, {
    CancelToken? cancel,
  }) async => Conversation.parse(
    await request(
      'api/v1/conversations',
      method: 'POST',
      status: 201,
      cancel: cancel,
      data: {
        'title': chatText(title, 256),
        'provider_key': model.provider,
        'model': model.model,
        'system_prompt': chatText(prompt, 32768, blank: true),
      },
    ),
    owner,
  );
  Future<Conversation> rename(
    String id,
    String title, {
    CancelToken? cancel,
  }) async {
    final result = Conversation.parse(
      await request(
        path(id),
        method: 'PATCH',
        data: {'title': chatText(title, 256)},
        cancel: cancel,
      ),
      owner,
    );
    if (result.id != id) throw invalidChat;
    return result;
  }

  Future<void> delete(String id, {CancelToken? cancel}) async {
    await request(path(id), method: 'DELETE', status: 204, cancel: cancel);
  }

  Future<ChatPage<ChatMessage>> messages(
    String id, {
    String? before,
    CancelToken? cancel,
  }) async => ChatPage.parse(
    await request(
      '${path(id)}/messages',
      query: {
        'limit': '$chatMessagePageSize',
        if (before != null) 'before': chatId(before),
      },
      maxBytes: maxChatMessagePageWireBytes,
      cancel: cancel,
    ),
    (v) => ChatMessage.parse(v, id),
    limit: chatMessagePageSize,
    before: before,
  );
  Future<ChatMessage> message(
    String id,
    String messageId, {
    bool cancelGeneration = false,
    CancelToken? cancel,
  }) async {
    final result = ChatMessage.parse(
      await request(
        '${path(id)}/messages/${chatId(messageId)}${cancelGeneration ? '/cancel' : ''}',
        method: cancelGeneration ? 'POST' : 'GET',
        maxBytes: maxChatMessageWireBytes,
        cancel: cancel,
      ),
      id,
    );
    if (result.id != messageId ||
        (cancelGeneration &&
            (result.role != 'assistant' || !result.terminal))) {
      throw invalidChat;
    }
    return result;
  }

  Stream<ChatEvent> send(
    String id,
    String requestId,
    String content,
    CancelToken cancel,
  ) {
    guard();
    chatText(content, 32768);
    chatText(requestId, 128);
    return parseChatEvents(
      session.authenticatedStream('${path(id)}/messages', {
        'content': content,
        'request_id': requestId,
      }, cancel),
    );
  }
}

class ConversationController extends ChangeNotifier {
  ConversationController(
    this.api,
    this.conversation, {
    this.pollInterval = const Duration(seconds: 2),
    this.recoveryLimit = const Duration(minutes: 6),
  }) {
    api.session.addListener(_sessionChanged);
  }
  final PersonalChatApi api;
  Conversation conversation;
  final Duration pollInterval, recoveryLimit;
  final messages = <ChatMessage>[];
  final _lifetime = CancelToken();
  CancelToken? _stream;
  Timer? _poll;
  DateTime? _recoverUntil;
  bool _disposed = false;
  bool _recoveringRequest = false;
  bool ready = false;
  int _epoch = 0;
  bool loading = true, sending = false, cancelling = false, recovering = false;
  String? error;
  String nextBefore = '';
  ChatTurn? get pending => api.pendingTurns[conversation.id];
  set pending(ChatTurn? turn) {
    if (turn == null) {
      api.pendingTurns.remove(conversation.id);
    } else {
      api.pendingTurns[conversation.id] = turn;
    }
  }

  String? activeId;
  bool get live => !_disposed && api.live;
  bool get canSend =>
      live &&
      ready &&
      !loading &&
      !sending &&
      !cancelling &&
      !recovering &&
      pending == null &&
      activeId == null;
  bool _current(int epoch) => live && _epoch == epoch;
  void _notify() {
    if (!_disposed) notifyListeners();
  }

  void _sessionChanged() {
    if (!api.live) {
      ++_epoch;
      _stream?.cancel();
      _lifetime.cancel();
      _poll?.cancel();
      messages.clear();
      pending = null;
      activeId = null;
      loading = sending = recovering = cancelling = false;
      error = 'Session ended. Return to Account.';
      _notify();
    }
  }

  void _put(ChatMessage message) {
    final index = messages.indexWhere((m) => m.id == message.id);
    if (messages.any(
          (m) => m.id != message.id && m.sequence == message.sequence,
        ) ||
        (index >= 0 && messages[index].sequence != message.sequence)) {
      throw invalidChat;
    }
    if (index < 0) {
      messages.add(message);
    } else if (!messages[index].terminal || message.terminal) {
      messages[index] = message;
    }
    messages.sort((a, b) => a.sequence.compareTo(b.sequence));
  }

  Future<void> load({bool older = false}) async {
    if (!live || (older && (loading || nextBefore.isEmpty))) return;
    final epoch = _epoch;
    loading = true;
    if (!older) ready = false;
    error = null;
    _notify();
    try {
      final current = await api.conversation(
        conversation.id,
        cancel: _lifetime,
      );
      final page = await api.messages(
        conversation.id,
        before: older ? nextBefore : null,
        cancel: _lifetime,
      );
      if (!_current(epoch)) return;
      if (older) {
        final cursor = messages.firstWhere((m) => m.id == nextBefore);
        if (page.items.any((m) => m.sequence >= cursor.sequence)) {
          throw invalidChat;
        }
      }
      conversation = current;
      ready = true;
      for (final m in page.items) {
        _put(m);
        if (m.role == 'assistant' &&
            m.requestId == pending?.requestId &&
            m.terminal) {
          pending = null;
        }
      }
      nextBefore = page.nextBefore;
      for (final m in messages) {
        if (m.role == 'assistant' && !m.terminal) activeId = m.id;
      }
      if (activeId != null && !sending) _scheduleRecovery();
    } catch (e) {
      if (_current(epoch)) error = chatFailure(e);
    } finally {
      if (_current(epoch)) {
        loading = false;
        _notify();
      }
    }
  }

  Future<void> send(String content, {bool replay = false}) async {
    if (!live || sending || cancelling || loading) return;
    if (replay) {
      if (pending == null || activeId != null) return;
    } else {
      if (!canSend) return;
      chatText(content, 32768);
      pending = ChatTurn(conversation.id, newRequestId(), content);
    }
    final original = pending!;
    final turn = ChatTurn(
      conversation.id,
      original.requestId,
      original.content,
    );
    pending = turn;
    final epoch = ++_epoch;
    _poll?.cancel();
    _recoverUntil = null;
    sending = true;
    recovering = false;
    error = null;
    final token = CancelToken();
    _stream = token;
    _notify();
    try {
      await for (final event in api.send(
        conversation.id,
        turn.requestId,
        turn.content,
        token,
      )) {
        if (!_current(epoch)) break;
        turn.apply(event);
        if (turn.user != null) _put(turn.user!);
        if (turn.assistant != null) {
          _put(turn.assistant!);
          activeId = turn.assistant!.id;
        }
        _notify();
        if (turn.terminalEvent) {
          activeId = null;
          pending = null;
          break;
        }
      }
    } catch (e) {
      if (_current(epoch)) error = chatFailure(e);
    } finally {
      token.cancel('turn stream ended');
      if (identical(_stream, token)) _stream = null;
    }
    if (!_current(epoch)) return;
    sending = false;
    if (!turn.terminalEvent) {
      error = 'Reply outcome is not yet confirmed. Checking saved messages.';
      await recover();
    }
    _notify();
  }

  void _scheduleRecovery() {
    _recoverUntil ??= DateTime.now().add(recoveryLimit);
    if (!live || activeId == null || _poll?.isActive == true) return;
    if (!DateTime.now().isBefore(_recoverUntil!)) {
      recovering = false;
      error = 'Recovery paused. Check the saved reply again or cancel it.';
      _notify();
      return;
    }
    recovering = true;
    _poll = Timer(pollInterval, () {
      _poll = null;
      recover();
    });
  }

  Future<void> recover({bool restart = false}) async {
    if (!live || sending || cancelling || _recoveringRequest) return;
    _recoveringRequest = true;
    if (restart) _recoverUntil = null;
    _poll?.cancel();
    _poll = null;
    final epoch = _epoch;
    recovering = true;
    _notify();
    try {
      if (activeId == null) {
        final page = await api.messages(conversation.id, cancel: _lifetime);
        if (!_current(epoch)) return;
        for (final m in page.items) {
          _put(m);
          if (m.role == 'assistant' && m.requestId == pending?.requestId) {
            activeId = m.id;
          }
        }
        if (activeId == null) {
          error = 'Acceptance is unknown. Recover the same send to avoid a duplicate turn.';
          return;
        }
      }
      final snapshot = await api.message(
        conversation.id,
        activeId!,
        cancel: _lifetime,
      );
      if (!_current(epoch)) return;
      if (snapshot.role != 'assistant' ||
          (pending != null && snapshot.requestId != pending!.requestId)) {
        throw invalidChat;
      }
      _put(snapshot);
      if (snapshot.terminal) {
        activeId = null;
        pending = null;
        error = null;
        _recoverUntil = null;
      } else {
        error =
            'Reply is still active. Checking saved status every two seconds.';
      }
    } catch (e) {
      if (_current(epoch)) error = chatFailure(e);
    } finally {
      _recoveringRequest = false;
      if (_current(epoch)) {
        recovering = false;
        if (activeId != null) _scheduleRecovery();
        _notify();
      }
    }
  }

  Future<void> cancelReply() async {
    if (!live || activeId == null || cancelling) return;
    final id = activeId!;
    final epoch = ++_epoch;
    _stream?.cancel('explicit cancellation');
    _poll?.cancel();
    sending = recovering = false;
    cancelling = true;
    _notify();
    try {
      final snapshot = await api.message(
        conversation.id,
        id,
        cancelGeneration: true,
        cancel: _lifetime,
      );
      if (!_current(epoch)) return;
      _put(snapshot);
      activeId = null;
      pending = null;
      error = null;
    } catch (e) {
      if (_current(epoch)) error = chatFailure(e);
    } finally {
      if (_current(epoch)) {
        cancelling = false;
        if (activeId != null) _scheduleRecovery();
        _notify();
      }
    }
  }

  @override
  void dispose() {
    _disposed = true;
    ++_epoch;
    _poll?.cancel();
    _stream?.cancel('conversation left');
    _lifetime.cancel('conversation left');
    api.session.removeListener(_sessionChanged);
    messages.clear();
    super.dispose();
  }
}
