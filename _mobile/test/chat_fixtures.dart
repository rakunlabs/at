import 'dart:async';
import 'dart:convert';

import 'package:at_mobile/auth.dart';
import 'package:at_mobile/auth_models.dart';
import 'package:at_mobile/server.dart';
import 'package:dio/dio.dart';

import 'auth_fixtures.dart';

String cid(int n) => '01K${n.toString().padLeft(23, '0')}';
Map<String, dynamic> conversationJson([int n = 1]) => {
  'id': cid(n),
  'owner_user_id': '01USER',
  'title': 'Trip planning',
  'provider_key': 'openai',
  'model': 'gpt-4.1',
  'system_prompt': 'Be concise.',
  'created_at': authNow.toIso8601String(),
  'updated_at': authNow.toIso8601String(),
};
Map<String, dynamic> messageJson({
  int n = 3,
  int? sequence,
  String? conversation,
  String request = 'request-1',
  String role = 'assistant',
  String content = '',
  String status = 'pending',
}) => {
  'id': cid(n),
  'sequence': sequence ?? n,
  'conversation_id': conversation ?? cid(1),
  'request_id': request,
  'role': role,
  'content': content,
  'status': status,
  'provider_key': 'openai',
  'model': 'gpt-4.1',
  'finish_reason': status == 'completed' && role == 'assistant' ? 'stop' : '',
  'error': status == 'failed' ? 'generation_interrupted' : '',
  'usage': {
    for (final k in [
      'prompt_tokens',
      'completion_tokens',
      'cache_read_tokens',
      'cache_write_tokens',
      'reasoning_tokens',
      'total_tokens',
    ])
      k: 0,
  },
  'created_at': authNow.toIso8601String(),
};
List<int> eventBytes(String name, Object data) =>
    utf8.encode('event: $name\ndata: ${jsonEncode(data)}\n\n');

class DurableChatBackend {
  final conversations = <String, Map<String, dynamic>>{};
  final messages = <String, Map<String, dynamic>>{};
  int seq = 10;
  String mode = 'complete';
  bool closed = false;
  final sends = <Map<String, dynamic>>[];

  Future<Object?> request(AuthRequest req) async {
    final segments = req.url.pathSegments;
    final index = segments.indexOf('conversations');
    if (index < 0) throw const HttpFailure(404, null);
    final suffix = segments.skip(index + 1).toList();
    if (suffix.length == 1 && suffix.single == 'models') {
      return [
        {'provider_key': 'openai', 'model': 'gpt-4.1'},
      ];
    }
    if (suffix.isEmpty) {
      if (req.method == 'POST') {
        final row = {...conversationJson(++seq), ...req.data!};
        conversations[row['id'] as String] = row;
        return row;
      }
      return page(conversations.values.toList(), req.url);
    }
    final id = suffix.first;
    if (!conversations.containsKey(id)) throw const HttpFailure(404, null);
    if (suffix.length == 1) {
      if (req.method == 'DELETE') {
        conversations.remove(id);
        messages.removeWhere((_, m) => m['conversation_id'] == id);
        return null;
      }
      if (req.method == 'PATCH') {
        conversations[id] = {...conversations[id]!, ...req.data!};
      }
      return conversations[id];
    }
    if (suffix.length == 2) {
      return page(
        messages.values.where((m) => m['conversation_id'] == id).toList(),
        req.url,
      );
    }
    final messageId = suffix[2];
    final row = messages[messageId];
    if (row == null || row['conversation_id'] != id) {
      throw const HttpFailure(404, null);
    }
    if (suffix.last == 'cancel') {
      messages[messageId] = {
        ...row,
        'status': 'cancelled',
        'error': 'generation_cancelled',
      };
    }
    return messages[messageId];
  }

  Map<String, dynamic> page(List<Map<String, dynamic>> rows, Uri url) {
    final before = url.queryParameters['before'];
    final limit = int.parse(url.queryParameters['limit'] ?? '10');
    if (url.path.endsWith('/messages')) {
      final beforeSequence = before == null
          ? null
          : messages[before]!['sequence'] as int;
      rows =
          rows
              .where(
                (r) =>
                    beforeSequence == null ||
                    (r['sequence'] as int) < beforeSequence,
              )
              .toList()
            ..sort(
              (a, b) => (b['sequence'] as int).compareTo(a['sequence'] as int),
            );
      final items = rows.take(limit).toList();
      return {
        'items': items,
        'next_before': items.length == limit ? items.last['id'] : '',
      };
    }
    rows =
        rows
            .where(
              (r) =>
                  before == null || (r['id'] as String).compareTo(before) < 0,
            )
            .toList()
          ..sort((a, b) => (b['id'] as String).compareTo(a['id'] as String));
    final items = rows.take(limit).toList();
    return {
      'items': items,
      'next_before': items.length == limit ? items.last['id'] : '',
    };
  }

  Stream<List<int>> send(
    Uri url,
    Map<String, dynamic> data,
    CancelToken cancel,
  ) async* {
    sends.add(Map.of(data));
    if (mode == 'unknown') return;
    final id = url.pathSegments[url.pathSegments.length - 2];
    final old = messages.values
        .where(
          (m) =>
              m['conversation_id'] == id &&
              m['request_id'] == data['request_id'],
        )
        .toList();
    Map<String, dynamic> user, assistant;
    if (old.isEmpty) {
      user = messageJson(
        n: ++seq,
        conversation: id,
        request: data['request_id'] as String,
        role: 'user',
        content: data['content'] as String,
        status: 'completed',
      );
      assistant = messageJson(
        n: ++seq,
        conversation: id,
        request: data['request_id'] as String,
      );
      messages[user['id'] as String] = user;
      messages[assistant['id'] as String] = assistant;
    } else {
      user = old.firstWhere((m) => m['role'] == 'user');
      assistant = old.firstWhere((m) => m['role'] == 'assistant');
      if (user['content'] != data['content']) {
        throw const HttpFailure(409, null);
      }
    }
    try {
      yield eventBytes('accepted', {
        'user': user,
        'assistant': assistant,
        'replay': old.isNotEmpty,
      });
      if (mode == 'hold') {
        await cancel.whenCancel;
        return;
      }
      if (mode == 'active') {
        yield eventBytes('snapshot', assistant);
        return;
      }
      final terminal = {
        ...assistant,
        'content': 'Saved reply \u{1F30D}',
        'status': mode == 'complete' ? 'completed' : 'failed',
        'finish_reason': mode == 'complete' ? 'stop' : '',
        'error': mode == 'complete' ? '' : 'generation_interrupted',
      };
      messages[assistant['id'] as String] = terminal;
      if (mode == 'offset') {
        yield eventBytes('delta', {
          'assistant_message_id': assistant['id'],
          'offset': 99,
          'content': 'Uncommitted text',
        });
        return;
      }
      if (mode == 'eof') {
        yield eventBytes('delta', {
          'assistant_message_id': assistant['id'],
          'offset': 0,
          'content': 'Uncommitted text',
        });
        return;
      }
      yield eventBytes('delta', {
        'assistant_message_id': assistant['id'],
        'offset': 0,
        'content': 'Provisional reply',
      });
      yield eventBytes('done', terminal);
    } finally {
      closed = true;
    }
  }
}

class ChatTransport extends FakeAuthTransport {
  ChatTransport(this.backend, {this.admin = true}) {
    handler = (req) async {
      if (req.url.path.endsWith('/auth/me')) return meJson(tokens);
      if (req.url.path.endsWith('/auth/mobile/logout')) return null;
      return backend.request(req);
    };
  }
  final DurableChatBackend backend;
  final bool admin;
  Map<String, dynamic> get tokens => {
    ...tokenJson(),
    'identity': {
      ...tokenJson()['identity'] as Map<String, dynamic>,
      if (admin) 'roles': ['admin'],
    },
  };
  @override
  Stream<List<int>> requestStream(
    Uri url, {
    required Map<String, dynamic> data,
    required String bearer,
    required CancelToken cancel,
  }) => backend.send(url, data, cancel);
}

Future<MobileSession> chatSession(ChatTransport transport) async {
  final vault = FakeVault()
    ..values[authServer.issuer] = jsonEncode(transport.tokens);
  final session = MobileSession(
    MobileAuth.parse(descriptor(), authServer),
    transport: transport,
    store: vault,
    browser: FakeBrowser(),
    clock: () => authNow,
  );
  await session.restore();
  return session;
}
