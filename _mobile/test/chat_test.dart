import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:at_mobile/auth.dart';
import 'package:at_mobile/auth_models.dart';
import 'package:at_mobile/chat.dart';
import 'package:at_mobile/chat_models.dart';
import 'package:at_mobile/chat_stream.dart';
import 'package:at_mobile/server.dart';
import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';

import 'auth_fixtures.dart';
import 'chat_fixtures.dart';

void main() {
  test('DTOs validate owner, safe catalog, required usage and chronological cursor', () {
    expect(Conversation.parse(conversationJson(), '01USER').id, cid(1));
    expect(
      () => Conversation.parse(conversationJson(), 'another-owner'),
      throwsA(isA<VerificationFailure>()),
    );
    expect(
      () => ChatModel.parse({
        'provider_key': 'p',
        'model': 'm',
        'api_key': 'secret',
      }),
      throwsA(isA<VerificationFailure>()),
    );
    expect(
      () => ChatMessage.parse({...messageJson(), 'usage': {}}, cid(1)),
      throwsA(isA<VerificationFailure>()),
    );
    expect(
      () => ChatMessage.parse(messageJson(conversation: cid(2)), cid(1)),
      throwsA(isA<VerificationFailure>()),
    );
    final rows = [messageJson(n: 5), messageJson(n: 4)];
    final page = ChatPage.parse(
      {'items': rows, 'next_before': cid(4)},
      (v) => ChatMessage.parse(v, cid(1)),
      limit: 2,
      before: cid(6),
    );
    expect(page.items.reversed.map((m) => m.id), [cid(4), cid(5)]);
    for (final data in [
      {'items': rows.reversed.toList(), 'next_before': ''},
      {'items': rows, 'next_before': cid(5)},
      {'items': null, 'next_before': ''},
    ]) {
      expect(
        () =>
            ChatPage.parse(data, (v) => ChatMessage.parse(v, cid(1)), limit: 2),
        throwsA(isA<VerificationFailure>()),
      );
    }
  });

  test('text limits count UTF-8 bytes and requests use unique UUID v4', () {
    expect(chatText('\u{1F30D}' * 8192, 32768).length, 16384);
    expect(
      () => chatText('\u{1F30D}' * 8193, 32768),
      throwsA(isA<VerificationFailure>()),
    );
    expect(
      () => chatText('hello\x00', 32768),
      throwsA(isA<VerificationFailure>()),
    );
    final first = newRequestId();
    expect(
      first,
      matches(
        r'^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$',
      ),
    );
    expect(first, isNot(newRequestId()));
  });

  test('SSE handles every UTF-8 byte boundary and authoritative terminal replacement', () async {
    final bytes = [
      ...eventBytes('accepted', {
        'user': messageJson(
          n: 2,
          role: 'user',
          content: 'Hi',
          status: 'completed',
        ),
        'assistant': messageJson(),
        'replay': false,
      }),
      ...eventBytes('delta', {
        'assistant_message_id': cid(3),
        'offset': 0,
        'content': '\u{1F30D}',
      }),
      ...eventBytes('delta', {
        'assistant_message_id': cid(3),
        'offset': 4,
        'content': '\u00e9',
      }),
      ...utf8.encode('event: future-event\ndata: not JSON\n\n'),
      ...eventBytes(
        'done',
        messageJson(content: 'Authoritative', status: 'completed'),
      ),
    ];
    final turn = ChatTurn(cid(1), 'request-1', 'Hi');
    final events = await parseChatEvents(
      Stream.fromIterable(bytes.map((b) => [b])),
    ).toList();
    for (final event in events) {
      turn.apply(event);
    }
    expect(events.length, 4);
    expect(turn.assistant!.content, 'Authoritative');
    expect(turn.terminalEvent, true);
  });

  test(
    'SSE rejects bad offsets IDs sequence and premature EOF is not completion',
    () async {
      ChatTurn accepted() {
        final turn = ChatTurn(cid(1), 'request-1', 'Hi');
        turn.apply(
          ChatEvent('accepted', {
            'user': messageJson(
              n: 2,
              role: 'user',
              content: 'Hi',
              status: 'completed',
            ),
            'assistant': messageJson(content: '\u{1F30D}', status: 'streaming'),
            'replay': true,
          }),
        );
        return turn;
      }

      expect(accepted().terminalEvent, false);
      for (final delta in [
        {'assistant_message_id': cid(3), 'offset': 2, 'content': 'x'},
        {'assistant_message_id': cid(4), 'offset': 4, 'content': 'x'},
      ]) {
        expect(
          () => accepted().apply(ChatEvent('delta', delta)),
          throwsA(isA<VerificationFailure>()),
        );
      }
      expect(
        () => ChatTurn(
          cid(1),
          'request-1',
          'Hi',
        ).apply(ChatEvent('done', messageJson(status: 'completed'))),
        throwsA(isA<VerificationFailure>()),
      );
      await expectLater(
        parseChatEvents(Stream.value(utf8.encode('event: done\ndata: {}')))
            .toList(),
        throwsA(isA<VerificationFailure>()),
      );
    },
  );

  test(
    'SSE bounds frames before JSON decoding and rejects malformed UTF-8',
    () async {
      await expectLater(
        parseChatEvents(
          Stream.value(utf8.encode('event: unknown\ndata: ${'x' * 200}\n\n')),
          maxFrameBytes: 128,
        ).toList(),
        throwsA(isA<VerificationFailure>()),
      );
      await expectLater(
        parseChatEvents(Stream.value([0xff])).toList(),
        throwsFormatException,
      );
    },
  );

  group('durable client', () {
    late DurableChatBackend backend;
    late ChatTransport transport;
    late MobileSession session;
    late PersonalChatApi api;
    late ConversationController controller;
    setUp(() async {
      backend = DurableChatBackend()
        ..conversations[cid(1)] = conversationJson();
      transport = ChatTransport(backend);
      session = await chatSession(transport);
      api = PersonalChatApi(session);
      controller = ConversationController(
        api,
        Conversation.parse(conversationJson(), '01USER'),
        pollInterval: const Duration(milliseconds: 5),
        recoveryLimit: const Duration(milliseconds: 50),
      );
      await controller.load();
    });
    tearDown(() {
      controller.dispose();
      session.dispose();
      transport.close();
    });

    test(
      'create rename delete and two clients load identical persisted messages',
      () async {
        final models = await api.models();
        final created = await api.create('Travel', models.single, 'Be kind');
        expect((await api.rename(created.id, 'Renamed')).title, 'Renamed');
        await api.delete(created.id);
        expect(backend.conversations.containsKey(created.id), false);
        await controller.send('A trip question');
        expect(controller.messages.map((m) => m.content), [
          'A trip question',
          'Saved reply \u{1F30D}',
        ]);
        final secondTransport = ChatTransport(backend);
        final secondSession = await chatSession(secondTransport);
        final second = PersonalChatApi(secondSession);
        final page = await second.messages(cid(1));
        expect(
          page.items.reversed.map((m) => m.content),
          controller.messages.map((m) => m.content),
        );
        secondSession.dispose();
        secondTransport.close();
      },
    );

    test(
      'messages paginate older without discarding the newest page',
      () async {
        for (var n = 20; n < 35; n++) {
          backend.messages[cid(n)] = messageJson(
            n: n,
            content: 'Reply $n',
            status: 'completed',
          );
        }
        await controller.load();
        expect(controller.messages.length, 2);
        expect(controller.nextBefore, cid(33));
        while (controller.nextBefore.isNotEmpty) {
          await controller.load(older: true);
        }
        expect(controller.messages.length, 15);
        expect(controller.messages.first.id, cid(20));
        expect(controller.nextBefore, '');
      },
    );

    for (final mode in ['eof', 'offset']) {
      test(
        '$mode recovers committed partial snapshot without another POST',
        () async {
          backend.mode = mode;
          await controller.send('Hello');
          expect(backend.sends.length, 1);
          expect(controller.messages.last.status, 'failed');
          expect(controller.messages.last.content, 'Saved reply \u{1F30D}');
          expect(controller.pending, isNull);
          expect(controller.canSend, true);
        },
      );
    }

    test('unknown acceptance replays only explicitly with same request and exact content', () async {
      backend.mode = 'unknown';
      await controller.send(' Hello ');
      expect(backend.sends.length, 1);
      expect(controller.canSend, false);
      final request = controller.pending!.requestId;
      backend.mode = 'complete';
      await controller.send('', replay: true);
      expect(backend.sends.length, 2);
      expect(backend.sends.last, {'content': ' Hello ', 'request_id': request});
      expect(backend.messages.length, 2);
    });

    test(
      'active replay polls boundedly and stops on terminal snapshot',
      () async {
        backend.mode = 'active';
        await controller.send('Hello');
        expect(controller.recovering, true);
        final id = controller.activeId!;
        backend.messages[id] = {
          ...backend.messages[id]!,
          'status': 'completed',
          'finish_reason': 'stop',
          'content': 'Stored finish',
        };
        await Future<void>.delayed(const Duration(milliseconds: 20));
        expect(controller.activeId, isNull);
        expect(controller.messages.last.content, 'Stored finish');
        expect(controller.recovering, false);
      },
    );

    test(
      'explicit cancel replaces provisional state with terminal cancellation',
      () async {
        backend.mode = 'hold';
        final sending = controller.send('Hello');
        while (controller.activeId == null) {
          await Future<void>.delayed(Duration.zero);
        }
        await controller.cancelReply();
        await sending;
        expect(controller.messages.last.status, 'cancelled');
        expect(controller.pending, isNull);
        expect(backend.closed, true);
      },
    );

    test(
      'recovery polling pauses at its deadline without inventing completion',
      () async {
        backend.mode = 'active';
        await controller.send('Still active');
        await Future<void>.delayed(const Duration(milliseconds: 100));
        expect(controller.recovering, false);
        expect(controller.activeId, isNotNull);
        expect(controller.messages.last.terminal, false);
        expect(controller.error, contains('Recovery paused'));
        final calls = transport.calls.length;
        await Future<void>.delayed(const Duration(milliseconds: 30));
        expect(transport.calls.length, calls);
      },
    );

    test(
      'logout cancels stream and removes all local issuer transcript state',
      () async {
        backend.mode = 'hold';
        final sending = controller.send('Private text');
        while (controller.activeId == null) {
          await Future<void>.delayed(Duration.zero);
        }
        await session.logout();
        await sending;
        expect(backend.closed, true);
        expect(controller.messages, isEmpty);
        expect(controller.pending, isNull);
        expect(api.live, false);
      },
    );
  });

  test(
    'non-admin cannot call chat routes even with direct navigation/API access',
    () async {
      final transport = ChatTransport(DurableChatBackend(), admin: false);
      final session = await chatSession(transport);
      await expectLater(
        PersonalChatApi(session).models(),
        throwsA(isA<VerificationFailure>()),
      );
      expect(transport.calls.length, 1);
      session.dispose();
      transport.close();
    },
  );

  test(
    'mutation has bearer me preflight and never retries failed POST',
    () async {
      final transport = ChatTransport(DurableChatBackend());
      final session = await chatSession(transport);
      transport.handler = (req) async {
        if (req.url.path.endsWith('/auth/me')) return meJson(transport.tokens);
        throw TimeoutException('private error');
      };
      await expectLater(
        PersonalChatApi(session).create(
          'Title',
          ChatModel.parse({'provider_key': 'p', 'model': 'm'}),
          '',
        ),
        throwsA(isA<TimeoutException>()),
      );
      expect(transport.calls.where((r) => r.method == 'POST').length, 1);
      expect(transport.calls[1].url.path, '/at/auth/me');
      expect(transport.calls.last.bearer, transport.tokens['access_token']);
      session.dispose();
      transport.close();
    },
  );

  test('mutation refreshes near-expiry access once before sending', () async {
    final backend = DurableChatBackend();
    final transport = ChatTransport(backend);
    var clock = authNow;
    final vault = FakeVault()
      ..values[authServer.issuer] = jsonEncode(transport.tokens);
    final session = MobileSession(
      MobileAuth.parse(descriptor(), authServer),
      transport: transport,
      store: vault,
      browser: FakeBrowser(),
      clock: () => clock,
    );
    await session.restore();
    clock = authNow.add(const Duration(minutes: 9, seconds: 45));
    transport.handler = (request) async {
      if (request.url.path.endsWith('/auth/me')) {
        return meJson(transport.tokens);
      }
      if (request.url.path.endsWith('/refresh')) {
        return {
          ...transport.tokens,
          'access_token': secret(2),
          'refresh_token': secret(12),
          'access_expires_at': clock
              .add(const Duration(minutes: 10))
              .toIso8601String(),
        };
      }
      expect(request.bearer, secret(2));
      return backend.request(request);
    };
    await PersonalChatApi(session).create(
      'Fresh access',
      ChatModel.parse({'provider_key': 'openai', 'model': 'gpt-4.1'}),
      '',
    );
    expect(
      transport.calls.where((r) => r.url.path.endsWith('/refresh')).length,
      1,
    );
    expect(transport.calls.last.url.path, '/at/api/v1/conversations');
    session.dispose();
    transport.close();
  });

  test('resource 401 with live me does not rotate or replay the GET', () async {
    final transport = ChatTransport(DurableChatBackend());
    final session = await chatSession(transport);
    transport.handler = (request) async {
      if (request.url.path.endsWith('/auth/me')) {
        return meJson(transport.tokens);
      }
      throw const HttpFailure(401, null);
    };
    await expectLater(
      PersonalChatApi(session).conversations(),
      throwsA(isA<HttpFailure>()),
    );
    expect(
      transport.calls
          .where((r) => r.url.path.endsWith('/conversations'))
          .length,
      1,
    );
    expect(
      transport.calls.where((r) => r.url.path.endsWith('/refresh')),
      isEmpty,
    );
    session.dispose();
    transport.close();
  });

  test('streaming POST refuses redirects without cookies or automatic retries', () async {
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    var requests = 0;
    server.listen((req) async {
      requests++;
      expect(req.method, 'POST');
      expect(req.headers.value('cookie'), isNull);
      expect(req.headers.value('authorization'), 'Bearer ${secret(1)}');
      req.response.statusCode = 307;
      req.response.headers.set('location', 'https://evil.example/');
      await req.response.close();
    });
    final client = StatusClient();
    final cancel = CancelToken();
    try {
      await expectLater(
        client
            .requestStream(
              Uri.parse(
                'http://127.0.0.1:${server.port}/at/api/v1/conversations/${cid(1)}/messages',
              ),
              data: {'content': 'Hello', 'request_id': newRequestId()},
              bearer: secret(1),
              cancel: cancel,
            )
            .toList(),
        throwsA(isA<HttpFailure>()),
      );
      expect(requests, 1);
      expect(cancel.isCancelled, false);
    } finally {
      client.close();
      await server.close(force: true);
    }
  });
}
