import 'dart:convert';

import 'chat_models.dart';

class ChatEvent {
  const ChatEvent(this.name, this.data);
  final String name;
  final Object? data;
}

Stream<ChatEvent> parseChatEvents(
  Stream<List<int>> source, {
  int maxFrameBytes = maxChatMessageWireBytes,
}) async* {
  final line = StringBuffer();
  var lineBytes = 0;
  var frameBytes = 0;
  var name = '';
  String? data;
  // UTF-8 decoding retains incomplete multibyte sequences across network chunks.
  await for (final text in source.transform(utf8.decoder)) {
    var start = 0;
    while (start < text.length) {
      final end = text.indexOf('\n', start);
      final part = text.substring(start, end < 0 ? text.length : end);
      final bytes = utf8.encode(part).length;
      lineBytes += bytes;
      frameBytes += bytes;
      if (frameBytes > maxFrameBytes || lineBytes > maxFrameBytes) {
        throw invalidChat;
      }
      line.write(part);
      if (end < 0) break;
      var value = line.toString();
      if (value.endsWith('\r')) value = value.substring(0, value.length - 1);
      line.clear();
      lineBytes = 0;
      frameBytes++;
      if (frameBytes > maxFrameBytes) throw invalidChat;
      start = end + 1;
      if (value.isEmpty) {
        if ({
          'accepted',
          'delta',
          'heartbeat',
          'snapshot',
          'done',
          'error',
        }.contains(name)) {
          if (data == null) throw invalidChat;
          yield ChatEvent(name, jsonDecode(data));
        }
        name = '';
        data = null;
        frameBytes = 0;
      } else if (value.startsWith('event:')) {
        if (name.isNotEmpty) throw invalidChat;
        name = value.substring(6).trim();
      } else if (value.startsWith('data:')) {
        if (data != null) throw invalidChat;
        data = value.substring(5).trimLeft();
      } else if (!value.startsWith(':')) {
        // No event cursors or retry directives are defined by this contract.
        throw invalidChat;
      }
    }
  }
  if (line.isNotEmpty || name.isNotEmpty || data != null) throw invalidChat;
}

class ChatTurn {
  ChatTurn(this.conversationId, this.requestId, this.content);
  final String conversationId, requestId, content;
  ChatMessage? user, assistant;
  bool terminalEvent = false;

  void apply(ChatEvent event) {
    if (terminalEvent) throw invalidChat;
    final map = chatObject(event.data);
    if (event.name == 'accepted') {
      if (assistant != null || map['replay'] is! bool) throw invalidChat;
      final u = ChatMessage.parse(map['user'], conversationId);
      final a = ChatMessage.parse(map['assistant'], conversationId);
      if (u.role != 'user' ||
          a.role != 'assistant' ||
          u.id == a.id ||
          u.sequence >= a.sequence ||
          u.requestId != requestId ||
          a.requestId != requestId ||
          u.content != content ||
          u.provider != a.provider ||
          u.model != a.model) {
        throw invalidChat;
      }
      user = u;
      assistant = a;
      return;
    }
    final previous = assistant;
    if (previous == null) throw invalidChat;
    if (event.name == 'delta' || event.name == 'heartbeat') {
      if (map['assistant_message_id'] != previous.id) throw invalidChat;
      if (event.name == 'heartbeat') return;
      final addition = chatText(map['content'], maxAssistantBytes, blank: true);
      final length = utf8.encode(previous.content).length;
      if (previous.terminal ||
          map['offset'] is! int ||
          map['offset'] != length ||
          utf8.encode(addition).length > maxAssistantBytes - length) {
        throw invalidChat;
      }
      assistant = ChatMessage.delta(previous, addition);
    } else {
      final snapshot = ChatMessage.parse(map, conversationId);
      if (snapshot.id != previous.id ||
          snapshot.sequence != previous.sequence ||
          snapshot.role != 'assistant' ||
          snapshot.requestId != requestId ||
          snapshot.provider != previous.provider ||
          snapshot.model != previous.model) {
        throw invalidChat;
      }
      if (event.name == 'done' && snapshot.status != 'completed') {
        throw invalidChat;
      }
      if (event.name == 'error' &&
          !{'failed', 'cancelled'}.contains(snapshot.status)) {
        throw invalidChat;
      }
      if (previous.terminal && !snapshot.terminal) throw invalidChat;
      assistant = snapshot;
      terminalEvent = event.name == 'done' || event.name == 'error';
    }
  }
}
