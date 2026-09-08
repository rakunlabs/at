import 'dart:convert';
import 'dart:math';

import 'auth_models.dart';
import 'server.dart';

const invalidChat = VerificationFailure(
  'Invalid conversation data. Reload the saved conversation.',
);
const maxAssistantBytes = 1024 * 1024;
// Go JSON can expand each text byte to a six-byte escape. These wire bounds
// include a full escaped assistant, accepted user text, and snapshot metadata.
const maxChatMessageWireBytes = 8 * 1024 * 1024;
const chatMessagePageSize = 2;
const maxChatMessagePageWireBytes =
    chatMessagePageSize * maxChatMessageWireBytes + 65536;
String chatId(Object? value) {
  if (value is! String ||
      !RegExp(r'^[0-7][0-9A-HJKMNP-TV-Z]{25}$').hasMatch(value)) {
    throw invalidChat;
  }
  return value;
}

String chatText(Object? value, int maxBytes, {bool blank = false}) {
  if (value is! String ||
      value.contains('\x00') ||
      (!blank && value.trim().isEmpty) ||
      utf8.encode(value).length > maxBytes) {
    throw invalidChat;
  }
  return value;
}

Map<String, dynamic> chatObject(Object? value) {
  if (value is! Map<String, dynamic>) throw invalidChat;
  return value;
}

String newRequestId() {
  final random = Random.secure();
  final bytes = List.generate(16, (_) => random.nextInt(256));
  bytes[6] = (bytes[6] & 15) | 64;
  bytes[8] = (bytes[8] & 63) | 128;
  final hex = bytes.map((b) => b.toRadixString(16).padLeft(2, '0')).join();
  return '${hex.substring(0, 8)}-${hex.substring(8, 12)}-${hex.substring(12, 16)}-${hex.substring(16, 20)}-${hex.substring(20)}';
}

class ChatModel {
  ChatModel.parse(Object? value) {
    final map = chatObject(value);
    if (map.length != 2) throw invalidChat;
    provider = chatText(map['provider_key'], 256);
    model = chatText(map['model'], 256);
  }
  late final String provider, model;
  String get label => '$provider / $model';
}

class Conversation {
  Conversation.parse(Object? value, String owner) {
    final map = chatObject(value);
    id = chatId(map['id']);
    if (map['owner_user_id'] != owner) throw invalidChat;
    title = chatText(map['title'], 256);
    provider = chatText(map['provider_key'], 256);
    model = chatText(map['model'], 256);
    systemPrompt = chatText(map['system_prompt'], 32768, blank: true);
    created = authTime(map['created_at']);
    updated = authTime(map['updated_at']);
  }
  late final String id, title, provider, model, systemPrompt;
  late final DateTime created, updated;
}

class ChatMessage {
  ChatMessage.parse(Object? value, String conversation) {
    final map = chatObject(value);
    id = chatId(map['id']);
    conversationId = chatId(map['conversation_id']);
    final order = map['sequence'];
    if (order is! int || order <= 0) throw invalidChat;
    sequence = order;
    if (conversationId != conversation) throw invalidChat;
    requestId = chatText(map['request_id'], 128);
    if (requestId.trim() != requestId) throw invalidChat;
    role = chatText(map['role'], 16);
    status = chatText(map['status'], 16);
    if (!{'user', 'assistant'}.contains(role) ||
        !{
          'pending',
          'streaming',
          'completed',
          'failed',
          'cancelled',
        }.contains(status)) {
      throw invalidChat;
    }
    content = chatText(
      map['content'],
      role == 'user' ? 32768 : maxAssistantBytes,
      blank: role != 'user',
    );
    provider = chatText(map['provider_key'], 256);
    model = chatText(map['model'], 256);
    finishReason = chatText(map['finish_reason'], 64, blank: true);
    if (!{'', 'stop', 'length', 'content_filter'}.contains(finishReason)) {
      throw invalidChat;
    }
    error = chatText(map['error'], 128, blank: true);
    final usageMap = chatObject(map['usage']);
    usage = {
      for (final key in [
        'prompt_tokens',
        'completion_tokens',
        'cache_read_tokens',
        'cache_write_tokens',
        'reasoning_tokens',
        'total_tokens',
      ])
        key: _count(usageMap[key]),
    };
    if (role == 'user' &&
        (status != 'completed' ||
            error.isNotEmpty ||
            finishReason.isNotEmpty ||
            usage.values.any((v) => v != 0))) {
      throw invalidChat;
    }
    created = authTime(map['created_at']);
  }
  ChatMessage.delta(ChatMessage previous, String addition)
    : id = previous.id,
      sequence = previous.sequence,
      conversationId = previous.conversationId,
      requestId = previous.requestId,
      role = previous.role,
      provider = previous.provider,
      model = previous.model,
      content = previous.content + addition,
      status = 'streaming',
      finishReason = '',
      error = '',
      usage = previous.usage,
      created = previous.created;
  late final String id,
      conversationId,
      requestId,
      role,
      content,
      status,
      provider,
      model,
      finishReason,
      error;
  late final Map<String, int> usage;
  late final DateTime created;
  late final int sequence;
  bool get terminal => {'completed', 'failed', 'cancelled'}.contains(status);
  static int _count(Object? value) {
    if (value is! int || value < 0) throw invalidChat;
    return value;
  }

  String get statusLabel {
    if (status == 'failed') {
      return switch (error) {
        'model_unavailable' => 'Failed: selected model is unavailable',
        'output_limit' => 'Failed: reply reached the text limit',
        'generation_timeout' => 'Failed: generation timed out',
        'generation_interrupted' ||
        'incomplete_stream' => 'Failed: generation was interrupted',
        'unsupported_output' => 'Failed: model returned unsupported output',
        _ => 'Failed: server could not finish this reply',
      };
    }
    if (status == 'completed' && finishReason == 'length') {
      return 'Completed: model output limit reached';
    }
    if (status == 'completed' && finishReason == 'content_filter') {
      return 'Completed: content filtered';
    }
    return switch (status) {
      'pending' => 'Pending',
      'streaming' => 'Generating',
      'cancelled' => 'Cancelled',
      _ => 'Completed',
    };
  }
}

class ChatPage<T> {
  ChatPage.parse(
    Object? value,
    T Function(Object?) parse, {
    required int limit,
    String? before,
  }) {
    final map = chatObject(value);
    if (map['items'] is! List || map['next_before'] is! String) {
      throw invalidChat;
    }
    final rows = map['items'] as List;
    if (rows.length > limit) throw invalidChat;
    items = rows.map(parse).toList();
    var previousId = before;
    int? previousSequence;
    final ids = <String>{};
    for (var i = 0; i < rows.length; i++) {
      final id = chatId(chatObject(rows[i])['id']);
      if (!ids.add(id) || id == before) throw invalidChat;
      final item = items[i];
      if (item is ChatMessage) {
        if (previousSequence != null && item.sequence >= previousSequence) {
          throw invalidChat;
        }
        previousSequence = item.sequence;
      } else if (previousId != null && id.compareTo(previousId) >= 0) {
        throw invalidChat;
      }
      previousId = id;
    }
    nextBefore = map['next_before'] as String;
    if (nextBefore.isNotEmpty &&
        (rows.isEmpty ||
            chatId(nextBefore) != previousId ||
            rows.length != limit)) {
      throw invalidChat;
    }
  }
  late final List<T> items;
  late final String nextBefore;
}

String chatFailure(Object error) => switch (error) {
  HttpFailure(status: 401) => 'Your session needs verification. Return to Account and check it before trying again.',
  HttpFailure(status: 403) =>
    'Personal conversations require an administrator account.',
  HttpFailure(status: 404) =>
    'Conversation or message not found, or not owned by this account.',
  HttpFailure(status: 409) => 'Conversation is busy or the request conflicts. Recover or cancel the current reply.',
  HttpFailure(status: 400) => 'The server rejected these settings or text. Check the selected model and size limits.',
  HttpFailure(status: 503) =>
    'Personal conversations are unavailable on this server.',
  _ => 'Could not complete the conversation request. Reload or recover the saved state.',
};
